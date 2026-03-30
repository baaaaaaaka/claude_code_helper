package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

var (
	execLookPathFn                = exec.LookPath
	resolveExecutablePathFn       = resolveExecutablePath
	currentProxyVersionFn         = currentProxyVersion
	resolveClaudeVersionFn        = resolveClaudeVersion
	hashFileSHA256Fn              = hashFileSHA256
	shouldSkipPatchFailureFn      = shouldSkipPatchFailure
	newPatchHistoryStoreFn        = config.NewPatchHistoryStore
	patchExecutableFn             = patchExecutable
	adhocCodesignFn               = adhocCodesign
	runClaudeProbeFn              = runClaudeProbe
	applyClaudeGlibcCompatPatchFn = applyClaudeGlibcCompatPatch
	restoreExecutableFromBackupFn = restoreExecutableFromBackup
	cleanupPatchHistoryFn         = cleanupPatchHistory
	recordPatchFailureFn          = recordPatchFailure
)

type exePatchOptions struct {
	enabledFlag              bool
	regex1                   string
	regex2                   []string
	regex3                   []string
	replace                  []string
	preview                  bool
	policySettings           bool
	dryRun                   bool
	glibcCompat              bool
	glibcCompatRoot          string
	glibcCompatPreferWrapper bool
}

type patchOutcome struct {
	Applied                  bool
	SourcePath               string
	SourceSHA256             string
	TargetPath               string
	BackupPath               string
	SpecsHash                string
	HistoryStore             *config.PatchHistoryStore
	TargetSHA256             string
	TargetVersion            string
	LaunchArgsPrefix         []string
	IsClaude                 bool
	AlreadyPatched           bool
	Verified                 bool
	NeedsVerification        bool
	RollbackOnStartupFailure bool
	ConfigPath               string
	LogWriter                io.Writer
	readiness                *patchReadiness
	PatchStats               []exePatchStats
}

func (o exePatchOptions) enabled() bool {
	if !o.enabledFlag {
		return false
	}
	return o.policySettings || o.customRulesEnabled() || o.glibcCompatConfigured()
}

func (o exePatchOptions) customRulesEnabled() bool {
	return o.regex1 != "" || len(o.regex2) > 0 || len(o.regex3) > 0 || len(o.replace) > 0
}

func (o exePatchOptions) glibcCompatConfigured() bool {
	return o.glibcCompat
}

func (o exePatchOptions) withGlibcCompatWrapperFallback() exePatchOptions {
	o.glibcCompatPreferWrapper = true
	return o
}

func (o exePatchOptions) validate() error {
	if !o.enabled() {
		return nil
	}

	if !o.customRulesEnabled() {
		return nil
	}

	missing := make([]string, 0, 4)
	if o.regex1 == "" {
		missing = append(missing, "--exe-patch-regex-1")
	}
	if len(o.regex2) == 0 {
		missing = append(missing, "--exe-patch-regex-2")
	}
	if len(o.regex3) == 0 {
		missing = append(missing, "--exe-patch-regex-3")
	}
	if len(o.replace) == 0 {
		missing = append(missing, "--exe-patch-replace")
	}

	if len(missing) > 0 {
		return fmt.Errorf("exe patch requires %s", strings.Join(missing, ", "))
	}
	if len(o.regex2) != len(o.regex3) || len(o.regex2) != len(o.replace) {
		return fmt.Errorf("exe patch requires the same number of --exe-patch-regex-2, --exe-patch-regex-3, and --exe-patch-replace values")
	}
	return nil
}

type exePatchSpec struct {
	match       *regexp.Regexp
	guard       *regexp.Regexp
	patch       *regexp.Regexp
	replace     []byte
	fixedLength bool
	label       string
	apply       func([]byte, io.Writer, bool) ([]byte, exePatchStats, error)
	applyID     string
}

type exePatchStats struct {
	Label        string
	Segments     int
	Eligible     int
	Patched      int
	Changed      int
	Replacements int
}

const (
	// Match the settings getter that starts with a policySettings guard.
	policySettingsGetterStage1 = `function\s+[A-Za-z0-9_$]+\s*\(\s*[A-Za-z0-9_$]+\s*\)\s*\{\s*if\(\s*(?:[A-Za-z0-9_$]+\s*={2,3}\s*['"]policySettings['"]|['"]policySettings['"]\s*={2,3}\s*[A-Za-z0-9_$]+)\s*\)\{`
)

func policySettingsSpecs() ([]exePatchSpec, error) {
	disableSpec, err := policySettingsDisablePatchSpec()
	if err != nil {
		return nil, err
	}
	gateSpec, err := bypassPermissionsGatePatchSpec()
	if err != nil {
		return nil, err
	}
	rootSpec, err := rootBypassGuardPatchSpec()
	if err != nil {
		return nil, err
	}
	remoteSpec, err := remoteSettingsDisablePatchSpec()
	if err != nil {
		return nil, err
	}

	return []exePatchSpec{disableSpec, gateSpec, rootSpec, remoteSpec}, nil
}

func policySettingsDisablePatchSpec() (exePatchSpec, error) {
	startRe, err := regexp.Compile(policySettingsGetterStage1)
	if err != nil {
		return exePatchSpec{}, fmt.Errorf("compile policySettings getter regex: %w", err)
	}
	return exePatchSpec{
		match:   startRe,
		label:   "policySettings-disable",
		applyID: "policySettings-disable-v1",
		apply: func(data []byte, log io.Writer, preview bool) ([]byte, exePatchStats, error) {
			return applyPolicySettingsDisablePatch(data, startRe, log, preview)
		},
		fixedLength: true,
	}, nil
}

