package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
	"github.com/baaaaaaaka/claude_code_helper/internal/diskspace"
)

var (
	runClaudeInstallerFn          = runClaudeInstaller
	probeInstalledClaudeVersionFn = probeInstalledClaudeVersion
)

func newUpgradeClaudeCmd(root *rootOptions) *cobra.Command {
	var profile string
	var claudeVersion string

	cmd := &cobra.Command{
		Use:   "upgrade-claude",
		Short: "Refresh Claude Code so claude-proxy has a usable launcher on this host",
		Long: strings.Join([]string{
			"Refresh Claude Code so claude-proxy has a usable launcher on this host.",
			"",
			"This command expects claude-proxy to have already created its config.",
			"Run 'claude-proxy init' first if this is the first time you are using claude-proxy.",
		}, "\n"),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpgradeClaude(cmd, root, profile, claudeVersion)
		},
	}

	cmd.Flags().StringVar(&profile, "profile", "", "SSH profile to use for proxy")
	cmd.Flags().StringVar(&claudeVersion, "version", "", "Claude Code version to install (for example 2.1.112; also accepts stable/latest)")

	return cmd
}

func runUpgradeClaude(cmd *cobra.Command, root *rootOptions, profileRef string, claudeVersion string) (err error) {
	store, err := config.NewStore(root.configPath)
	if err != nil {
		return err
	}

	if _, err := os.Stat(store.Path()); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("claude-proxy has not been initialized; run 'claude-proxy init' first")
		}
		return fmt.Errorf("cannot access config file: %w", err)
	}

	cfg, err := store.Load()
	if err != nil {
		return err
	}

	opts, err := upgradeClaudeInstallOpts(cfg, profileRef)
	if err != nil {
		return err
	}
	targetVersion, err := normalizeClaudeInstallTarget(claudeVersion)
	if err != nil {
		return err
	}
	opts.TargetVersion = targetVersion

	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	var installLog bytes.Buffer
	installOut := io.Writer(&installLog)
	if out != nil {
		installOut = io.MultiWriter(out, &installLog)
	}

	beforeInstall := installedClaudeBinaryState{}
	if path, ok := findInstalledClaudePath(claudeInstallGOOS, "", os.Getenv); ok {
		beforeInstall = snapshotInstalledClaudeBinaryBestEffort(path)
	}

	// `upgrade-claude` is the explicit "refresh Claude" command, so it
	// intentionally reruns the official installer before considering reuse of
	// any existing managed launcher. This differs from runtime launch paths such
	// as ensureClaudeInstalled, which are allowed to fast-path to any launcher
	// that is already usable on the current host.
	if err := runClaudeInstallerFn(ctx, installOut, opts); err != nil {
		return err
	}

	versionCleanup := newClaudeVersionCleanupStash(targetVersion)
	defer func() {
		if err == nil {
			return
		}
		if restoreErr := versionCleanup.Restore(); restoreErr != nil {
			err = fmt.Errorf("%w; also failed to restore stashed Claude versions: %v", err, restoreErr)
		}
	}()
	if err := versionCleanup.Stash(installOut, claudeInstallGOOS, os.Getenv); err != nil {
		return err
	}

	claudePath := "claude"
	if path, ok := findInstalledClaudePath(claudeInstallGOOS, installLog.String(), os.Getenv); ok {
		claudePath = path
	} else if targetVersion != "" {
		return fmt.Errorf("Claude installer finished for %s but managed Claude launcher was not found", targetVersion)
	}
	claudePath, err = maybeRepairInstalledClaude(ctx, installOut, &installLog, opts, root.exePatch, beforeInstall, claudePath, versionCleanup)
	if err != nil {
		return err
	}
	claudePath, err = ensureInstalledClaudeUsableAfterUpgrade(ctx, installOut, root.exePatch, claudePath)
	if err != nil {
		return err
	}

	return finalizeUpgradedClaudeLauncher(ctx, out, root, claudePath, true, func() error {
		return versionCleanup.Commit(out)
	})
}

