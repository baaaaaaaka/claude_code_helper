package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

var (
	runClaudeInstallerFn          = runClaudeInstaller
	probeInstalledClaudeVersionFn = probeInstalledClaudeVersion
)

func newUpgradeClaudeCmd(root *rootOptions) *cobra.Command {
	var profile string

	cmd := &cobra.Command{
		Use:   "upgrade-claude",
		Short: "Upgrade (or install) Claude Code using the official installer",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpgradeClaude(cmd, root, profile)
		},
	}

	cmd.Flags().StringVar(&profile, "profile", "", "SSH profile to use for proxy")

	return cmd
}

func runUpgradeClaude(cmd *cobra.Command, root *rootOptions, profileRef string) error {
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

	if err := runClaudeInstallerFn(ctx, installOut, opts); err != nil {
		return err
	}

	claudePath := "claude"
	if path, ok := findInstalledClaudePath(claudeInstallGOOS, installLog.String(), os.Getenv); ok {
		claudePath = path
	}
	claudePath, err = maybeRepairInstalledClaude(ctx, installOut, &installLog, opts, root.exePatch, beforeInstall, claudePath)
	if err != nil {
		return err
	}

	// Remove stale exe-patch backup and history so the patch system treats
	// the freshly-installed binary as new rather than re-patching the old
	// backup over the top.
	invalidateExePatchStatePath(claudePath, root.configPath)

	if root.exePatch.enabled() {
		patchOutcome, patchErr := maybePatchExecutableCtxFn(ctx, []string{claudePath}, root.exePatch, root.configPath, out)
		if patchErr != nil {
			return patchErr
		}
		if waitErr := waitPatchedExecutableReadyFn(ctx, patchOutcome); waitErr != nil {
			return waitErr
		}
	}

	_, _ = fmt.Fprintln(out, "Claude Code upgrade complete.")
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

func maybeRepairInstalledClaude(ctx context.Context, installOut io.Writer, installLog *bytes.Buffer, installOpts installProxyOptions, patchOpts exePatchOptions, before installedClaudeBinaryState, claudePath string) (string, error) {
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
		return "", restoreOriginal(fmt.Errorf("Claude installer retry still produced an unusable version file (%s)", problem))
	}
	removeStashedClaudeVersionFile(stashedPath)

	return retriedClaudePath, nil
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
	if version := extractVersion(out); version != "" {
		return true, nil
	}
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(out) != "", nil
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
		return "", err
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
		return "", err
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
	return os.Rename(stashedPath, path)
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
	return os.Symlink(target, previous.Path)
}

func removeStashedClaudeVersionFile(stashedPath string) {
	if strings.TrimSpace(stashedPath) == "" {
		return
	}
	_ = os.Remove(stashedPath)
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