func bypassPermissionsGatePatchSpec() (exePatchSpec, error) {
	return exePatchSpec{
		label:   "bypass-permissions-gate",
		applyID: "bypass-permissions-gate-v1",
		apply: func(data []byte, log io.Writer, preview bool) ([]byte, exePatchStats, error) {
			return applyBypassPermissionsGatePatch(data, log, preview)
		},
		fixedLength: true,
	}, nil
}

func remoteSettingsDisablePatchSpec() (exePatchSpec, error) {
	return exePatchSpec{
		label:   "remote-settings-disable",
		applyID: "remote-settings-disable-v1",
		apply: func(data []byte, log io.Writer, preview bool) ([]byte, exePatchStats, error) {
			return applyRemoteSettingsDisablePatch(data, log, preview)
		},
		fixedLength: true,
	}, nil
}

func rootBypassGuardPatchSpec() (exePatchSpec, error) {
	return exePatchSpec{
		label:   "root-bypass-guard",
		applyID: "root-bypass-guard-v2",
		apply: func(data []byte, log io.Writer, preview bool) ([]byte, exePatchStats, error) {
			return applyRootBypassGuardPatch(data, log, preview)
		},
		fixedLength: true,
	}, nil
}

func (o exePatchOptions) compile() ([]exePatchSpec, error) {
	builtin, err := o.compileBuiltinSpecs()
	if err != nil {
		return nil, err
	}
	custom, err := o.compileCustomSpecs()
	if err != nil {
		return nil, err
	}
	return append(builtin, custom...), nil
}

func (o exePatchOptions) compileBuiltinSpecs() ([]exePatchSpec, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}
	if !o.enabled() || !o.policySettings {
		return nil, nil
	}

	return policySettingsSpecs()
}

func (o exePatchOptions) compileCustomSpecs() ([]exePatchSpec, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}
	if !o.enabled() || !o.customRulesEnabled() {
		return nil, nil
	}

	re1, err := regexp.Compile(o.regex1)
	if err != nil {
		return nil, fmt.Errorf("compile --exe-patch-regex-1: %w", err)
	}

	specs := make([]exePatchSpec, 0, len(o.regex2))
	for i := range o.regex2 {
		re2, err := regexp.Compile(o.regex2[i])
		if err != nil {
			return nil, fmt.Errorf("compile --exe-patch-regex-2[%d]: %w", i, err)
		}
		re3, err := regexp.Compile(o.regex3[i])
		if err != nil {
			return nil, fmt.Errorf("compile --exe-patch-regex-3[%d]: %w", i, err)
		}

		specs = append(specs, exePatchSpec{
			match:   re1,
			guard:   re2,
			patch:   re3,
			replace: []byte(normalizeReplacement(o.replace[i])),
			label:   fmt.Sprintf("custom-%d", i+1),
		})
	}

	return specs, nil
}

func maybePatchExecutable(cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
	return maybePatchExecutableWithContext(context.Background(), cmdArgs, opts, configPath, log)
}