func finalizeUpgradedClaudeLauncher(ctx context.Context, out io.Writer, root *rootOptions, claudePath string, invalidatePatchState bool, beforeComplete func() error) error {
	// Remove stale exe-patch backup and history so the patch system treats
	// the freshly-installed binary as new rather than re-patching the old
	// backup over the top.
	if invalidatePatchState {
		invalidateExePatchStatePath(claudePath, root.configPath)
	}

	if root.exePatch.enabled() {
		patchOutcome, patchErr := maybePatchExecutableCtxFn(ctx, []string{claudePath}, root.exePatch, root.configPath, out)
		if patchErr != nil {
			return patchErr
		}
		if waitErr := waitPatchedExecutableReadyFn(ctx, patchOutcome); waitErr != nil {
			return waitErr
		}
	}

	if beforeComplete != nil {
		if err := beforeComplete(); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintln(out, "Claude Code launcher refresh complete.")
	return nil
}

// invalidateExePatchState removes the exe-patch backup file and patch history
// entry for the given command so the next run re-patches from the new binary.
func invalidateExePatchState(cmdName string, configPath string) {
	exePath, err := exec.LookPath(cmdName)
	if err != nil {
		return
	}
	invalidateExePatchStatePath(exePath, configPath)
}

func invalidateExePatchStatePath(path string, configPath string) {
	resolved, err := resolveExecutablePath(path)
	if err != nil {
		return
	}

	backup := originalBackupPath(resolved)
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		return
	}

	historyStore, err := config.NewPatchHistoryStore(configPath)
	if err != nil {
		return
	}
	_ = historyStore.Update(func(h *config.PatchHistory) error {
		for i := 0; i < len(h.Entries); i++ {
			if config.PathsEqual(h.Entries[i].Path, resolved) {
				h.Entries = append(h.Entries[:i], h.Entries[i+1:]...)
				i--
			}
		}
		return nil
	})
}

type installedClaudeBinaryState struct {
	Path               string
	ResolvedPath       string
	SHA256             string
	LauncherWasSymlink bool
	LauncherLinkTarget string
}

func snapshotInstalledClaudeBinaryBestEffort(path string) installedClaudeBinaryState {
	state, err := snapshotInstalledClaudeBinary(path)
	if err != nil {
		return installedClaudeBinaryState{}
	}
	return state
}

