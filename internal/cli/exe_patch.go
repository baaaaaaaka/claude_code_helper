package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

type exePatchOptions struct {
	enabledFlag    bool
	regex1         string
	regex2         []string
	regex3         []string
	replace        []string
	preview        bool
	policySettings bool
	dryRun         bool
}

type patchOutcome struct {
	Applied        bool
	TargetPath     string
	BackupPath     string
	SpecsHash      string
	HistoryStore   *config.PatchHistoryStore
	TargetSHA256   string
	TargetVersion  string
	IsClaude       bool
	AlreadyPatched bool
	ConfigPath     string
}

func (o exePatchOptions) enabled() bool {
	if !o.enabledFlag {
		return false
	}
	return o.policySettings || o.customRulesEnabled()
}

func (o exePatchOptions) customRulesEnabled() bool {
	return o.regex1 != "" || len(o.regex2) > 0 || len(o.regex3) > 0 || len(o.replace) > 0
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
	remoteSpec, err := remoteSettingsDisablePatchSpec()
	if err != nil {
		return nil, err
	}

	return []exePatchSpec{disableSpec, gateSpec, remoteSpec}, nil
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

func (o exePatchOptions) compile() ([]exePatchSpec, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}
	if !o.enabled() {
		return nil, nil
	}

	specs := make([]exePatchSpec, 0, len(o.regex2)+2)
	if o.policySettings {
		builtin, err := policySettingsSpecs()
		if err != nil {
			return nil, err
		}
		specs = append(specs, builtin...)
	}

	if !o.customRulesEnabled() {
		return specs, nil
	}

	re1, err := regexp.Compile(o.regex1)
	if err != nil {
		return nil, fmt.Errorf("compile --exe-patch-regex-1: %w", err)
	}

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
	specs, err := opts.compile()
	if err != nil {
		return nil, err
	}
	if len(specs) == 0 {
		return nil, nil
	}
	if len(cmdArgs) == 0 {
		return nil, fmt.Errorf("missing command")
	}
	if log == nil {
		log = io.Discard
	}

	exePath, err := exec.LookPath(cmdArgs[0])
	if err != nil {
		return nil, fmt.Errorf("resolve target executable %q: %w", cmdArgs[0], err)
	}

	resolvedPath, err := resolveExecutablePath(exePath)
	if err != nil {
		return nil, err
	}

	isClaude := isClaudeExecutable(cmdArgs[0], resolvedPath)
	proxyVersion := currentProxyVersion()
	targetVersion := ""
	targetSHA := ""
	if isClaude {
		targetVersion = resolveClaudeVersion(resolvedPath)
		if targetVersion == "" {
			if sha, err := hashFileSHA256(resolvedPath); err == nil {
				targetSHA = sha
			}
		}
		if skip, skipErr := shouldSkipPatchFailure(configPath, proxyVersion, targetVersion, targetSHA); skipErr == nil && skip {
			if targetVersion != "" {
				_, _ = fmt.Fprintf(log, "exe-patch: skip (previous failure) for claude %s with proxy %s\n", targetVersion, proxyVersion)
			} else {
				_, _ = fmt.Fprintf(log, "exe-patch: skip (previous failure) for claude binary with proxy %s\n", proxyVersion)
			}
			return &patchOutcome{
				TargetPath:    resolvedPath,
				TargetVersion: targetVersion,
				TargetSHA256:  targetSHA,
				IsClaude:      true,
				ConfigPath:    configPath,
			}, nil
		} else if skipErr != nil {
			_, _ = fmt.Fprintf(log, "exe-patch: failed to read patch failure config: %v\n", skipErr)
		}
	}

	historyStore, err := config.NewPatchHistoryStore(configPath)
	if err != nil {
		_, _ = fmt.Fprintf(log, "exe-patch: failed to init patch history: %v\n", err)
		historyStore = nil
	}

	outcome, err := patchExecutable(resolvedPath, specs, log, opts.preview, opts.dryRun, historyStore, proxyVersion)
	if err != nil {
		return nil, err
	}
	if outcome != nil {
		outcome.TargetVersion = targetVersion
		if outcome.TargetSHA256 == "" {
			outcome.TargetSHA256 = targetSHA
		}
		outcome.IsClaude = isClaude
		outcome.ConfigPath = configPath
		if outcome.Applied && outcome.IsClaude {
			if signErr := adhocCodesign(resolvedPath, log); signErr != nil {
				_, _ = fmt.Fprintln(log, "exe-patch: codesign failed; restoring backup")
				if restoreErr := restoreExecutableFromBackup(outcome); restoreErr != nil {
					return nil, fmt.Errorf("restore patched executable: %w", restoreErr)
				}
				if historyErr := cleanupPatchHistory(outcome); historyErr != nil {
					return nil, fmt.Errorf("cleanup patch history: %w", historyErr)
				}
				if recordErr := recordPatchFailure(configPath, outcome, formatFailureReason(signErr, "")); recordErr != nil {
					_, _ = fmt.Fprintf(log, "exe-patch: failed to record patch failure: %v\n", recordErr)
				}
				return nil, nil
			}
			out, probeErr := runClaudeProbe(resolvedPath, "--version")
			if probeErr != nil {
				_, _ = fmt.Fprintln(log, "exe-patch: detected startup failure; restoring backup")
				if restoreErr := restoreExecutableFromBackup(outcome); restoreErr != nil {
					return nil, fmt.Errorf("restore patched executable: %w", restoreErr)
				}
				if historyErr := cleanupPatchHistory(outcome); historyErr != nil {
					return nil, fmt.Errorf("cleanup patch history: %w", historyErr)
				}
				if recordErr := recordPatchFailure(configPath, outcome, formatFailureReason(probeErr, out)); recordErr != nil {
					_, _ = fmt.Fprintf(log, "exe-patch: failed to record patch failure: %v\n", recordErr)
				}
				return nil, nil
			}
		}
	}
	return outcome, nil
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

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read target executable %q: %w", path, err)
	}

	specsHash := patchSpecsHash(specs)
	outcome := &patchOutcome{
		TargetPath:   path,
		SpecsHash:    specsHash,
		HistoryStore: historyStore,
	}
	currentHash := hashBytes(data)
	outcome.TargetSHA256 = currentHash
	var history config.PatchHistory
	historyLoaded := false
	if historyStore != nil {
		loaded, err := historyStore.Load()
		if err != nil {
			_, _ = fmt.Fprintf(log, "exe-patch: failed to load patch history: %v\n", err)
		} else {
			history = loaded
			historyLoaded = true
		}
	}
	if historyLoaded {
		if history.IsPatched(path, specsHash, currentHash, proxyVersion) {
			outcome.AlreadyPatched = true
			logAlreadyPatched(log, path)
			return outcome, nil
		}
		if entry, ok := history.Find(path, specsHash); ok && entry.PatchedSHA256 == currentHash {
			prev := strings.TrimSpace(entry.ProxyVersion)
			if prev != "" && prev != strings.TrimSpace(proxyVersion) {
				_, _ = fmt.Fprintf(log, "exe-patch: proxy changed (%s -> %s); reapplying %s\n", prev, proxyVersion, path)
			}
		}
	}

	sourceData := data
	backupPath := originalBackupPath(path)
	if backupBytes, err := os.ReadFile(backupPath); err == nil {
		sourceData = backupBytes
		outcome.BackupPath = backupPath
	} else if err != nil && !os.IsNotExist(err) {
		_, _ = fmt.Fprintf(log, "exe-patch: failed to read backup %s: %v (using current binary)\n", backupPath, err)
	}

	patched, stats, err := applyExePatches(sourceData, specs, log, preview)
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
	if changed && bytes.Equal(patched, data) {
		changed = false
	}

	var patchedHash string
	if changed {
		patchedHash = hashBytes(patched)
	}

	backupReady := outcome.BackupPath != ""
	if touched && !dryRun && !backupReady {
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

	if dryRun {
		logDryRun(log, path, changed)
	}

	if historyStore != nil && touched && !dryRun {
		entryHash := currentHash
		if changed {
			entryHash = patchedHash
		}
		entry := config.PatchHistoryEntry{
			Path:          path,
			SpecsSHA256:   specsHash,
			PatchedSHA256: entryHash,
			ProxyVersion:  proxyVersion,
			PatchedAt:     time.Now(),
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
	if outcome == nil || outcome.TargetPath == "" || outcome.BackupPath == "" {
		return fmt.Errorf("missing backup data for restore")
	}
	info, err := os.Stat(outcome.BackupPath)
	if err != nil {
		return fmt.Errorf("stat backup file: %w", err)
	}
	data, err := os.ReadFile(outcome.BackupPath)
	if err != nil {
		return fmt.Errorf("read backup file: %w", err)
	}
	if err := os.WriteFile(outcome.TargetPath, data, info.Mode().Perm()); err != nil {
		return fmt.Errorf("restore executable from backup: %w", err)
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