func maybePatchExecutableWithContext(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
	if len(cmdArgs) == 0 {
		return nil, fmt.Errorf("missing command")
	}
	if log == nil {
		log = io.Discard
	}

	builtinSpecs, err := opts.compileBuiltinSpecs()
	if err != nil {
		return nil, err
	}
	customSpecs, err := opts.compileCustomSpecs()
	if err != nil {
		return nil, err
	}
	glibcCompat := opts.glibcCompatConfigured()

	exePath, err := execLookPathFn(cmdArgs[0])
	if err != nil {
		return nil, fmt.Errorf("resolve target executable %q: %w", cmdArgs[0], err)
	}

	resolvedPath, err := resolveExecutablePathFn(exePath)
	if err != nil {
		return nil, err
	}

	isClaude := isClaudeExecutable(cmdArgs[0], resolvedPath)
	if !isClaude {
		builtinSpecs = nil
	}
	yoloRequested := hasYoloBypassPermissionsArg(cmdArgs)
	glibcCompatHost := isClaude && glibcCompat && glibcCompatHostEligibleFn()
	patchTargetPath := resolvedPath
	var compatOutcome *patchOutcome
	// Claude's built-in byte patches are only allowed for launches that
	// explicitly request `--permission-mode bypassPermissions`. When that flag
	// is absent, any previously-applied Claude byte patch must be restored
	// before launch, but Linux glibc-compat rescue remains available below.
	if isClaude && !yoloRequested {
		if err := disableClaudeBytePatch(resolvedPath, configPath, log, opts.dryRun); err != nil {
			return nil, err
		}
		builtinSpecs = nil
	}
	if glibcCompatHost {
		prepared, _, compatErr := applyClaudeGlibcCompatPatchFn(resolvedPath, opts, log, opts.dryRun, &patchOutcome{
			SourcePath:       resolvedPath,
			TargetPath:       resolvedPath,
			LaunchArgsPrefix: []string{resolvedPath},
			IsClaude:         true,
			ConfigPath:       configPath,
			LogWriter:        log,
		})
		if compatErr != nil {
			return nil, compatErr
		}
		compatOutcome = prepared
		if compatOutcome != nil && strings.TrimSpace(compatOutcome.TargetPath) != "" {
			patchTargetPath = compatOutcome.TargetPath
		}
		if isClaude && !yoloRequested && !config.PathsEqual(patchTargetPath, resolvedPath) {
			if err := disableClaudeBytePatch(patchTargetPath, configPath, log, opts.dryRun); err != nil {
				return nil, err
			}
		}
	}
	specs := append(builtinSpecs, customSpecs...)
	if len(specs) == 0 && !glibcCompat {
		return nil, nil
	}

	proxyVersion := currentProxyVersionFn()
	var historyStore *config.PatchHistoryStore
	if len(specs) > 0 {
		historyStore = initPatchHistoryStore(configPath, log)
		if isClaude {
			if err := purgeStalePatchFailures(configPath, proxyVersion); err != nil {
				_, _ = fmt.Fprintf(log, "exe-patch: failed to read patch failure config: %v\n", err)
			}
		}
	}

	var outcome *patchOutcome
	if len(specs) > 0 {
		outcome, err = findAlreadyPatchedExecutable(patchTargetPath, specs, log, historyStore, proxyVersion)
		if err != nil {
			return nil, err
		}
	}

	targetVersion := ""
	targetSHA := ""
	if outcome == nil && isClaude {
		targetVersion = resolveClaudeVersionFn(resolvedPath)
		if targetVersion == "" {
			if sha, err := hashFileSHA256Fn(resolvedPath); err == nil {
				targetSHA = sha
			}
		}
		// Previous patch failures are advisory only. Probe and startup failures
		// can be false negatives, so every launch should still retry patching at
		// least once instead of hard-skipping future attempts.
		if knownFailure, skipErr := shouldSkipPatchFailureFn(configPath, proxyVersion, targetVersion, targetSHA); skipErr == nil && knownFailure {
			if targetVersion != "" {
				_, _ = fmt.Fprintf(log, "exe-patch: previous failure recorded for claude %s with proxy %s; retrying patch\n", targetVersion, proxyVersion)
			} else {
				_, _ = fmt.Fprintf(log, "exe-patch: previous failure recorded for claude binary with proxy %s; retrying patch\n", proxyVersion)
			}
		} else if skipErr != nil {
			_, _ = fmt.Fprintf(log, "exe-patch: failed to read patch failure config: %v\n", skipErr)
		}
	}

	if historyStore == nil {
		historyStore = initPatchHistoryStore(configPath, log)
	}

	if outcome == nil && len(specs) > 0 {
		outcome, err = patchExecutableFn(patchTargetPath, specs, log, opts.preview, opts.dryRun, historyStore, proxyVersion)
		if err != nil {
			return nil, err
		}
	}
	if outcome == nil {
		if compatOutcome != nil {
			outcome = compatOutcome
			outcome.HistoryStore = historyStore
		} else {
			outcome = &patchOutcome{
				SourcePath:   resolvedPath,
				TargetPath:   patchTargetPath,
				HistoryStore: historyStore,
			}
		}
	}
	if strings.TrimSpace(outcome.SourcePath) == "" {
		outcome.SourcePath = resolvedPath
	}
	if outcome.SourceSHA256 == "" {
		outcome.SourceSHA256 = targetSHA
	}
	if compatOutcome != nil {
		if strings.TrimSpace(outcome.TargetPath) == "" {
			outcome.TargetPath = compatOutcome.TargetPath
		}
		if outcome.SourceSHA256 == "" {
			outcome.SourceSHA256 = compatOutcome.SourceSHA256
		}
		if len(outcome.LaunchArgsPrefix) == 0 {
			outcome.LaunchArgsPrefix = append([]string{}, compatOutcome.LaunchArgsPrefix...)
		}
		outcome.Applied = outcome.Applied || compatOutcome.Applied
	}
	if len(outcome.LaunchArgsPrefix) == 0 && strings.TrimSpace(outcome.TargetPath) != "" {
		outcome.LaunchArgsPrefix = []string{outcome.TargetPath}
	}

	outcome.TargetVersion = targetVersion
	if outcome.TargetSHA256 == "" {
		outcome.TargetSHA256 = targetSHA
	}
	outcome.IsClaude = isClaude
	outcome.ConfigPath = configPath
	outcome.LogWriter = log
	if runtimeGOOS == "windows" &&
		outcome.IsClaude &&
		outcome.AlreadyPatched &&
		!outcome.Verified &&
		outcome.BackupPath != "" &&
		strings.TrimSpace(outcome.TargetVersion) == "" {
		outcome.TargetVersion = resolveClaudeVersionFn(resolvedPath)
	}

	bytePatchApplied := outcome.Applied
	if bytePatchApplied && outcome.IsClaude {
		if signErr := adhocCodesignFn(resolvedPath, log); signErr != nil {
			_, _ = fmt.Fprintln(log, "exe-patch: codesign failed; restoring backup")
			if restoreErr := restoreExecutableFromBackupFn(outcome); restoreErr != nil {
				return nil, fmt.Errorf("restore patched executable: %w", restoreErr)
			}
			if historyErr := cleanupPatchHistoryFn(outcome); historyErr != nil {
				return nil, fmt.Errorf("cleanup patch history: %w", historyErr)
			}
			if recordErr := recordPatchFailureFn(configPath, outcome, formatFailureReason(signErr, "")); recordErr != nil {
				_, _ = fmt.Fprintf(log, "exe-patch: failed to record patch failure: %v\n", recordErr)
			}
			return nil, nil
		}
	}

	windowsHistoricalVerification := runtimeGOOS == "windows" && outcome.AlreadyPatched && !outcome.Verified && outcome.BackupPath != ""
	outcome.NeedsVerification = outcome.IsClaude && (bytePatchApplied || windowsHistoricalVerification)
	outcome.RollbackOnStartupFailure = bytePatchApplied || windowsHistoricalVerification

	needProbe := outcome.IsClaude && (bytePatchApplied || glibcCompat || outcome.NeedsVerification)
	if needProbe && runtimeGOOS == "windows" && !glibcCompat {
		startPatchedExecutableReadiness(ctx, outcome, opts)
		return outcome, nil
	}
	if needProbe {
		out, probeErr := runClaudeProbeOutcome(outcome, resolvedPath, "--version")
		if probeErr != nil && glibcCompat && isMissingGlibcSymbolError(out) {
			patchedOutcome, compatApplied, compatErr := applyClaudeGlibcCompatPatchFn(resolvedPath, opts, log, opts.dryRun, outcome)
			outcome = patchedOutcome
			if compatErr != nil {
				_, _ = fmt.Fprintf(log, "exe-patch: glibc compat patch failed: %v\n", compatErr)
			} else if compatApplied {
				if len(outcome.LaunchArgsPrefix) == 0 && strings.TrimSpace(outcome.TargetPath) != "" {
					outcome.LaunchArgsPrefix = []string{outcome.TargetPath}
				}
				outcome.NeedsVerification = outcome.IsClaude && outcome.Applied
				outcome.RollbackOnStartupFailure = outcome.RollbackOnStartupFailure || outcome.Applied
				out, probeErr = runClaudeProbeOutcome(outcome, resolvedPath, "--version")
			}
		}
		if probeErr != nil && glibcCompat && usesGlibcCompatMirrorLaunch(outcome, resolvedPath) {
			_, _ = fmt.Fprintf(log, "exe-patch: glibc compat mirror probe failed; retrying with wrapper: %v\n", probeErr)
			wrapperOutcome, wrapperApplied, wrapperErr := applyClaudeGlibcCompatPatchFn(
				resolvedPath,
				opts.withGlibcCompatWrapperFallback(),
				log,
				opts.dryRun,
				outcome,
			)
			if wrapperErr != nil {
				_, _ = fmt.Fprintf(log, "exe-patch: glibc compat wrapper fallback failed: %v\n", wrapperErr)
			} else if wrapperApplied {
				outcome = wrapperOutcome
				if len(outcome.LaunchArgsPrefix) == 0 && strings.TrimSpace(outcome.TargetPath) != "" {
					outcome.LaunchArgsPrefix = []string{outcome.TargetPath}
				}
				out, probeErr = runClaudeProbeOutcome(outcome, resolvedPath, "--version")
			}
		}
		if probeErr != nil {
			if outcome.Applied || outcome.NeedsVerification {
				if failureErr := handlePatchedExecutableFailure(outcome, probeErr, out); failureErr != nil {
					return nil, failureErr
				}
				return nil, nil
			}
			return outcome, nil
		}
		if outcome.NeedsVerification {
			if markErr := markPatchedExecutableVerified(outcome, time.Now()); markErr != nil {
				_, _ = fmt.Fprintf(log, "exe-patch: failed to persist patch verification: %v\n", markErr)
			}
		}
	}

	return outcome, nil
}