func snapshotInstalledClaudeBinary(path string) (installedClaudeBinaryState, error) {
	state := installedClaudeBinaryState{Path: strings.TrimSpace(path)}
	if state.Path == "" {
		return state, nil
	}
	info, err := os.Lstat(state.Path)
	if err != nil {
		return installedClaudeBinaryState{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		state.LauncherWasSymlink = true
		linkTarget, err := os.Readlink(state.Path)
		if err != nil {
			return installedClaudeBinaryState{}, err
		}
		state.LauncherLinkTarget = linkTarget
	}
	resolved, err := resolveExecutablePathFn(state.Path)
	if err != nil {
		return installedClaudeBinaryState{}, err
	}
	sha, err := hashFileSHA256Fn(resolved)
	if err != nil {
		return installedClaudeBinaryState{}, err
	}
	state.ResolvedPath = resolved
	state.SHA256 = strings.TrimSpace(sha)
	return state, nil
}

func maybeRepairInstalledClaude(ctx context.Context, installOut io.Writer, installLog *bytes.Buffer, installOpts installProxyOptions, patchOpts exePatchOptions, before installedClaudeBinaryState, claudePath string, versionCleanup *claudeVersionCleanupStash) (string, error) {
	after := snapshotInstalledClaudeBinaryBestEffort(claudePath)
	shouldRepair, reason, err := shouldRepairInstalledClaude(ctx, before, after, patchOpts)
	if err != nil {
		return "", err
	}
	if !shouldRepair {
		return claudePath, nil
	}

	if installOut != nil {
		_, _ = fmt.Fprintf(installOut, "Claude installer left the existing version file unchanged (%s); moving %s aside and retrying once.\n", reason, after.ResolvedPath)
	}
	stashedPath, err := stashInstalledClaudeVersionFile(after.ResolvedPath)
	if err != nil {
		return "", fmt.Errorf("stash stale Claude version file %q: %w", after.ResolvedPath, err)
	}

	restoreOriginal := func(failure error) error {
		restoreErr := restoreStashedClaudeInstall(before, stashedPath)
		if restoreErr == nil {
			return fmt.Errorf("%w; restored previous Claude version file", failure)
		}
		return fmt.Errorf("%w; also failed to restore previous Claude version file: %v", failure, restoreErr)
	}

	var retryLog bytes.Buffer
	retryOut := io.Writer(&retryLog)
	if installOut != nil {
		retryOut = io.MultiWriter(installOut, &retryLog)
	}
	if err := runClaudeInstallerFn(ctx, retryOut, installOpts); err != nil {
		return "", restoreOriginal(fmt.Errorf("Claude installer retry failed: %w", err))
	}
	appendInstallOutput(installLog, retryLog.String())
	if err := versionCleanup.Stash(installOut, claudeInstallGOOS, os.Getenv); err != nil {
		return "", restoreOriginal(err)
	}

	retriedClaudePath := claudePath
	if path, ok := findInstalledClaudePath(claudeInstallGOOS, retryLog.String(), os.Getenv); ok {
		retriedClaudePath = path
	} else if path, ok := findInstalledClaudePath(claudeInstallGOOS, installLog.String(), os.Getenv); ok {
		retriedClaudePath = path
	}

	retriedInstall, err := snapshotInstalledClaudeBinary(retriedClaudePath)
	if err != nil {
		return "", restoreOriginal(fmt.Errorf("inspect Claude install after retry: %w", err))
	}
	problem, bad, err := installedClaudeBinaryProblem(ctx, retriedInstall, patchOpts)
	if err != nil {
		return "", restoreOriginal(err)
	}
	if bad {
		if reusablePath, reusable := findUsableManagedClaudePath(ctx, installOut, claudeInstallGOOS, installLog.String(), os.Getenv, patchOpts); reusable && !config.PathsEqual(reusablePath, retriedClaudePath) {
			if installOut != nil {
				_, _ = fmt.Fprintf(installOut, "Claude installer retry still produced an unusable version file (%s); reusing the existing managed launcher at %s on this old-kernel host.\n", problem, reusablePath)
			}
			removeStashedClaudeVersionFile(stashedPath)
			return reusablePath, nil
		}
		return "", restoreOriginal(fmt.Errorf("Claude installer retry still produced an unusable version file (%s)", problem))
	}
	removeStashedClaudeVersionFile(stashedPath)

	return retriedClaudePath, nil
}

func ensureInstalledClaudeUsableAfterUpgrade(ctx context.Context, installOut io.Writer, patchOpts exePatchOptions, claudePath string) (string, error) {
	if !shouldValidateManagedClaudePath(claudeInstallGOOS) {
		return claudePath, nil
	}
	if _, err := probeManagedClaudeLauncher(ctx, claudePath, patchOpts); err == nil {
		return claudePath, nil
	} else {
		if reusablePath, reusable := findUsableManagedClaudePath(ctx, installOut, claudeInstallGOOS, "", os.Getenv, patchOpts); reusable && !config.PathsEqual(reusablePath, claudePath) {
			if installOut != nil {
				_, _ = fmt.Fprintf(installOut, "Claude installer finished but the installed launcher at %s is still unusable on this old-kernel host; reusing the existing managed launcher at %s instead. Probe error: %v\n", claudePath, reusablePath, err)
			}
			return reusablePath, nil
		}
		return "", fmt.Errorf("Claude installer finished but the installed launcher at %s is still unusable on this old-kernel host: %w", claudePath, err)
	}
}

func shouldRepairInstalledClaude(ctx context.Context, before installedClaudeBinaryState, after installedClaudeBinaryState, patchOpts exePatchOptions) (bool, string, error) {
	if !sameInstalledClaudeBinary(before, after) {
		return false, "", nil
	}
	reason, bad, err := installedClaudeBinaryProblem(ctx, after, patchOpts)
	if err != nil {
		return false, "", err
	}
	return bad, reason, nil
}

func installedClaudeBinaryProblem(ctx context.Context, state installedClaudeBinaryState, patchOpts exePatchOptions) (string, bool, error) {
	if strings.TrimSpace(state.ResolvedPath) == "" || !isClaudeVersionStorePath(state.ResolvedPath) {
		return "", false, nil
	}

	native, reason, err := claudeBinaryHasExpectedMagic(state.ResolvedPath)
	if err != nil {
		return "", false, err
	}
	if !native {
		return reason, true, nil
	}

	if !patchOpts.enabled() || !patchOpts.policySettings {
		return "", false, nil
	}

	patchable, err := claudeBinarySupportsBuiltInPatch(state.ResolvedPath)
	if err != nil {
		return "", false, err
	}
	if patchable {
		return "", false, nil
	}
	if canRepairWithGlibcCompat(patchOpts) {
		return "", false, nil
	}

	versionOK, err := probeInstalledClaudeVersionFn(ctx, state.ResolvedPath)
	if err != nil {
		return "", false, err
	}
	if versionOK {
		return "", false, nil
	}
	return "built-in Claude patch rules found no matches and --version probe failed", true, nil
}

func sameInstalledClaudeBinary(a installedClaudeBinaryState, b installedClaudeBinaryState) bool {
	if strings.TrimSpace(a.ResolvedPath) == "" || strings.TrimSpace(b.ResolvedPath) == "" {
		return false
	}
	if strings.TrimSpace(a.SHA256) == "" || strings.TrimSpace(b.SHA256) == "" {
		return false
	}
	return config.PathsEqual(a.ResolvedPath, b.ResolvedPath) && a.SHA256 == b.SHA256
}

func isClaudeVersionStorePath(path string) bool {
	normalized := strings.ToLower(filepath.ToSlash(filepath.Clean(strings.TrimSpace(path))))
	if normalized == "" {
		return false
	}
	return strings.Contains(normalized, "/claude/versions/")
}

func claudeBinaryHasExpectedMagic(path string) (bool, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, "", fmt.Errorf("open installed Claude binary %q: %w", path, err)
	}
	defer f.Close()

	header := make([]byte, 4)
	n, err := io.ReadFull(f, header)
	if err != nil && err != io.ErrUnexpectedEOF {
		return false, "", fmt.Errorf("read installed Claude binary %q: %w", path, err)
	}
	header = header[:n]

	switch strings.ToLower(strings.TrimSpace(claudeInstallGOOS)) {
	case "windows":
		if len(header) >= 2 && header[0] == 'M' && header[1] == 'Z' {
			return true, "", nil
		}
		return false, "installed file is not a PE executable", nil
	case "darwin":
		if isMachOMagic(header) {
			return true, "", nil
		}
		return false, "installed file is not a Mach-O executable", nil
	default:
		if len(header) >= 4 && header[0] == 0x7f && header[1] == 'E' && header[2] == 'L' && header[3] == 'F' {
			return true, "", nil
		}
		return false, "installed file is not an ELF executable", nil
	}
}