func initPatchHistoryStore(configPath string, log io.Writer) *config.PatchHistoryStore {
	if log == nil {
		log = io.Discard
	}
	historyStore, err := newPatchHistoryStoreFn(configPath)
	if err != nil {
		_, _ = fmt.Fprintf(log, "exe-patch: failed to init patch history: %v\n", err)
		return nil
	}
	return historyStore
}

func loadPatchHistory(store *config.PatchHistoryStore, log io.Writer) (config.PatchHistory, bool) {
	if store == nil {
		return config.PatchHistory{}, false
	}
	if log == nil {
		log = io.Discard
	}
	history, err := store.Load()
	if err != nil {
		_, _ = fmt.Fprintf(log, "exe-patch: failed to load patch history: %v\n", err)
		return config.PatchHistory{}, false
	}
	return history, true
}

func findAlreadyPatchedExecutable(path string, specs []exePatchSpec, log io.Writer, historyStore *config.PatchHistoryStore, proxyVersion string) (*patchOutcome, error) {
	if historyStore == nil || len(specs) == 0 {
		return nil, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat target executable %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("target executable %q is not a regular file", path)
	}

	currentHash, err := hashFileSHA256Fn(path)
	if err != nil {
		return nil, fmt.Errorf("hash target executable %q: %w", path, err)
	}

	specsHash := patchSpecsHash(specs)
	history, historyLoaded := loadPatchHistory(historyStore, log)
	if !historyLoaded || !history.IsPatched(path, specsHash, currentHash, proxyVersion) {
		return nil, nil
	}

	outcome := &patchOutcome{
		AlreadyPatched: true,
		HistoryStore:   historyStore,
		SpecsHash:      specsHash,
		TargetPath:     path,
		TargetSHA256:   currentHash,
		Verified:       history.IsVerified(path, specsHash, currentHash, proxyVersion),
	}
	if runtimeGOOS != "windows" && !outcome.Verified {
		outcome.Verified = true
	}

	backupPath := originalBackupPath(path)
	if info, statErr := os.Stat(backupPath); statErr == nil && info.Mode().IsRegular() {
		outcome.BackupPath = backupPath
	}
	logAlreadyPatched(log, path)
	return outcome, nil
}

func usesGlibcCompatMirrorLaunch(outcome *patchOutcome, sourcePath string) bool {
	if outcome == nil {
		return false
	}
	targetPath := strings.TrimSpace(outcome.TargetPath)
	if targetPath == "" || config.PathsEqual(targetPath, sourcePath) {
		return false
	}
	if len(outcome.LaunchArgsPrefix) == 0 {
		return false
	}
	return config.PathsEqual(strings.TrimSpace(outcome.LaunchArgsPrefix[len(outcome.LaunchArgsPrefix)-1]), targetPath)
}

func patchExecutable(path string, specs []exePatchSpec, log io.Writer, preview bool, dryRun bool, historyStore *config.PatchHistoryStore, proxyVersion string) (*patchOutcome, error) {
	if log == nil {
		log = io.Discard
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat target executable %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("target executable %q is not a regular file", path)
	}

	specsHash := patchSpecsHash(specs)
	outcome := &patchOutcome{
		TargetPath:   path,
		SpecsHash:    specsHash,
		HistoryStore: historyStore,
	}
	currentHash, err := hashFileSHA256Fn(path)
	if err != nil {
		return nil, fmt.Errorf("hash target executable %q: %w", path, err)
	}
	outcome.TargetSHA256 = currentHash
	history, historyLoaded := loadPatchHistory(historyStore, log)
	if historyLoaded {
		if history.IsPatched(path, specsHash, currentHash, proxyVersion) {
			outcome.AlreadyPatched = true
			outcome.Verified = history.IsVerified(path, specsHash, currentHash, proxyVersion)
			if runtimeGOOS != "windows" && !outcome.Verified {
				outcome.Verified = true
			}
			backupPath := originalBackupPath(path)
			if info, statErr := os.Stat(backupPath); statErr == nil && info.Mode().IsRegular() {
				outcome.BackupPath = backupPath
			}
			logAlreadyPatched(log, path)
			return outcome, nil
		}
		if entry, ok := history.Find(path, specsHash); ok {
			if entry.PatchedSHA256 == currentHash {
				prev := strings.TrimSpace(entry.ProxyVersion)
				if prev != "" && prev != strings.TrimSpace(proxyVersion) {
					_, _ = fmt.Fprintf(log, "exe-patch: proxy changed (%s -> %s); reapplying %s\n", prev, proxyVersion, path)
				}
			} else {
				_, _ = fmt.Fprintf(log, "exe-patch: hash mismatch for %s (stored=%s current=%s)\n", path, truncHash(entry.PatchedSHA256), truncHash(currentHash))
			}
		} else if len(history.Entries) > 0 {
			logPatchHistoryMiss(log, path, specsHash, history)
		}
	}

	sourcePath := path
	backupPath := originalBackupPath(path)
	if backupHash, err := hashFileSHA256Fn(backupPath); err == nil {
		// Check if the backup is stale. If the current binary matches
		// neither the backup (original) nor the stored patched hash, it
		// means the target was replaced externally (e.g. Claude Code
		// auto-update). In that case the backup belongs to a previous
		// version and must not be used as the patch source — doing so
		// would overwrite the new binary with a patched copy of the old
		// one, causing a version rollback.
		storedPatchedHash := ""
		if historyLoaded {
			if entry, ok := history.Find(path, specsHash); ok {
				storedPatchedHash = entry.PatchedSHA256
			}
		}
		backupIsStale := currentHash != backupHash &&
			storedPatchedHash != "" &&
			currentHash != storedPatchedHash
		if backupIsStale {
			_, _ = fmt.Fprintf(log, "exe-patch: backup is stale (target updated externally); refreshing %s\n", backupPath)
			if err := os.Remove(backupPath); err != nil && !os.IsNotExist(err) {
				_, _ = fmt.Fprintf(log, "exe-patch: failed to remove stale backup: %v\n", err)
			}
		} else {
			sourcePath = backupPath
			outcome.BackupPath = backupPath
		}
	} else if err != nil && !os.IsNotExist(err) {
		_, _ = fmt.Fprintf(log, "exe-patch: failed to read backup %s: %v (using current binary)\n", backupPath, err)
	}

	data, err := os.ReadFile(sourcePath)
	if err != nil {
		if sourcePath == path {
			return nil, fmt.Errorf("read target executable %q: %w", path, err)
		}
		return nil, fmt.Errorf("read patch source %q: %w", sourcePath, err)
	}

	patched, stats, err := applyExePatches(data, specs, log, preview)
	if err != nil {
		return nil, fmt.Errorf("patch target executable %q: %w", path, err)
	}

	changed := false
	touched := false
	for _, stat := range stats {
		if stat.Changed > 0 {
			changed = true
		}
		if stat.Replacements > 0 || stat.Eligible > 0 {
			touched = true
		}
	}
	var patchedHash string
	if changed {
		patchedHash = hashBytes(patched)
		if patchedHash == currentHash {
			changed = false
			for i := range stats {
				stats[i].Changed = 0
			}
		}
	}

	backupReady := outcome.BackupPath != ""
	if changed && !dryRun && !backupReady {
		backupPath, err := backupExecutable(path, info.Mode().Perm())
		if err != nil {
			if changed {
				return nil, err
			}
			_, _ = fmt.Fprintf(log, "exe-patch: failed to create backup: %v\n", err)
		} else {
			outcome.BackupPath = backupPath
			backupReady = true
		}
	}

	if changed && !dryRun {
		if !backupReady {
			return nil, fmt.Errorf("missing backup for patched executable %q", path)
		}
		outcome.Applied = true

		if err := os.WriteFile(path, patched, info.Mode().Perm()); err != nil {
			return nil, fmt.Errorf("write patched executable %q: %w", path, err)
		}
	}
	if touched && !changed {
		outcome.AlreadyPatched = true
	}

	if dryRun {
		logDryRun(log, path, changed)
	}

	if historyStore != nil && touched && !dryRun {
		entryHash := currentHash
		if changed {
			entryHash = patchedHash
		}
		verifiedAt := time.Time{}
		if historyLoaded {
			if entry, ok := history.Find(path, specsHash); ok && entry.PatchedSHA256 == entryHash {
				verifiedAt = entry.VerifiedAt
			}
		}
		if changed {
			if runtimeGOOS == "windows" {
				verifiedAt = time.Time{}
			} else {
				verifiedAt = time.Now()
			}
		} else if runtimeGOOS != "windows" && verifiedAt.IsZero() {
			verifiedAt = time.Now()
		}
		outcome.Verified = !verifiedAt.IsZero()
		entry := config.PatchHistoryEntry{
			Path:          path,
			SpecsSHA256:   specsHash,
			PatchedSHA256: entryHash,
			ProxyVersion:  proxyVersion,
			PatchedAt:     time.Now(),
			VerifiedAt:    verifiedAt,
		}
		if err := historyStore.Update(func(h *config.PatchHistory) error {
			h.Upsert(entry)
			return nil
		}); err != nil {
			_, _ = fmt.Fprintf(log, "exe-patch: failed to update patch history: %v\n", err)
		}
	}

	for _, stat := range stats {
		logPatchSummary(log, path, stat)
	}
	outcome.PatchStats = stats
	return outcome, nil
}