func isMachOMagic(header []byte) bool {
	if len(header) < 4 {
		return false
	}
	magic := [4]byte{header[0], header[1], header[2], header[3]}
	switch magic {
	case [4]byte{0xfe, 0xed, 0xfa, 0xce},
		[4]byte{0xce, 0xfa, 0xed, 0xfe},
		[4]byte{0xfe, 0xed, 0xfa, 0xcf},
		[4]byte{0xcf, 0xfa, 0xed, 0xfe},
		[4]byte{0xca, 0xfe, 0xba, 0xbe},
		[4]byte{0xbe, 0xba, 0xfe, 0xca},
		[4]byte{0xca, 0xfe, 0xba, 0xbf},
		[4]byte{0xbf, 0xba, 0xfe, 0xca}:
		return true
	default:
		return false
	}
}

func claudeBinarySupportsBuiltInPatch(path string) (bool, error) {
	specs, err := policySettingsSpecs()
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read installed Claude binary %q for patch inspection: %w", path, err)
	}
	_, stats, err := applyExePatches(data, specs, io.Discard, false)
	if err != nil {
		return false, fmt.Errorf("inspect built-in Claude patchability for %q: %w", path, err)
	}
	for _, stat := range stats {
		if stat.Eligible > 0 || stat.Replacements > 0 {
			return true, nil
		}
	}
	return false, nil
}

func probeInstalledClaudeVersion(ctx context.Context, path string) (bool, error) {
	out, err := runClaudeProbeWithContext(ctx, path, "--version", 5*time.Second)
	if version := extractVersion(out); version != "" && err == nil {
		return true, nil
	}
	if err == nil {
		return strings.TrimSpace(out) != "", nil
	}
	return false, nil
}

func canRepairWithGlibcCompat(opts exePatchOptions) bool {
	return opts.glibcCompatConfigured() &&
		strings.EqualFold(claudeInstallGOOS, "linux") &&
		glibcCompatHostEligibleFn()
}

func stashInstalledClaudeVersionFile(path string) (string, error) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	f, err := os.CreateTemp(dir, base+".claude-proxy-reinstall-*")
	if err != nil {
		return "", diskspace.AnnotateWriteError(dir, err)
	}
	stashedPath := f.Name()
	if closeErr := f.Close(); closeErr != nil {
		_ = os.Remove(stashedPath)
		return "", closeErr
	}
	if err := os.Remove(stashedPath); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := os.Rename(path, stashedPath); err != nil {
		return "", diskspace.AnnotateWriteError(stashedPath, err)
	}
	return stashedPath, nil
}

func restoreStashedClaudeVersionFile(path string, stashedPath string) error {
	if strings.TrimSpace(stashedPath) == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(stashedPath, path); err != nil {
		return diskspace.AnnotateWriteError(path, err)
	}
	return nil
}

func restoreStashedClaudeInstall(previous installedClaudeBinaryState, stashedPath string) error {
	if err := restoreStashedClaudeVersionFile(previous.ResolvedPath, stashedPath); err != nil {
		return err
	}
	return restoreInstalledClaudeLauncher(previous)
}

func restoreInstalledClaudeLauncher(previous installedClaudeBinaryState) error {
	if strings.TrimSpace(previous.Path) == "" || strings.TrimSpace(previous.ResolvedPath) == "" {
		return nil
	}
	if !previous.LauncherWasSymlink || config.PathsEqual(previous.Path, previous.ResolvedPath) {
		return nil
	}

	target := strings.TrimSpace(previous.LauncherLinkTarget)
	if target == "" {
		target = previous.ResolvedPath
	}

	if err := os.MkdirAll(filepath.Dir(previous.Path), 0o755); err != nil {
		return err
	}
	if err := os.Remove(previous.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Symlink(target, previous.Path); err != nil {
		return diskspace.AnnotateWriteError(previous.Path, err)
	}
	return nil
}

func removeStashedClaudeVersionFile(stashedPath string) {
	if strings.TrimSpace(stashedPath) == "" {
		return
	}
	_ = os.Remove(stashedPath)
}

func normalizeClaudeInstallTarget(raw string) (string, error) {
	target := strings.TrimSpace(raw)
	if target == "" {
		return "", nil
	}
	lower := strings.ToLower(target)
	if lower == "latest" || lower == "stable" {
		return lower, nil
	}
	if len(target) > 1 && (target[0] == 'v' || target[0] == 'V') && target[1] >= '0' && target[1] <= '9' {
		target = target[1:]
	}
	if !validClaudeInstallVersion(target) {
		return "", fmt.Errorf("invalid Claude Code version %q; expected stable, latest, or X.Y.Z", raw)
	}
	return target, nil
}

func validClaudeInstallVersion(version string) bool {
	core := version
	suffix := ""
	if before, after, ok := strings.Cut(version, "-"); ok {
		core = before
		suffix = after
		if suffix == "" {
			return false
		}
		for _, ch := range suffix {
			if !isClaudeVersionSuffixChar(ch) {
				return false
			}
		}
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return false
			}
		}
	}
	return true
}