func resolveExecutablePath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve executable path %q: %w", path, err)
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve executable absolute path %q: %w", resolved, err)
	}
	return abs, nil
}

func originalBackupPath(path string) string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	return filepath.Join(dir, base+".claude-proxy.bak")
}

func backupExecutable(path string, perm os.FileMode) (string, error) {
	backupPath := originalBackupPath(path)
	if info, err := os.Stat(backupPath); err == nil {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("backup path %q is not a regular file", backupPath)
		}
		return backupPath, nil
	} else if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("stat backup file: %w", err)
	}

	src, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open executable for backup: %w", err)
	}
	defer src.Close()

	dst, err := os.OpenFile(backupPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return "", fmt.Errorf("create backup file: %w", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return "", fmt.Errorf("write backup file: %w", err)
	}
	if err := dst.Sync(); err != nil {
		_ = dst.Close()
		return "", fmt.Errorf("sync backup file: %w", err)
	}
	if err := dst.Close(); err != nil {
		return "", fmt.Errorf("close backup file: %w", err)
	}
	return backupPath, nil
}

func restoreExecutableFromBackup(outcome *patchOutcome) error {
	if outcome == nil || outcome.TargetPath == "" {
		return fmt.Errorf("missing backup data for restore")
	}
	restorePath := outcome.BackupPath
	if strings.TrimSpace(restorePath) == "" && outcome.SourcePath != "" && !config.PathsEqual(outcome.SourcePath, outcome.TargetPath) {
		restorePath = outcome.SourcePath
	}
	if strings.TrimSpace(restorePath) == "" {
		return fmt.Errorf("missing backup data for restore")
	}
	info, err := os.Stat(restorePath)
	if err != nil {
		return fmt.Errorf("stat backup file: %w", err)
	}
	return restoreExecutableFileWithRetry(restorePath, outcome.TargetPath, info.Mode().Perm())
}

func restoreExecutableFileWithRetry(srcPath string, dstPath string, perm os.FileMode) error {
	attempts := 1
	if runtimeGOOS == "windows" {
		attempts = 5
	}
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		err = restoreExecutableFile(srcPath, dstPath, perm)
		if err == nil {
			return nil
		}
		if !shouldRetryWindowsRestore(err) || attempt == attempts {
			return err
		}
		time.Sleep(time.Duration(attempt) * 100 * time.Millisecond)
	}
	return err
}

func restoreExecutableFile(srcPath string, dstPath string, perm os.FileMode) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("read backup file: %w", err)
	}
	defer src.Close()

	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("restore executable from backup: %w", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return fmt.Errorf("restore executable from backup: %w", err)
	}
	if err := dst.Sync(); err != nil {
		_ = dst.Close()
		return fmt.Errorf("restore executable from backup: %w", err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("restore executable from backup: %w", err)
	}
	return nil
}

func shouldRetryWindowsRestore(err error) bool {
	if runtimeGOOS != "windows" {
		return false
	}
	return errors.Is(err, syscall.Errno(32)) || errors.Is(err, syscall.Errno(33))
}

// disableClaudeBytePatch removes Claude-specific built-in byte patch state for
// launches that do not include `--permission-mode bypassPermissions`. It
// restores the original executable, clears patch metadata, and drops the stale
// backup so a future built-in patch starts from the current Claude binary
// instead of an older copy. Dry-run mode only reports what would happen.
func disableClaudeBytePatch(resolvedPath string, configPath string, log io.Writer, dryRun bool) error {
	backupPath := originalBackupPath(resolvedPath)
	info, err := os.Stat(backupPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat backup file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("backup path %q is not a regular file", backupPath)
	}

	currentHash, err := hashFileSHA256Fn(resolvedPath)
	if err != nil {
		return fmt.Errorf("hash target executable %q: %w", resolvedPath, err)
	}
	backupHash, err := hashFileSHA256Fn(backupPath)
	if err != nil {
		return fmt.Errorf("hash backup executable %q: %w", backupPath, err)
	}

	if currentHash == backupHash {
		return nil
	}

	historyStore := initPatchHistoryStore(configPath, log)

	specs, err := policySettingsSpecs()
	if err != nil {
		return err
	}
	expectedPatched, expectedPatchedHash, touched, err := claudeBuiltInPatchedBytesFromBackup(backupPath, specs)
	if err != nil {
		return err
	}

	shouldRestore := touched && strings.EqualFold(currentHash, expectedPatchedHash)
	matchedHistoryEntry := false
	if historyStore != nil {
		history, historyLoaded := loadPatchHistory(historyStore, log)
		if historyLoaded {
			specsHash := patchSpecsHash(specs)
			if entry, ok := history.Find(resolvedPath, specsHash); ok && touched && strings.EqualFold(entry.PatchedSHA256, expectedPatchedHash) {
				matchedHistoryEntry = true
				if strings.EqualFold(currentHash, entry.PatchedSHA256) {
					shouldRestore = true
				} else {
					shouldRestore, err = looksLikeClaudeBuiltInBytePatch(resolvedPath)
					if err != nil {
						return err
					}
				}
			}
		}
	}
	if !shouldRestore && touched && !matchedHistoryEntry {
		shouldRestore, err = fileHasStrictPrefix(resolvedPath, expectedPatched)
		if err != nil {
			return err
		}
	}
	if !shouldRestore {
		return nil
	}

	if dryRun {
		_, _ = fmt.Fprintf(log, "exe-patch: dry-run enabled; would restore original Claude executable %s\n", resolvedPath)
		return nil
	}
	_, _ = fmt.Fprintf(log, "exe-patch: yolo disabled; restoring original Claude executable %s\n", resolvedPath)
	if err := restoreExecutableFromBackupFn(&patchOutcome{
		TargetPath: resolvedPath,
		BackupPath: backupPath,
	}); err != nil {
		return fmt.Errorf("restore patched executable: %w", err)
	}
	if err := cleanupPatchHistoryForPath(historyStore, resolvedPath); err != nil {
		_, _ = fmt.Fprintf(log, "exe-patch: failed to cleanup patch history for %s: %v\n", resolvedPath, err)
	}
	if err := removePatchBackup(backupPath); err != nil {
		_, _ = fmt.Fprintf(log, "exe-patch: failed to remove backup %s: %v\n", backupPath, err)
	}
	return nil
}