func isClaudeVersionSuffixChar(ch rune) bool {
	return (ch >= '0' && ch <= '9') ||
		(ch >= 'A' && ch <= 'Z') ||
		(ch >= 'a' && ch <= 'z') ||
		ch == '.' ||
		ch == '-' ||
		ch == '+'
}

type claudeVersionCleanupStash struct {
	target      string
	targetTuple []int
	enabled     bool
	entries     []claudeVersionCleanupStashEntry
}

type claudeVersionCleanupStashEntry struct {
	OriginalPath string
	StashedPath  string
	Restore      bool
	Committed    bool
}

func newClaudeVersionCleanupStash(target string) *claudeVersionCleanupStash {
	targetTuple, ok := claudeVersionTuple(target)
	return &claudeVersionCleanupStash{
		target:      strings.TrimSpace(target),
		targetTuple: targetTuple,
		enabled:     ok,
	}
}

func (s *claudeVersionCleanupStash) Stash(out io.Writer, goos string, getenv func(string) string) error {
	if s == nil || !s.enabled {
		return nil
	}

	for _, dir := range defaultClaudeVersionStoreDirs(goos, getenv) {
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect default Claude version dir %s: %w", dir, err)
		}
		for _, entry := range entries {
			entryTuple, ok := claudeVersionTuple(entry.Name())
			if !ok || compareClaudeVersionTuple(entryTuple, s.targetTuple) <= 0 {
				continue
			}
			originalPath := filepath.Join(dir, entry.Name())
			stashedPath, err := uniqueClaudeVersionStashPath(dir, entry.Name())
			if err != nil {
				return err
			}
			if err := os.Rename(originalPath, stashedPath); err != nil {
				return fmt.Errorf("stash default Claude version %s: %w", originalPath, diskspace.AnnotateWriteError(stashedPath, err))
			}
			s.entries = append(s.entries, claudeVersionCleanupStashEntry{
				OriginalPath: originalPath,
				StashedPath:  stashedPath,
				Restore:      !s.hasRestoreEntry(originalPath),
			})
		}
	}
	return nil
}

func (s *claudeVersionCleanupStash) Commit(out io.Writer) error {
	if s == nil || len(s.entries) == 0 {
		return nil
	}

	removed := 0
	for i := range s.entries {
		entry := &s.entries[i]
		if entry.Committed || strings.TrimSpace(entry.StashedPath) == "" {
			continue
		}
		if err := os.RemoveAll(entry.StashedPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove stashed Claude version %s: %w", entry.StashedPath, err)
		}
		entry.Committed = true
		removed++
	}
	if removed > 0 && out != nil {
		_, _ = fmt.Fprintf(out, "Removed %d Claude Code version(s) newer than %s from the default install path.\n", removed, s.target)
	}
	return nil
}

func (s *claudeVersionCleanupStash) Restore() error {
	if s == nil || len(s.entries) == 0 {
		return nil
	}

	errs := []error{}
	for i := len(s.entries) - 1; i >= 0; i-- {
		entry := &s.entries[i]
		if entry.Committed || strings.TrimSpace(entry.StashedPath) == "" {
			continue
		}
		if _, err := os.Lstat(entry.StashedPath); err != nil {
			if os.IsNotExist(err) && !entry.Restore {
				continue
			}
			errs = append(errs, fmt.Errorf("inspect stashed Claude version %s: %w", entry.StashedPath, err))
			continue
		}
		if !entry.Restore {
			if err := os.RemoveAll(entry.StashedPath); err != nil && !os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("remove duplicate stashed Claude version %s: %w", entry.StashedPath, err))
			}
			entry.Committed = true
			continue
		}
		if _, err := os.Lstat(entry.OriginalPath); err == nil {
			errs = append(errs, fmt.Errorf("cannot restore stashed Claude version %s: original path already exists", entry.OriginalPath))
			continue
		} else if !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("inspect Claude version restore path %s: %w", entry.OriginalPath, err))
			continue
		}
		if err := os.Rename(entry.StashedPath, entry.OriginalPath); err != nil {
			errs = append(errs, fmt.Errorf("restore stashed Claude version %s: %w", entry.OriginalPath, diskspace.AnnotateWriteError(entry.OriginalPath, err)))
			continue
		}
		entry.Committed = true
	}
	return errors.Join(errs...)
}

func (s *claudeVersionCleanupStash) hasRestoreEntry(path string) bool {
	for _, entry := range s.entries {
		if entry.Restore && config.PathsEqual(entry.OriginalPath, path) {
			return true
		}
	}
	return false
}

func uniqueClaudeVersionStashPath(dir string, name string) (string, error) {
	for i := 0; i < 100; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf(".claude-proxy-stash-%d-%d-%d-%s", os.Getpid(), time.Now().UnixNano(), i, name))
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", fmt.Errorf("inspect Claude version stash path %s: %w", candidate, err)
		}
	}
	return "", fmt.Errorf("cannot choose an unused Claude version stash path in %s", dir)
}

func defaultClaudeVersionStoreDirs(goos string, getenv func(string) string) []string {
	dirs := []string{}
	for _, home := range installHomeCandidates(goos, getenv) {
		dirs = appendInstallCandidate(dirs, goos, filepath.Join(home, ".local", "share", "claude", "versions"))
	}
	return dirs
}

func claudeVersionTuple(version string) ([]int, bool) {
	version = strings.TrimSpace(version)
	if version == "" {
		return nil, false
	}
	if len(version) > 1 && (version[0] == 'v' || version[0] == 'V') {
		version = version[1:]
	}
	if before, _, ok := strings.Cut(version, "-"); ok {
		version = before
	}
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return nil, false
	}
	tuple := make([]int, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return nil, false
		}
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return nil, false
			}
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, false
		}
		tuple = append(tuple, n)
	}
	return tuple, true
}

func compareClaudeVersionTuple(a []int, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] > b[i] {
			return 1
		}
		if a[i] < b[i] {
			return -1
		}
	}
	if len(a) > len(b) {
		return 1
	}
	if len(a) < len(b) {
		return -1
	}
	return 0
}

func upgradeClaudeInstallOpts(cfg config.Config, profileRef string) (installProxyOptions, error) {
	if cfg.ProxyEnabled != nil && !*cfg.ProxyEnabled {
		return installProxyOptions{UseProxy: false}, nil
	}

	useProxy := false
	if cfg.ProxyEnabled != nil && *cfg.ProxyEnabled {
		useProxy = true
	} else if cfg.ProxyEnabled == nil && len(cfg.Profiles) > 0 {
		useProxy = true
	}

	if !useProxy {
		return installProxyOptions{UseProxy: false}, nil
	}

	profile, err := selectProfile(cfg, profileRef)
	if err != nil {
		return installProxyOptions{}, err
	}

	return installProxyOptions{
		UseProxy:  true,
		Profile:   &profile,
		Instances: cfg.Instances,
	}, nil
}