func claudeBuiltInPatchedBytesFromBackup(backupPath string, specs []exePatchSpec) ([]byte, string, bool, error) {
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return nil, "", false, fmt.Errorf("read backup executable %q: %w", backupPath, err)
	}
	patched, stats, err := applyExePatches(data, specs, io.Discard, false)
	if err != nil {
		return nil, "", false, nil
	}
	touched := false
	for _, stat := range stats {
		if stat.Eligible > 0 || stat.Replacements > 0 {
			touched = true
			break
		}
	}
	if !touched {
		return nil, "", false, nil
	}
	return patched, hashBytes(patched), true, nil
}

func fileHasStrictPrefix(path string, prefix []byte) (bool, error) {
	if len(prefix) == 0 {
		return false, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return false, fmt.Errorf("stat target executable %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("target executable %q is not a regular file", path)
	}
	if info.Size() <= int64(len(prefix)) {
		return false, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("read target executable %q: %w", path, err)
	}
	defer f.Close()

	bufSize := 1 << 20
	if len(prefix) < bufSize {
		bufSize = len(prefix)
	}
	buf := make([]byte, bufSize)
	offset := 0
	for offset < len(prefix) {
		n := len(buf)
		if remaining := len(prefix) - offset; remaining < n {
			n = remaining
		}
		if _, err := io.ReadFull(f, buf[:n]); err != nil {
			return false, fmt.Errorf("read target executable %q: %w", path, err)
		}
		if !bytes.Equal(buf[:n], prefix[offset:offset+n]) {
			return false, nil
		}
		offset += n
	}
	return true, nil
}

func looksLikeClaudeBuiltInBytePatch(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read target executable %q: %w", path, err)
	}
	specs, err := policySettingsSpecs()
	if err != nil {
		return false, err
	}
	patched, stats, err := applyExePatches(data, specs, io.Discard, false)
	if err != nil {
		return false, nil
	}
	touched := false
	for _, stat := range stats {
		if stat.Eligible > 0 || stat.Replacements > 0 {
			touched = true
			break
		}
	}
	return touched && bytes.Equal(patched, data), nil
}

func cleanupPatchHistoryForPath(store *config.PatchHistoryStore, path string) error {
	if store == nil || strings.TrimSpace(path) == "" {
		return nil
	}
	return store.Update(func(h *config.PatchHistory) error {
		for i := 0; i < len(h.Entries); i++ {
			if !config.PathsEqual(h.Entries[i].Path, path) {
				continue
			}
			h.Entries = append(h.Entries[:i], h.Entries[i+1:]...)
			i--
		}
		return nil
	})
}

func removePatchBackup(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func cleanupPatchHistory(outcome *patchOutcome) error {
	if outcome == nil || outcome.HistoryStore == nil || outcome.SpecsHash == "" {
		return nil
	}
	return outcome.HistoryStore.Update(func(h *config.PatchHistory) error {
		h.Remove(outcome.TargetPath, outcome.SpecsHash)
		return nil
	})
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func patchSpecsHash(specs []exePatchSpec) string {
	hasher := sha256.New()
	for _, spec := range specs {
		_, _ = io.WriteString(hasher, spec.label)
		_, _ = io.WriteString(hasher, "\n")
		if spec.apply != nil {
			_, _ = io.WriteString(hasher, "apply\n")
			_, _ = io.WriteString(hasher, spec.applyID)
			_, _ = io.WriteString(hasher, "\n")
		} else {
			_, _ = io.WriteString(hasher, "regex\n")
		}
		_, _ = io.WriteString(hasher, regexString(spec.match))
		_, _ = io.WriteString(hasher, "\n")
		_, _ = io.WriteString(hasher, regexString(spec.guard))
		_, _ = io.WriteString(hasher, "\n")
		_, _ = io.WriteString(hasher, regexString(spec.patch))
		_, _ = io.WriteString(hasher, "\n")
		if spec.fixedLength {
			_, _ = io.WriteString(hasher, "fixed\n")
		} else {
			_, _ = io.WriteString(hasher, "flex\n")
		}
		_, _ = hasher.Write(spec.replace)
		_, _ = io.WriteString(hasher, "\n")
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func regexString(re *regexp.Regexp) string {
	if re == nil {
		return ""
	}
	return re.String()
}

// applyExePatch performs a single pass over stage-1 matches in the original
// data to avoid re-patching loops when multiple matches exist.
func applyExePatch(data []byte, spec exePatchSpec, log io.Writer, preview bool) ([]byte, exePatchStats, error) {
	stats := exePatchStats{Label: spec.label}
	matches := spec.match.FindAllIndex(data, -1)
	if len(matches) == 0 {
		return nil, stats, fmt.Errorf("stage-1 regex produced no matches")
	}

	var out bytes.Buffer
	out.Grow(len(data))
	last := 0

	for _, match := range matches {
		start, end := match[0], match[1]
		if start == end {
			return nil, stats, fmt.Errorf("stage-1 regex matched an empty span")
		}

		stats.Segments++
		out.Write(data[last:start])

		segment := data[start:end]
		if spec.guard != nil && !spec.guard.Match(segment) {
			out.Write(segment)
			last = end
			continue
		}

		stats.Eligible++
		replLocs := spec.patch.FindAllIndex(segment, -1)
		if len(replLocs) == 0 {
			return nil, stats, fmt.Errorf("stage-3 regex did not match a stage-1 segment")
		}
		for _, loc := range replLocs {
			if loc[0] == loc[1] {
				return nil, stats, fmt.Errorf("stage-3 regex matched an empty span")
			}
		}

		stats.Patched++
		stats.Replacements += len(replLocs)

		patched := spec.patch.ReplaceAll(segment, spec.replace)
		if spec.fixedLength && len(patched) != len(segment) {
			return nil, stats, fmt.Errorf("stage-3 replacement changed length (segment=%d patched=%d)", len(segment), len(patched))
		}
		if preview {
			logPatchPreview(log, spec.label, segment, patched)
		}
		if !bytes.Equal(patched, segment) {
			stats.Changed++
		}

		out.Write(patched)
		last = end
	}

	out.Write(data[last:])
	return out.Bytes(), stats, nil
}

func applyExePatches(data []byte, specs []exePatchSpec, log io.Writer, preview bool) ([]byte, []exePatchStats, error) {
	if len(specs) == 0 {
		return data, nil, nil
	}

	out := data
	stats := make([]exePatchStats, 0, len(specs))
	for _, spec := range specs {
		if spec.apply != nil {
			updated, stat, err := spec.apply(out, log, preview)
			if err != nil {
				return nil, stats, err
			}
			out = updated
			stats = append(stats, stat)
			continue
		}
		updated, stat, err := applyExePatch(out, spec, log, preview)
		if err != nil {
			return nil, stats, err
		}
		out = updated
		stats = append(stats, stat)
	}

	return out, stats, nil
}

func normalizeReplacement(repl string) string {
	if repl == "" {
		return repl
	}

	var out strings.Builder
	out.Grow(len(repl))

	for i := 0; i < len(repl); {
		if repl[i] != '$' {
			out.WriteByte(repl[i])
			i++
			continue
		}
		if i+1 < len(repl) && repl[i+1] == '$' {
			out.WriteString("$$")
			i += 2
			continue
		}
		if i+1 < len(repl) && repl[i+1] == '{' {
			out.WriteByte(repl[i])
			i++
			continue
		}

		j := i + 1
		for j < len(repl) && repl[j] >= '0' && repl[j] <= '9' {
			j++
		}
		if j > i+1 && j < len(repl) && isIdentChar(repl[j]) {
			out.WriteString("${")
			out.WriteString(repl[i+1 : j])
			out.WriteString("}")
			i = j
			continue
		}

		out.WriteByte(repl[i])
		i++
	}

	return out.String()
}

func isIdentChar(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

const previewByteLimit = 240

func logDryRun(w io.Writer, path string, changed bool) {
	if w == nil {
		return
	}
	if changed {
		_, _ = fmt.Fprintf(w, "exe-patch: dry-run enabled; skipped write to %s\n", path)
		return
	}
	_, _ = fmt.Fprintf(w, "exe-patch: dry-run enabled; no changes for %s\n", path)
}

func truncHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func logPatchHistoryMiss(w io.Writer, path string, specsHash string, history config.PatchHistory) {
	if w == nil {
		return
	}
	for _, entry := range history.Entries {
		if strings.EqualFold(entry.Path, path) {
			_, _ = fmt.Fprintf(w, "exe-patch: path case mismatch: stored=%q current=%q\n", entry.Path, path)
			return
		}
	}
	_, _ = fmt.Fprintf(w, "exe-patch: no history entry for %s (specsHash=%s, entries=%d)\n", path, truncHash(specsHash), len(history.Entries))
}

func logAlreadyPatched(w io.Writer, path string) {
	if w == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "exe-patch: already patched; skipping %s\n", path)
}

func logPatchPreview(w io.Writer, label string, before, after []byte) {
	if w == nil {
		return
	}

	prefix := patchLogPrefix(label)
	_, _ = fmt.Fprintf(w, "%s: before=%s\n", prefix, formatPreviewSegment(before))
	_, _ = fmt.Fprintf(w, "%s: after=%s\n", prefix, formatPreviewSegment(after))
}

func formatPreviewSegment(segment []byte) string {
	if len(segment) <= previewByteLimit {
		return fmt.Sprintf("%q", segment)
	}
	head := segment[:previewByteLimit]
	return fmt.Sprintf("%q...(truncated %d bytes)", head, len(segment)-previewByteLimit)
}

func patchLogPrefix(label string) string {
	if label == "" {
		return "exe-patch"
	}
	return "exe-patch[" + label + "]"
}

func logPatchSummary(w io.Writer, path string, stats exePatchStats) {
	if w == nil {
		return
	}

	prefix := patchLogPrefix(stats.Label)
	if stats.Changed > 0 {
		_, _ = fmt.Fprintf(
			w,
			"%s: updated %d segment(s) in %s (matches=%d, eligible=%d, replacements=%d)\n",
			prefix,
			stats.Changed,
			path,
			stats.Segments,
			stats.Eligible,
			stats.Replacements,
		)
		return
	}

	_, _ = fmt.Fprintf(
		w,
		"%s: no byte changes for %s (matches=%d, eligible=%d)\n",
		prefix,
		path,
		stats.Segments,
		stats.Eligible,
	)
}
