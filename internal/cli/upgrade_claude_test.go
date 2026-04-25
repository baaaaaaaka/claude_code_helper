package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

func TestUpgradeClaudeInstallOptsNoProxy(t *testing.T) {
	disabled := false
	cfg := config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &disabled,
	}
	opts, err := upgradeClaudeInstallOpts(cfg, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.UseProxy {
		t.Fatalf("expected UseProxy=false when ProxyEnabled=false")
	}
}

func TestUpgradeClaudeInstallOptsWithProxy(t *testing.T) {
	enabled := true
	profile := config.Profile{ID: "p1", Name: "p1"}
	instances := []config.Instance{{ID: "inst-1", ProfileID: "p1"}}
	cfg := config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &enabled,
		Profiles:     []config.Profile{profile},
		Instances:    instances,
	}
	opts, err := upgradeClaudeInstallOpts(cfg, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !opts.UseProxy {
		t.Fatalf("expected UseProxy=true")
	}
	if opts.Profile == nil || opts.Profile.ID != "p1" {
		t.Fatalf("expected profile p1, got %v", opts.Profile)
	}
	if len(opts.Instances) != 1 || opts.Instances[0].ID != "inst-1" {
		t.Fatalf("expected instances to be passed through")
	}
}

func TestUpgradeClaudeInstallOptsImpliedProxy(t *testing.T) {
	cfg := config.Config{
		Version:  config.CurrentVersion,
		Profiles: []config.Profile{{ID: "p1", Name: "p1"}},
	}
	opts, err := upgradeClaudeInstallOpts(cfg, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !opts.UseProxy {
		t.Fatalf("expected UseProxy=true when ProxyEnabled=nil and profiles exist")
	}
	if opts.Profile == nil || opts.Profile.ID != "p1" {
		t.Fatalf("expected profile p1")
	}
}

func TestUpgradeClaudeInstallOptsEmptyConfig(t *testing.T) {
	cfg := config.Config{Version: config.CurrentVersion}
	opts, err := upgradeClaudeInstallOpts(cfg, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.UseProxy {
		t.Fatalf("expected UseProxy=false when no profiles and no proxy preference")
	}
}

func TestUpgradeClaudeInstallOptsMultipleProfilesNoRef(t *testing.T) {
	enabled := true
	cfg := config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &enabled,
		Profiles: []config.Profile{
			{ID: "p1", Name: "p1"},
			{ID: "p2", Name: "p2"},
		},
	}
	_, err := upgradeClaudeInstallOpts(cfg, "")
	if err == nil {
		t.Fatalf("expected error when multiple profiles and no --profile")
	}
	if !strings.Contains(err.Error(), "multiple profiles") {
		t.Fatalf("expected 'multiple profiles' error, got: %v", err)
	}
}

func TestUpgradeClaudeInstallOptsMultipleProfilesWithRef(t *testing.T) {
	enabled := true
	cfg := config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &enabled,
		Profiles: []config.Profile{
			{ID: "p1", Name: "alpha"},
			{ID: "p2", Name: "beta"},
		},
	}
	opts, err := upgradeClaudeInstallOpts(cfg, "beta")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Profile == nil || opts.Profile.ID != "p2" {
		t.Fatalf("expected profile p2, got %v", opts.Profile)
	}
}

func TestUpgradeClaudeInstallOptsProfileNotFound(t *testing.T) {
	enabled := true
	cfg := config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &enabled,
		Profiles:     []config.Profile{{ID: "p1", Name: "p1"}},
	}
	_, err := upgradeClaudeInstallOpts(cfg, "unknown")
	if err == nil {
		t.Fatalf("expected error for unknown profile")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' error, got: %v", err)
	}
}

func TestRunUpgradeClaudeUninitializedConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "nonexistent", "config.json")

	root := &rootOptions{configPath: configPath}
	cmd := newUpgradeClaudeCmd(root)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error for uninitialized config")
	}
	if !strings.Contains(err.Error(), "not been initialized") {
		t.Fatalf("expected 'not been initialized' error, got: %v", err)
	}
}

func TestRunUpgradeClaudeNoProxyDirect(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}

	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("https_proxy", "")

	store := newTempStore(t)
	disabled := false
	if err := store.Save(config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &disabled,
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	envFile := filepath.Join(dir, "env.txt")
	script := filepath.Join(dir, "bash")
	scriptBody := fmt.Sprintf("#!/bin/sh\nprintf ok > %q\nprintf '%%s\\n%%s\\n' \"$HTTP_PROXY\" \"$HTTPS_PROXY\" > %q\nexit 0\n", marker, envFile)
	if err := os.WriteFile(script, []byte(scriptBody), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	root := &rootOptions{configPath: store.Path()}
	cmd := newUpgradeClaudeCmd(root)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("upgrade-claude error: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("expected installer to run: %v", err)
	}

	content, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			t.Fatalf("expected empty HTTP_PROXY/HTTPS_PROXY, got %q", string(content))
		}
	}
}

func TestRunUpgradeClaudePrewarmsPatchedClaude(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	prevLookPath := claudeInstallLookPathFn
	claudeInstallLookPathFn = func(file string) (string, error) {
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	}
	t.Cleanup(func() {
		claudeInstallLookPathFn = prevLookPath
	})

	store := newTempStore(t)
	disabled := false
	if err := store.Save(config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &disabled,
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	prepareCalled := false
	waitCalled := false
	prevInstaller := runClaudeInstallerFn
	runClaudeInstallerFn = func(ctx context.Context, out io.Writer, opts installProxyOptions) error {
		return nil
	}
	t.Cleanup(func() {
		runClaudeInstallerFn = prevInstaller
	})
	maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
		prepareCalled = true
		if len(cmdArgs) != 1 || cmdArgs[0] != "claude" {
			t.Fatalf("unexpected patch prep args: %v", cmdArgs)
		}
		return &patchOutcome{}, nil
	}
	waitPatchedExecutableReadyFn = func(ctx context.Context, outcome *patchOutcome) error {
		waitCalled = true
		return nil
	}

	root := &rootOptions{
		configPath: store.Path(),
		exePatch: exePatchOptions{
			enabledFlag:    true,
			policySettings: true,
		},
	}
	cmd := newUpgradeClaudeCmd(root)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("upgrade-claude error: %v", err)
	}
	if !prepareCalled {
		t.Fatalf("expected upgrade-claude to prewarm patched claude")
	}
	if !waitCalled {
		t.Fatalf("expected upgrade-claude to wait for readiness")
	}
}

func TestRunUpgradeClaudePrewarmsInstalledLocationFromInstallerOutput(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	prevLookPath := claudeInstallLookPathFn
	claudeInstallLookPathFn = func(file string) (string, error) {
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	}
	t.Cleanup(func() {
		claudeInstallLookPathFn = prevLookPath
	})

	store := newTempStore(t)
	disabled := false
	if err := store.Save(config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &disabled,
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	claudePath := filepath.Join(t.TempDir(), "recovered", "claude")
	if err := os.MkdirAll(filepath.Dir(claudePath), 0o755); err != nil {
		t.Fatalf("mkdir claude dir: %v", err)
	}
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write claude: %v", err)
	}

	prepareCalled := false
	waitCalled := false
	prevInstaller := runClaudeInstallerFn
	runClaudeInstallerFn = func(ctx context.Context, out io.Writer, opts installProxyOptions) error {
		_, _ = fmt.Fprintf(out, "Location: %s\n", claudePath)
		return nil
	}
	t.Cleanup(func() {
		runClaudeInstallerFn = prevInstaller
	})
	maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
		prepareCalled = true
		if len(cmdArgs) != 1 || !config.PathsEqual(cmdArgs[0], claudePath) {
			t.Fatalf("unexpected patch prep args: %v", cmdArgs)
		}
		return &patchOutcome{}, nil
	}
	waitPatchedExecutableReadyFn = func(ctx context.Context, outcome *patchOutcome) error {
		waitCalled = true
		return nil
	}

	root := &rootOptions{
		configPath: store.Path(),
		exePatch: exePatchOptions{
			enabledFlag:    true,
			policySettings: true,
		},
	}
	cmd := newUpgradeClaudeCmd(root)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("upgrade-claude error: %v", err)
	}
	if !prepareCalled {
		t.Fatalf("expected upgrade-claude to prewarm patched installed location")
	}
	if !waitCalled {
		t.Fatalf("expected upgrade-claude to wait for readiness")
	}
}

func TestRunUpgradeClaudeRetriesWhenInstallerLeavesUnpatchableVersionFile(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	prevProbe := probeInstalledClaudeVersionFn
	installerCalls := 0
	probeInstalledClaudeVersionFn = func(ctx context.Context, path string) (bool, error) {
		return installerCalls >= 2, nil
	}
	t.Cleanup(func() {
		probeInstalledClaudeVersionFn = prevProbe
	})

	store := newTempStore(t)
	disabled := false
	if err := store.Save(config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &disabled,
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	versionPath := filepath.Join(homeDir, ".local", "share", "claude", "versions", "2.1.90")
	if err := os.MkdirAll(filepath.Dir(versionPath), 0o755); err != nil {
		t.Fatalf("mkdir version dir: %v", err)
	}
	original := testNativeExecutableStubBytesWithMarker(claudeInstallGOOS, "not-claude")
	if err := os.WriteFile(versionPath, original, 0o700); err != nil {
		t.Fatalf("write original version file: %v", err)
	}

	launcherPath := filepath.Join(homeDir, ".local", "bin", testClaudeLauncherName(claudeInstallGOOS))
	if err := os.MkdirAll(filepath.Dir(launcherPath), 0o755); err != nil {
		t.Fatalf("mkdir launcher dir: %v", err)
	}
	if err := os.Symlink(versionPath, launcherPath); err != nil {
		t.Fatalf("symlink launcher: %v", err)
	}

	retryMovedStaleVersionAside := false
	prevInstaller := runClaudeInstallerFn
	runClaudeInstallerFn = func(ctx context.Context, out io.Writer, opts installProxyOptions) error {
		installerCalls++
		if installerCalls == 2 {
			if _, err := os.Stat(versionPath); os.IsNotExist(err) {
				retryMovedStaleVersionAside = true
			} else if err != nil {
				t.Fatalf("stat version path before retry install: %v", err)
			}
			repaired := testNativeExecutableStubBytesWithMarker(claudeInstallGOOS, "repaired")
			if err := os.WriteFile(versionPath, repaired, 0o700); err != nil {
				t.Fatalf("write repaired version file: %v", err)
			}
		}
		_, _ = fmt.Fprintf(out, "Version: 2.1.90\nLocation: %s\n", launcherPath)
		return nil
	}
	t.Cleanup(func() {
		runClaudeInstallerFn = prevInstaller
	})

	prepareCalled := false
	waitCalled := false
	maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
		prepareCalled = true
		if len(cmdArgs) != 1 || !config.PathsEqual(cmdArgs[0], launcherPath) {
			t.Fatalf("unexpected patch prep args: %v", cmdArgs)
		}
		return &patchOutcome{}, nil
	}
	waitPatchedExecutableReadyFn = func(ctx context.Context, outcome *patchOutcome) error {
		waitCalled = true
		return nil
	}

	root := &rootOptions{
		configPath: store.Path(),
		exePatch: exePatchOptions{
			enabledFlag:    true,
			policySettings: true,
		},
	}
	cmd := newUpgradeClaudeCmd(root)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("upgrade-claude error: %v", err)
	}
	if installerCalls != 2 {
		t.Fatalf("expected installer retry, got %d calls", installerCalls)
	}
	if !retryMovedStaleVersionAside {
		t.Fatalf("expected stale version file to be moved aside before retry")
	}
	if !prepareCalled {
		t.Fatalf("expected patched claude prewarm after retry")
	}
	if !waitCalled {
		t.Fatalf("expected readiness wait after retry")
	}

	got, err := os.ReadFile(versionPath)
	if err != nil {
		t.Fatalf("read repaired version file: %v", err)
	}
	if string(got) == string(original) {
		t.Fatalf("expected repaired version file to differ from original")
	}
}

func TestRunUpgradeClaudeRestoresVersionFileWhenRetryStillBroken(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	store := newTempStore(t)
	disabled := false
	if err := store.Save(config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &disabled,
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	versionsDir := filepath.Join(homeDir, ".local", "share", "claude", "versions")
	versionPath := filepath.Join(versionsDir, "2.1.90")
	if err := os.MkdirAll(filepath.Dir(versionPath), 0o755); err != nil {
		t.Fatalf("mkdir version dir: %v", err)
	}
	original := testNativeExecutableStubBytesWithMarker(claudeInstallGOOS, "original")
	if err := os.WriteFile(versionPath, original, 0o700); err != nil {
		t.Fatalf("write original version file: %v", err)
	}

	launcherPath := filepath.Join(homeDir, ".local", "bin", testClaudeLauncherName(claudeInstallGOOS))
	if err := os.MkdirAll(filepath.Dir(launcherPath), 0o755); err != nil {
		t.Fatalf("mkdir launcher dir: %v", err)
	}
	if err := os.Symlink(versionPath, launcherPath); err != nil {
		t.Fatalf("symlink launcher: %v", err)
	}

	installerCalls := 0
	prevInstaller := runClaudeInstallerFn
	runClaudeInstallerFn = func(ctx context.Context, out io.Writer, opts installProxyOptions) error {
		installerCalls++
		if installerCalls == 2 {
			retryVersionPath := filepath.Join(versionsDir, "2.1.90-retry")
			stillBroken := testNativeExecutableStubBytesWithMarker(claudeInstallGOOS, "still-bad")
			if err := os.WriteFile(retryVersionPath, stillBroken, 0o700); err != nil {
				t.Fatalf("write broken retry version file: %v", err)
			}
			if err := os.Remove(launcherPath); err != nil {
				t.Fatalf("remove launcher before repointing retry install: %v", err)
			}
			if err := os.Symlink(retryVersionPath, launcherPath); err != nil {
				t.Fatalf("repoint launcher to retry version path: %v", err)
			}
		}
		_, _ = fmt.Fprintf(out, "Version: 2.1.90\nLocation: %s\n", launcherPath)
		return nil
	}
	t.Cleanup(func() {
		runClaudeInstallerFn = prevInstaller
	})

	patchCalled := false
	maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
		patchCalled = true
		return &patchOutcome{}, nil
	}

	root := &rootOptions{
		configPath: store.Path(),
		exePatch: exePatchOptions{
			enabledFlag:    true,
			policySettings: true,
		},
	}
	cmd := newUpgradeClaudeCmd(root)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected upgrade-claude error when retry remains broken")
	}
	if !strings.Contains(err.Error(), "restored previous Claude version file") {
		t.Fatalf("expected restore message, got: %v", err)
	}
	if installerCalls != 2 {
		t.Fatalf("expected installer retry, got %d calls", installerCalls)
	}
	if patchCalled {
		t.Fatalf("expected patch prep to be skipped after failed retry")
	}

	got, readErr := os.ReadFile(versionPath)
	if readErr != nil {
		t.Fatalf("read restored version file: %v", readErr)
	}
	if string(got) != string(original) {
		t.Fatalf("expected original version file to be restored, got %q", string(got))
	}
	resolvedLauncher, resolveErr := filepath.EvalSymlinks(launcherPath)
	if resolveErr != nil {
		t.Fatalf("resolve restored launcher: %v", resolveErr)
	}
	assertSameExistingPath(t, resolvedLauncher, versionPath)
}

func TestRunUpgradeClaudeDoesNotRetryWhenVersionProbeStillWorks(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	prevProbe := probeInstalledClaudeVersionFn
	probeInstalledClaudeVersionFn = func(ctx context.Context, path string) (bool, error) {
		return true, nil
	}
	t.Cleanup(func() {
		probeInstalledClaudeVersionFn = prevProbe
	})

	store := newTempStore(t)
	disabled := false
	if err := store.Save(config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &disabled,
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	versionPath := filepath.Join(homeDir, ".local", "share", "claude", "versions", "2.1.90")
	if err := os.MkdirAll(filepath.Dir(versionPath), 0o755); err != nil {
		t.Fatalf("mkdir version dir: %v", err)
	}
	unchanged := testNativeExecutableStubBytesWithMarker(claudeInstallGOOS, "nopatch")
	if err := os.WriteFile(versionPath, unchanged, 0o700); err != nil {
		t.Fatalf("write unchanged version file: %v", err)
	}

	launcherPath := filepath.Join(homeDir, ".local", "bin", testClaudeLauncherName(claudeInstallGOOS))
	if err := os.MkdirAll(filepath.Dir(launcherPath), 0o755); err != nil {
		t.Fatalf("mkdir launcher dir: %v", err)
	}
	if err := os.Symlink(versionPath, launcherPath); err != nil {
		t.Fatalf("symlink launcher: %v", err)
	}

	installerCalls := 0
	prevInstaller := runClaudeInstallerFn
	runClaudeInstallerFn = func(ctx context.Context, out io.Writer, opts installProxyOptions) error {
		installerCalls++
		_, _ = fmt.Fprintf(out, "Version: 2.1.90\nLocation: %s\n", launcherPath)
		return nil
	}
	t.Cleanup(func() {
		runClaudeInstallerFn = prevInstaller
	})

	patchCalled := false
	waitCalled := false
	maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
		patchCalled = true
		return &patchOutcome{}, nil
	}
	waitPatchedExecutableReadyFn = func(ctx context.Context, outcome *patchOutcome) error {
		waitCalled = true
		return nil
	}

	root := &rootOptions{
		configPath: store.Path(),
		exePatch: exePatchOptions{
			enabledFlag:    true,
			policySettings: true,
		},
	}
	cmd := newUpgradeClaudeCmd(root)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("upgrade-claude error: %v", err)
	}
	if installerCalls != 1 {
		t.Fatalf("expected no retry, got %d installer calls", installerCalls)
	}
	if !patchCalled || !waitCalled {
		t.Fatalf("expected normal patch prewarm flow to continue")
	}
}

func TestRunUpgradeClaudeDoesNotRetryWhenGlibcCompatCanHandleProbe(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)
	withClaudeInstallGOOS(t, "linux")

	prevProbe := probeInstalledClaudeVersionFn
	probeCalled := false
	probeInstalledClaudeVersionFn = func(ctx context.Context, path string) (bool, error) {
		probeCalled = true
		return false, nil
	}
	t.Cleanup(func() {
		probeInstalledClaudeVersionFn = prevProbe
	})

	prevGlibcHostEligible := glibcCompatHostEligibleFn
	glibcCompatHostEligibleFn = func() bool { return true }
	t.Cleanup(func() {
		glibcCompatHostEligibleFn = prevGlibcHostEligible
	})

	store := newTempStore(t)
	disabled := false
	if err := store.Save(config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &disabled,
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	versionPath := filepath.Join(homeDir, ".local", "share", "claude", "versions", "2.1.90")
	if err := os.MkdirAll(filepath.Dir(versionPath), 0o755); err != nil {
		t.Fatalf("mkdir version dir: %v", err)
	}
	unchanged := testNativeExecutableStubBytesWithMarker(claudeInstallGOOS, "nopatch")
	if err := os.WriteFile(versionPath, unchanged, 0o700); err != nil {
		t.Fatalf("write unchanged version file: %v", err)
	}

	launcherPath := filepath.Join(homeDir, ".local", "bin", testClaudeLauncherName(claudeInstallGOOS))
	if err := os.MkdirAll(filepath.Dir(launcherPath), 0o755); err != nil {
		t.Fatalf("mkdir launcher dir: %v", err)
	}
	if err := os.Symlink(versionPath, launcherPath); err != nil {
		t.Fatalf("symlink launcher: %v", err)
	}

	installerCalls := 0
	prevInstaller := runClaudeInstallerFn
	runClaudeInstallerFn = func(ctx context.Context, out io.Writer, opts installProxyOptions) error {
		installerCalls++
		_, _ = fmt.Fprintf(out, "Version: 2.1.90\nLocation: %s\n", launcherPath)
		return nil
	}
	t.Cleanup(func() {
		runClaudeInstallerFn = prevInstaller
	})

	patchCalled := false
	waitCalled := false
	maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
		patchCalled = true
		return &patchOutcome{}, nil
	}
	waitPatchedExecutableReadyFn = func(ctx context.Context, outcome *patchOutcome) error {
		waitCalled = true
		return nil
	}

	root := &rootOptions{
		configPath: store.Path(),
		exePatch: exePatchOptions{
			enabledFlag:    true,
			policySettings: true,
			glibcCompat:    true,
		},
	}
	cmd := newUpgradeClaudeCmd(root)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("upgrade-claude error: %v", err)
	}
	if installerCalls != 1 {
		t.Fatalf("expected no retry when glibc compat can handle probe, got %d installer calls", installerCalls)
	}
	if probeCalled {
		t.Fatalf("expected raw version probe to be skipped when glibc compat is configured")
	}
	if !patchCalled || !waitCalled {
		t.Fatalf("expected normal patch prewarm flow to continue")
	}
}

func TestRunUpgradeClaudeReusesRecoveredLauncherWhenInstalledClaudeStillBrokenOnUnsupportedKernel(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("old-kernel Claude fallback only applies on linux")
	}

	requireExePatchEnabled(t)
	withExePatchTestHooks(t)
	withClaudeInstallGOOS(t, "linux")

	prevReadKernelReleaseFn := readLinuxKernelReleaseFn
	readLinuxKernelReleaseFn = func() ([]byte, error) { return []byte("3.10.0-1160.el7.x86_64"), nil }
	prevInstaller := runClaudeInstallerFn
	prevPatch := maybePatchExecutableCtxFn
	prevWait := waitPatchedExecutableReadyFn
	prevGlibcPatch := applyClaudeGlibcCompatPatchFn
	t.Cleanup(func() {
		readLinuxKernelReleaseFn = prevReadKernelReleaseFn
		runClaudeInstallerFn = prevInstaller
		maybePatchExecutableCtxFn = prevPatch
		waitPatchedExecutableReadyFn = prevWait
		applyClaudeGlibcCompatPatchFn = prevGlibcPatch
	})

	store := newTempStore(t)
	disabled := false
	if err := store.Save(config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &disabled,
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	homeDir := filepath.Join(t.TempDir(), "home")
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv(claudeProxyHostIDEnv, "upgrade-old-kernel-reuse-recovery-host")

	launcherPath := filepath.Join(homeDir, ".local", "bin", testClaudeLauncherName(claudeInstallGOOS))
	versionPath := filepath.Join(homeDir, ".local", "share", "claude", "versions", "2.1.114")
	recoveryLauncher := filepath.Join(cacheRoot, "claude-proxy", "hosts", "upgrade-old-kernel-reuse-recovery-host", "install-recovery", testClaudeLauncherName(claudeInstallGOOS))
	compatPath := filepath.Join(t.TempDir(), "compat", testClaudeLauncherName(claudeInstallGOOS))
	customCompatRoot := filepath.Join(t.TempDir(), "custom-glibc-root")

	installerCalls := 0
	runClaudeInstallerFn = func(ctx context.Context, out io.Writer, opts installProxyOptions) error {
		installerCalls++
		if err := os.MkdirAll(filepath.Dir(versionPath), 0o755); err != nil {
			t.Fatalf("mkdir version dir: %v", err)
		}
		script := "#!/bin/sh\necho 'broken installed claude' >&2\nexit 1\n"
		if err := os.WriteFile(versionPath, []byte(script), 0o700); err != nil {
			t.Fatalf("write version file: %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(launcherPath), 0o755); err != nil {
			t.Fatalf("mkdir launcher dir: %v", err)
		}
		if err := os.Remove(launcherPath); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove launcher before symlink: %v", err)
		}
		if err := os.Symlink(versionPath, launcherPath); err != nil {
			t.Fatalf("symlink launcher: %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(recoveryLauncher), 0o755); err != nil {
			t.Fatalf("mkdir recovery launcher dir: %v", err)
		}
		recoveryScript := "#!/bin/sh\necho \"/lib64/libc.so.6: version \\`GLIBC_2.25' not found\" >&2\nexit 1\n"
		if err := os.WriteFile(recoveryLauncher, []byte(recoveryScript), 0o700); err != nil {
			t.Fatalf("write recovery launcher: %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(compatPath), 0o755); err != nil {
			t.Fatalf("mkdir compat dir: %v", err)
		}
		compatScript := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo 'Claude Code 2.1.112'\n  exit 0\nfi\nexit 0\n"
		if err := os.WriteFile(compatPath, []byte(compatScript), 0o700); err != nil {
			t.Fatalf("write compat launcher: %v", err)
		}
		_, _ = fmt.Fprintf(out, "Version: 2.1.114\nLocation: %s\n", launcherPath)
		return nil
	}

	applyClaudeGlibcCompatPatchFn = func(path string, opts exePatchOptions, log io.Writer, dryRun bool, outcome *patchOutcome) (*patchOutcome, bool, error) {
		if !config.PathsEqual(path, recoveryLauncher) {
			t.Fatalf("expected glibc compat probe for %q, got %q", recoveryLauncher, path)
		}
		if opts.glibcCompatRoot != customCompatRoot {
			t.Fatalf("expected glibc compat root %q, got %q", customCompatRoot, opts.glibcCompatRoot)
		}
		return &patchOutcome{
			SourcePath:       path,
			TargetPath:       compatPath,
			LaunchArgsPrefix: []string{compatPath},
			IsClaude:         true,
		}, true, nil
	}

	patchedPath := ""
	maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
		if len(cmdArgs) != 1 {
			t.Fatalf("unexpected patch args: %v", cmdArgs)
		}
		patchedPath = cmdArgs[0]
		return &patchOutcome{}, nil
	}
	waitPatchedExecutableReadyFn = func(ctx context.Context, outcome *patchOutcome) error { return nil }

	root := &rootOptions{
		configPath: store.Path(),
		exePatch: exePatchOptions{
			enabledFlag:     true,
			policySettings:  true,
			glibcCompat:     true,
			glibcCompatRoot: customCompatRoot,
		},
	}
	cmd := newUpgradeClaudeCmd(root)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("upgrade-claude error: %v", err)
	}
	if installerCalls != 1 {
		t.Fatalf("expected one installer call, got %d", installerCalls)
	}
	if !config.PathsEqual(patchedPath, recoveryLauncher) {
		t.Fatalf("expected patched path %q, got %q", recoveryLauncher, patchedPath)
	}
}

func TestRunUpgradeClaudeSkipsRecoveryReuseWhenGlibcCompatDisabled(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("old-kernel Claude fallback only applies on linux")
	}

	requireExePatchEnabled(t)
	withExePatchTestHooks(t)
	withClaudeInstallGOOS(t, "linux")

	prevReadKernelReleaseFn := readLinuxKernelReleaseFn
	readLinuxKernelReleaseFn = func() ([]byte, error) { return []byte("3.10.0-1160.el7.x86_64"), nil }
	prevInstaller := runClaudeInstallerFn
	prevPatch := maybePatchExecutableCtxFn
	prevWait := waitPatchedExecutableReadyFn
	prevGlibcPatch := applyClaudeGlibcCompatPatchFn
	t.Cleanup(func() {
		readLinuxKernelReleaseFn = prevReadKernelReleaseFn
		runClaudeInstallerFn = prevInstaller
		maybePatchExecutableCtxFn = prevPatch
		waitPatchedExecutableReadyFn = prevWait
		applyClaudeGlibcCompatPatchFn = prevGlibcPatch
	})

	store := newTempStore(t)
	disabled := false
	if err := store.Save(config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &disabled,
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	homeDir := filepath.Join(t.TempDir(), "home")
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv(claudeProxyHostIDEnv, "upgrade-old-kernel-disabled-glibc-host")

	launcherPath := filepath.Join(homeDir, ".local", "bin", testClaudeLauncherName(claudeInstallGOOS))
	if err := os.MkdirAll(filepath.Dir(launcherPath), 0o755); err != nil {
		t.Fatalf("mkdir launcher dir: %v", err)
	}
	if err := os.WriteFile(launcherPath, []byte("#!/bin/sh\necho 'broken installed claude' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatalf("write launcher: %v", err)
	}

	recoveryLauncher := filepath.Join(cacheRoot, "claude-proxy", "hosts", "upgrade-old-kernel-disabled-glibc-host", "install-recovery", testClaudeLauncherName(claudeInstallGOOS))
	if err := os.MkdirAll(filepath.Dir(recoveryLauncher), 0o755); err != nil {
		t.Fatalf("mkdir recovery dir: %v", err)
	}
	recoveryScript := "#!/bin/sh\necho \"/lib64/libc.so.6: version \\`GLIBC_2.25' not found\" >&2\nexit 1\n"
	if err := os.WriteFile(recoveryLauncher, []byte(recoveryScript), 0o700); err != nil {
		t.Fatalf("write recovery launcher: %v", err)
	}

	installerCalls := 0
	runClaudeInstallerFn = func(ctx context.Context, out io.Writer, opts installProxyOptions) error {
		installerCalls++
		_, _ = fmt.Fprintf(out, "Version: 2.1.114\nLocation: %s\n", launcherPath)
		return nil
	}
	applyClaudeGlibcCompatPatchFn = func(path string, opts exePatchOptions, log io.Writer, dryRun bool, outcome *patchOutcome) (*patchOutcome, bool, error) {
		t.Fatalf("did not expect glibc compat probe when the flag is disabled")
		return nil, false, nil
	}

	patchedPath := ""
	maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
		if len(cmdArgs) != 1 {
			t.Fatalf("unexpected patch args: %v", cmdArgs)
		}
		patchedPath = cmdArgs[0]
		return &patchOutcome{}, nil
	}
	waitPatchedExecutableReadyFn = func(ctx context.Context, outcome *patchOutcome) error { return nil }

	root := &rootOptions{
		configPath: store.Path(),
		exePatch: exePatchOptions{
			enabledFlag:    true,
			policySettings: true,
			glibcCompat:    false,
		},
	}
	cmd := newUpgradeClaudeCmd(root)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected upgrade-claude to fail without glibc compat or a usable recovered launcher")
	}
	if !strings.Contains(err.Error(), "still unusable") {
		t.Fatalf("expected unusable launcher error, got %v", err)
	}
	if installerCalls != 1 {
		t.Fatalf("expected one installer call, got %d", installerCalls)
	}
	if patchedPath != "" {
		t.Fatalf("expected no patch prewarm after failed refresh, got %q", patchedPath)
	}
}

func TestRunUpgradeClaudeKeepsUsableNativeClaudeOnUnsupportedKernel(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("old-kernel Claude fallback only applies on linux")
	}

	requireExePatchEnabled(t)
	withExePatchTestHooks(t)
	withClaudeInstallGOOS(t, "linux")

	prevReadKernelReleaseFn := readLinuxKernelReleaseFn
	readLinuxKernelReleaseFn = func() ([]byte, error) { return []byte("4.18.0-553.el8.x86_64"), nil }
	prevInstaller := runClaudeInstallerFn
	prevPatch := maybePatchExecutableCtxFn
	prevWait := waitPatchedExecutableReadyFn
	t.Cleanup(func() {
		readLinuxKernelReleaseFn = prevReadKernelReleaseFn
		runClaudeInstallerFn = prevInstaller
		maybePatchExecutableCtxFn = prevPatch
		waitPatchedExecutableReadyFn = prevWait
	})

	store := newTempStore(t)
	disabled := false
	if err := store.Save(config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &disabled,
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	homeDir := filepath.Join(t.TempDir(), "home")
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv(claudeProxyHostIDEnv, "upgrade-old-kernel-native-host")

	launcherPath := filepath.Join(homeDir, ".local", "bin", testClaudeLauncherName(claudeInstallGOOS))
	installerCalls := 0
	runClaudeInstallerFn = func(ctx context.Context, out io.Writer, opts installProxyOptions) error {
		installerCalls++
		if err := os.MkdirAll(filepath.Dir(launcherPath), 0o755); err != nil {
			t.Fatalf("mkdir launcher dir: %v", err)
		}
		script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo 'Claude Code 2.1.112'\n  exit 0\nfi\nexit 0\n"
		if err := os.WriteFile(launcherPath, []byte(script), 0o700); err != nil {
			t.Fatalf("write launcher: %v", err)
		}
		_, _ = fmt.Fprintf(out, "Version: 2.1.112\nLocation: %s\n", launcherPath)
		return nil
	}

	patchedPath := ""
	maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
		if len(cmdArgs) != 1 {
			t.Fatalf("unexpected patch args: %v", cmdArgs)
		}
		patchedPath = cmdArgs[0]
		return &patchOutcome{}, nil
	}
	waitPatchedExecutableReadyFn = func(ctx context.Context, outcome *patchOutcome) error { return nil }

	root := &rootOptions{
		configPath: store.Path(),
		exePatch: exePatchOptions{
			enabledFlag:    true,
			policySettings: true,
		},
	}
	cmd := newUpgradeClaudeCmd(root)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("upgrade-claude error: %v", err)
	}
	if installerCalls != 1 {
		t.Fatalf("expected one installer call, got %d", installerCalls)
	}
	if !config.PathsEqual(patchedPath, launcherPath) {
		t.Fatalf("expected patched path %q, got %q", launcherPath, patchedPath)
	}
}

func TestRunUpgradeClaudeWithProxyUsesProxyEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	instanceID := "inst-1"
	mux := http.NewServeMux()
	mux.HandleFunc("/_claude_proxy/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":         true,
			"instanceId": instanceID,
		})
	})
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(ln) }()
	t.Cleanup(func() { _ = server.Close() })

	store := newTempStore(t)
	enabled := true
	profile := config.Profile{ID: "profile-1", Name: "profile-1"}
	if err := store.Save(config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &enabled,
		Profiles:     []config.Profile{profile},
		Instances: []config.Instance{{
			ID:        instanceID,
			ProfileID: profile.ID,
			Kind:      config.InstanceKindDaemon,
			HTTPPort:  port,
			DaemonPID: os.Getpid(),
		}},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := t.TempDir()
	outFile := filepath.Join(dir, "env.txt")
	script := filepath.Join(dir, "bash")
	scriptBody := "#!/bin/sh\nprintf \"%s\\n%s\\n\" \"$HTTP_PROXY\" \"$HTTPS_PROXY\" > \"$OUT_FILE\"\nexit 0\n"
	if err := os.WriteFile(script, []byte(scriptBody), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}

	t.Setenv("OUT_FILE", outFile)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	root := &rootOptions{configPath: store.Path()}
	cmd := newUpgradeClaudeCmd(root)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("upgrade-claude error: %v", err)
	}

	content, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), string(content))
	}
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if lines[0] != proxyURL || lines[1] != proxyURL {
		t.Fatalf("expected proxy env %q, got %q", proxyURL, strings.Join(lines, ","))
	}
}

func TestNewUpgradeClaudeCmdExists(t *testing.T) {
	root := &rootOptions{}
	cmd := newUpgradeClaudeCmd(root)
	if cmd.Use != "upgrade-claude" {
		t.Fatalf("expected Use='upgrade-claude', got %q", cmd.Use)
	}
	if cmd.Short != "Refresh Claude Code so claude-proxy has a usable launcher on this host" {
		t.Fatalf("unexpected Short=%q", cmd.Short)
	}
	if !strings.Contains(cmd.Long, "claude-proxy init") {
		t.Fatalf("expected Long help to mention init prerequisite, got %q", cmd.Long)
	}
	f := cmd.Flags().Lookup("profile")
	if f == nil {
		t.Fatalf("expected --profile flag")
	}
	if f.DefValue != "" {
		t.Fatalf("expected empty default for --profile, got %q", f.DefValue)
	}
	versionFlag := cmd.Flags().Lookup("version")
	if versionFlag == nil {
		t.Fatalf("expected --version flag")
	}
	if versionFlag.DefValue != "" {
		t.Fatalf("expected empty default for --version, got %q", versionFlag.DefValue)
	}
}

func TestUpgradeClaudeCmdRegistered(t *testing.T) {
	root := newRootCmd()
	found := false
	for _, sub := range root.Commands() {
		if sub.Use == "upgrade-claude" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("upgrade-claude subcommand not registered on root command")
	}
}

func TestRunUpgradeClaudeStatError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip permission test on windows")
	}
	if os.Getuid() == 0 {
		t.Skip("skip permission test when running as root")
	}

	dir := t.TempDir()
	subDir := filepath.Join(dir, "restricted")
	if err := os.Mkdir(subDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	configPath := filepath.Join(subDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.Chmod(subDir, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(subDir, 0o700) })

	root := &rootOptions{configPath: configPath}
	cmd := newUpgradeClaudeCmd(root)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error for inaccessible config")
	}
	if !strings.Contains(err.Error(), "cannot access") {
		t.Fatalf("expected 'cannot access' error, got: %v", err)
	}
}

func TestRunUpgradeClaudeCorruptConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{{{`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	root := &rootOptions{configPath: configPath}
	cmd := newUpgradeClaudeCmd(root)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error for corrupt config")
	}
	if strings.Contains(err.Error(), "not been initialized") {
		t.Fatalf("should not report 'not been initialized' for corrupt config, got: %v", err)
	}
}

func TestUpgradeClaudeInstallOptsProxyEnabledNoProfiles(t *testing.T) {
	enabled := true
	cfg := config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &enabled,
		Profiles:     []config.Profile{},
	}
	_, err := upgradeClaudeInstallOpts(cfg, "")
	if err == nil {
		t.Fatalf("expected error when proxy enabled but no profiles")
	}
	if !strings.Contains(err.Error(), "no profiles found") {
		t.Fatalf("expected 'no profiles found' error, got: %v", err)
	}
}

func TestRunUpgradeClaudeWithProfileFlag(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	instanceID := "inst-1"
	mux := http.NewServeMux()
	mux.HandleFunc("/_claude_proxy/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":         true,
			"instanceId": instanceID,
		})
	})
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(ln) }()
	t.Cleanup(func() { _ = server.Close() })

	store := newTempStore(t)
	enabled := true
	if err := store.Save(config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &enabled,
		Profiles: []config.Profile{
			{ID: "p1", Name: "alpha"},
			{ID: "p2", Name: "beta"},
		},
		Instances: []config.Instance{{
			ID:        instanceID,
			ProfileID: "p2",
			Kind:      config.InstanceKindDaemon,
			HTTPPort:  port,
			DaemonPID: os.Getpid(),
		}},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := t.TempDir()
	outFile := filepath.Join(dir, "env.txt")
	script := filepath.Join(dir, "bash")
	scriptBody := "#!/bin/sh\nprintf \"%s\\n%s\\n\" \"$HTTP_PROXY\" \"$HTTPS_PROXY\" > \"$OUT_FILE\"\nexit 0\n"
	if err := os.WriteFile(script, []byte(scriptBody), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}

	t.Setenv("OUT_FILE", outFile)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	root := &rootOptions{configPath: store.Path()}
	cmd := newUpgradeClaudeCmd(root)
	cmd.SetArgs([]string{"--profile", "beta"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("upgrade-claude --profile beta error: %v", err)
	}

	content, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), string(content))
	}
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if lines[0] != proxyURL || lines[1] != proxyURL {
		t.Fatalf("expected proxy env %q, got %q", proxyURL, strings.Join(lines, ","))
	}
}

func TestRunUpgradeClaudeSpecificVersionCleansHigherDefaultVersions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip symlink test on windows")
	}

	prevLookPath := claudeInstallLookPathFn
	claudeInstallLookPathFn = func(file string) (string, error) {
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	}
	t.Cleanup(func() {
		claudeInstallLookPathFn = prevLookPath
	})

	store := newTempStore(t)
	disabled := false
	if err := store.Save(config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &disabled,
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	rootDir := t.TempDir()
	homeDir := filepath.Join(rootDir, "home")
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	defaultVersionsDir := filepath.Join(homeDir, ".local", "share", "claude", "versions")
	if err := os.MkdirAll(defaultVersionsDir, 0o755); err != nil {
		t.Fatalf("mkdir default versions dir: %v", err)
	}
	higherDefault := filepath.Join(defaultVersionsDir, "2.1.91")
	if err := os.WriteFile(higherDefault, testNativeExecutableStubBytesWithMarker(claudeInstallGOOS, "higher-default"), 0o700); err != nil {
		t.Fatalf("write higher default version: %v", err)
	}

	customVersionsDir := filepath.Join(rootDir, "custom", "claude", "versions")
	if err := os.MkdirAll(customVersionsDir, 0o755); err != nil {
		t.Fatalf("mkdir custom versions dir: %v", err)
	}
	higherCustom := filepath.Join(customVersionsDir, "2.1.91")
	if err := os.WriteFile(higherCustom, testNativeExecutableStubBytesWithMarker(claudeInstallGOOS, "higher-custom"), 0o700); err != nil {
		t.Fatalf("write higher custom version: %v", err)
	}

	targetPath := filepath.Join(defaultVersionsDir, "2.1.90")
	launcherPath := filepath.Join(homeDir, ".local", "bin", testClaudeLauncherName(claudeInstallGOOS))
	installerCalls := 0
	prevInstaller := runClaudeInstallerFn
	runClaudeInstallerFn = func(ctx context.Context, out io.Writer, opts installProxyOptions) error {
		installerCalls++
		if opts.TargetVersion != "2.1.90" {
			t.Fatalf("expected normalized target version 2.1.90, got %q", opts.TargetVersion)
		}
		if err := os.WriteFile(targetPath, testNativeExecutableStubBytesWithMarker(claudeInstallGOOS, "target"), 0o700); err != nil {
			t.Fatalf("write target version: %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(launcherPath), 0o755); err != nil {
			t.Fatalf("mkdir launcher dir: %v", err)
		}
		if err := os.Remove(launcherPath); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove old launcher: %v", err)
		}
		if err := os.Symlink(targetPath, launcherPath); err != nil {
			t.Fatalf("symlink launcher: %v", err)
		}
		_, _ = fmt.Fprintf(out, "Version: 2.1.90\nLocation: %s\n", launcherPath)
		return nil
	}
	t.Cleanup(func() {
		runClaudeInstallerFn = prevInstaller
	})

	root := &rootOptions{configPath: store.Path()}
	cmd := newUpgradeClaudeCmd(root)
	cmd.SetArgs([]string{"--version", "v2.1.90"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("upgrade-claude --version error: %v", err)
	}
	if installerCalls != 1 {
		t.Fatalf("expected one installer call, got %d", installerCalls)
	}
	if _, err := os.Stat(targetPath); err != nil {
		t.Fatalf("expected target default version to remain: %v", err)
	}
	if _, err := os.Stat(higherDefault); !os.IsNotExist(err) {
		t.Fatalf("expected higher default version to be removed, stat err=%v", err)
	}
	if _, err := os.Stat(higherCustom); err != nil {
		t.Fatalf("expected higher custom version to remain: %v", err)
	}
}

func TestRunUpgradeClaudeSpecificVersionRestoresHigherDefaultVersionsWhenPatchFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip symlink test on windows")
	}
	withExePatchTestHooks(t)

	prevLookPath := claudeInstallLookPathFn
	claudeInstallLookPathFn = func(file string) (string, error) {
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	}
	t.Cleanup(func() {
		claudeInstallLookPathFn = prevLookPath
	})

	store := newTempStore(t)
	disabled := false
	if err := store.Save(config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &disabled,
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	homeDir := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	defaultVersionsDir := filepath.Join(homeDir, ".local", "share", "claude", "versions")
	if err := os.MkdirAll(defaultVersionsDir, 0o755); err != nil {
		t.Fatalf("mkdir default versions dir: %v", err)
	}
	higherDefault := filepath.Join(defaultVersionsDir, "2.1.91")
	higherDefaultBytes := testNativeExecutableStubBytesWithMarker(claudeInstallGOOS, "higher-default")
	if err := os.WriteFile(higherDefault, higherDefaultBytes, 0o700); err != nil {
		t.Fatalf("write higher default version: %v", err)
	}

	targetPath := filepath.Join(defaultVersionsDir, "2.1.90")
	launcherPath := filepath.Join(homeDir, ".local", "bin", testClaudeLauncherName(claudeInstallGOOS))
	prevInstaller := runClaudeInstallerFn
	runClaudeInstallerFn = func(ctx context.Context, out io.Writer, opts installProxyOptions) error {
		if err := os.WriteFile(targetPath, testNativeExecutableStubBytesWithMarker(claudeInstallGOOS, "target"), 0o700); err != nil {
			t.Fatalf("write target version: %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(launcherPath), 0o755); err != nil {
			t.Fatalf("mkdir launcher dir: %v", err)
		}
		if err := os.Remove(launcherPath); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove old launcher: %v", err)
		}
		if err := os.Symlink(targetPath, launcherPath); err != nil {
			t.Fatalf("symlink launcher: %v", err)
		}
		_, _ = fmt.Fprintf(out, "Version: 2.1.90\nLocation: %s\n", launcherPath)
		return nil
	}
	t.Cleanup(func() {
		runClaudeInstallerFn = prevInstaller
	})

	maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
		return nil, fmt.Errorf("patch failed")
	}

	root := &rootOptions{
		configPath: store.Path(),
		exePatch: exePatchOptions{
			enabledFlag:    true,
			policySettings: true,
		},
	}
	cmd := newUpgradeClaudeCmd(root)
	cmd.SetArgs([]string{"--version", "2.1.90"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "patch failed") {
		t.Fatalf("expected patch failure, got %v", err)
	}
	got, readErr := os.ReadFile(higherDefault)
	if readErr != nil {
		t.Fatalf("expected higher default version to be restored: %v", readErr)
	}
	if string(got) != string(higherDefaultBytes) {
		t.Fatalf("expected restored higher default version bytes to match original")
	}
	entries, readDirErr := os.ReadDir(defaultVersionsDir)
	if readDirErr != nil {
		t.Fatalf("read default versions dir: %v", readDirErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".claude-proxy-stash-") {
			t.Fatalf("expected no leftover stash entry after failed upgrade, found %s", entry.Name())
		}
	}
}

func TestClaudeVersionCleanupStashSkipsNonNumericTargets(t *testing.T) {
	for _, target := range []string{"", "latest", "stable"} {
		t.Run(target, func(t *testing.T) {
			homeDir := filepath.Join(t.TempDir(), "home")
			t.Setenv("HOME", homeDir)
			t.Setenv("USERPROFILE", homeDir)

			versionsDir := filepath.Join(homeDir, ".local", "share", "claude", "versions")
			if err := os.MkdirAll(versionsDir, 0o755); err != nil {
				t.Fatalf("mkdir versions dir: %v", err)
			}
			higher := filepath.Join(versionsDir, "2.1.91")
			if err := os.WriteFile(higher, []byte("keep"), 0o700); err != nil {
				t.Fatalf("write higher version: %v", err)
			}

			stash := newClaudeVersionCleanupStash(target)
			if err := stash.Stash(io.Discard, claudeInstallGOOS, os.Getenv); err != nil {
				t.Fatalf("stash error: %v", err)
			}
			if err := stash.Commit(io.Discard); err != nil {
				t.Fatalf("commit error: %v", err)
			}
			if got, err := os.ReadFile(higher); err != nil || string(got) != "keep" {
				t.Fatalf("expected non-numeric target %q to keep higher version, got %q err=%v", target, got, err)
			}
		})
	}
}

func TestClaudeVersionCleanupStashRestoresOriginalAcrossRetryDuplicate(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	versionsDir := filepath.Join(homeDir, ".local", "share", "claude", "versions")
	if err := os.MkdirAll(versionsDir, 0o755); err != nil {
		t.Fatalf("mkdir versions dir: %v", err)
	}
	higher := filepath.Join(versionsDir, "2.1.91")
	if err := os.WriteFile(higher, []byte("original"), 0o700); err != nil {
		t.Fatalf("write original higher version: %v", err)
	}

	stash := newClaudeVersionCleanupStash("2.1.90")
	if err := stash.Stash(io.Discard, claudeInstallGOOS, os.Getenv); err != nil {
		t.Fatalf("first stash error: %v", err)
	}
	if err := os.WriteFile(higher, []byte("retry"), 0o700); err != nil {
		t.Fatalf("write retry higher version: %v", err)
	}
	if err := stash.Stash(io.Discard, claudeInstallGOOS, os.Getenv); err != nil {
		t.Fatalf("second stash error: %v", err)
	}
	if err := stash.Restore(); err != nil {
		t.Fatalf("restore error: %v", err)
	}
	got, err := os.ReadFile(higher)
	if err != nil {
		t.Fatalf("read restored higher version: %v", err)
	}
	if string(got) != "original" {
		t.Fatalf("expected original higher version to be restored, got %q", got)
	}
	entries, err := os.ReadDir(versionsDir)
	if err != nil {
		t.Fatalf("read versions dir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".claude-proxy-stash-") {
			t.Fatalf("expected duplicate stash to be removed, found %s", entry.Name())
		}
	}
}

func TestNormalizeClaudeInstallTarget(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{input: "", want: ""},
		{input: " latest ", want: "latest"},
		{input: "stable", want: "stable"},
		{input: "v2.1.90", want: "2.1.90"},
		{input: "V2.1.90", want: "2.1.90"},
		{input: "2.1.90-beta.1", want: "2.1.90-beta.1"},
		{input: "2.1", wantErr: true},
		{input: "2.1.90;rm", wantErr: true},
	}

	for _, tc := range tests {
		got, err := normalizeClaudeInstallTarget(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("normalizeClaudeInstallTarget(%q) expected error", tc.input)
			}
			continue
		}
		if err != nil {
			t.Fatalf("normalizeClaudeInstallTarget(%q) unexpected error: %v", tc.input, err)
		}
		if got != tc.want {
			t.Fatalf("normalizeClaudeInstallTarget(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestSnapshotInstalledClaudeBinaryCapturesLauncherSymlinkState(t *testing.T) {
	withExePatchTestHooks(t)

	dir := t.TempDir()
	versionsDir := filepath.Join(dir, "versions")
	versionPath := filepath.Join(versionsDir, "2.1.90")
	if err := os.MkdirAll(versionsDir, 0o755); err != nil {
		t.Fatalf("mkdir versions dir: %v", err)
	}
	if err := os.WriteFile(versionPath, testNativeExecutableStubBytes(claudeInstallGOOS), 0o700); err != nil {
		t.Fatalf("write version file: %v", err)
	}

	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}
	launcherPath := filepath.Join(binDir, "claude")
	relativeTarget, err := filepath.Rel(binDir, versionPath)
	if err != nil {
		t.Fatalf("build relative symlink target: %v", err)
	}
	if err := os.Symlink(relativeTarget, launcherPath); err != nil {
		t.Fatalf("create launcher symlink: %v", err)
	}

	got, err := snapshotInstalledClaudeBinary(launcherPath)
	if err != nil {
		t.Fatalf("snapshotInstalledClaudeBinary error: %v", err)
	}
	wantSHA, err := hashFileSHA256(versionPath)
	if err != nil {
		t.Fatalf("hash version file: %v", err)
	}
	if !got.LauncherWasSymlink {
		t.Fatalf("expected launcher symlink metadata to be captured")
	}
	if got.LauncherLinkTarget != relativeTarget {
		t.Fatalf("expected raw launcher target %q, got %q", relativeTarget, got.LauncherLinkTarget)
	}
	if !config.PathsEqual(got.Path, launcherPath) {
		t.Fatalf("expected launcher path %q, got %q", launcherPath, got.Path)
	}
	assertSameExistingPath(t, got.ResolvedPath, versionPath)
	if got.SHA256 != wantSHA {
		t.Fatalf("expected sha %q, got %q", wantSHA, got.SHA256)
	}
}

func TestRestoreInstalledClaudeLauncherRestoresOriginalSymlinkTarget(t *testing.T) {
	withExePatchTestHooks(t)

	dir := t.TempDir()
	versionsDir := filepath.Join(dir, "versions")
	versionPath := filepath.Join(versionsDir, "2.1.90")
	if err := os.MkdirAll(versionsDir, 0o755); err != nil {
		t.Fatalf("mkdir versions dir: %v", err)
	}
	if err := os.WriteFile(versionPath, testNativeExecutableStubBytes(claudeInstallGOOS), 0o700); err != nil {
		t.Fatalf("write original version file: %v", err)
	}

	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}
	launcherPath := filepath.Join(binDir, "claude")
	originalTarget, err := filepath.Rel(binDir, versionPath)
	if err != nil {
		t.Fatalf("build original symlink target: %v", err)
	}
	if err := os.Symlink(originalTarget, launcherPath); err != nil {
		t.Fatalf("create launcher symlink: %v", err)
	}

	snapshot, err := snapshotInstalledClaudeBinary(launcherPath)
	if err != nil {
		t.Fatalf("snapshotInstalledClaudeBinary error: %v", err)
	}

	retryVersionPath := filepath.Join(versionsDir, "2.1.90-retry")
	if err := os.WriteFile(retryVersionPath, testNativeExecutableStubBytes(claudeInstallGOOS), 0o700); err != nil {
		t.Fatalf("write retry version file: %v", err)
	}
	if err := os.Remove(launcherPath); err != nil {
		t.Fatalf("remove launcher before repointing: %v", err)
	}
	retryTarget, err := filepath.Rel(binDir, retryVersionPath)
	if err != nil {
		t.Fatalf("build retry symlink target: %v", err)
	}
	if err := os.Symlink(retryTarget, launcherPath); err != nil {
		t.Fatalf("repoint launcher symlink: %v", err)
	}

	if err := restoreInstalledClaudeLauncher(snapshot); err != nil {
		t.Fatalf("restoreInstalledClaudeLauncher error: %v", err)
	}

	gotTarget, err := os.Readlink(launcherPath)
	if err != nil {
		t.Fatalf("read restored launcher target: %v", err)
	}
	if gotTarget != originalTarget {
		t.Fatalf("expected restored raw target %q, got %q", originalTarget, gotTarget)
	}
	resolved, err := filepath.EvalSymlinks(launcherPath)
	if err != nil {
		t.Fatalf("resolve restored launcher: %v", err)
	}
	assertSameExistingPath(t, resolved, versionPath)
}

func TestInstalledClaudeBinaryProblemSkipsProbeWhenGlibcCompatCanRepair(t *testing.T) {
	withExePatchTestHooks(t)
	withClaudeInstallGOOS(t, "linux")

	prevProbe := probeInstalledClaudeVersionFn
	probeCalled := false
	probeInstalledClaudeVersionFn = func(ctx context.Context, path string) (bool, error) {
		probeCalled = true
		return false, nil
	}
	t.Cleanup(func() {
		probeInstalledClaudeVersionFn = prevProbe
	})

	prevGlibcHostEligible := glibcCompatHostEligibleFn
	glibcCompatHostEligibleFn = func() bool { return true }
	t.Cleanup(func() {
		glibcCompatHostEligibleFn = prevGlibcHostEligible
	})

	dir := t.TempDir()
	versionPath := filepath.Join(dir, "home", ".local", "share", "claude", "versions", "2.1.90")
	if err := os.MkdirAll(filepath.Dir(versionPath), 0o755); err != nil {
		t.Fatalf("mkdir version dir: %v", err)
	}
	if err := os.WriteFile(versionPath, testNativeExecutableStubBytes(claudeInstallGOOS), 0o700); err != nil {
		t.Fatalf("write version file: %v", err)
	}

	problem, bad, err := installedClaudeBinaryProblem(context.Background(), installedClaudeBinaryState{
		ResolvedPath: versionPath,
	}, exePatchOptions{
		enabledFlag:    true,
		policySettings: true,
		glibcCompat:    true,
	})
	if err != nil {
		t.Fatalf("installedClaudeBinaryProblem error: %v", err)
	}
	if bad || problem != "" {
		t.Fatalf("expected glibc compat to suppress reinstall, got bad=%v problem=%q", bad, problem)
	}
	if probeCalled {
		t.Fatalf("expected raw version probe to be skipped when glibc compat can repair")
	}
}

func TestInstalledClaudeBinaryProblemRequiresProbeWhenGlibcCompatUnavailable(t *testing.T) {
	withExePatchTestHooks(t)
	withClaudeInstallGOOS(t, "linux")

	prevProbe := probeInstalledClaudeVersionFn
	probeCalled := false
	probeInstalledClaudeVersionFn = func(ctx context.Context, path string) (bool, error) {
		probeCalled = true
		return false, nil
	}
	t.Cleanup(func() {
		probeInstalledClaudeVersionFn = prevProbe
	})

	prevGlibcHostEligible := glibcCompatHostEligibleFn
	glibcCompatHostEligibleFn = func() bool { return false }
	t.Cleanup(func() {
		glibcCompatHostEligibleFn = prevGlibcHostEligible
	})

	dir := t.TempDir()
	versionPath := filepath.Join(dir, "home", ".local", "share", "claude", "versions", "2.1.90")
	if err := os.MkdirAll(filepath.Dir(versionPath), 0o755); err != nil {
		t.Fatalf("mkdir version dir: %v", err)
	}
	if err := os.WriteFile(versionPath, testNativeExecutableStubBytes(claudeInstallGOOS), 0o700); err != nil {
		t.Fatalf("write version file: %v", err)
	}

	problem, bad, err := installedClaudeBinaryProblem(context.Background(), installedClaudeBinaryState{
		ResolvedPath: versionPath,
	}, exePatchOptions{
		enabledFlag:    true,
		policySettings: true,
		glibcCompat:    true,
	})
	if err != nil {
		t.Fatalf("installedClaudeBinaryProblem error: %v", err)
	}
	if !bad {
		t.Fatalf("expected non-probeable binary to be considered broken when glibc compat is unavailable")
	}
	if !strings.Contains(problem, "probe failed") {
		t.Fatalf("expected probe failure reason, got %q", problem)
	}
	if !probeCalled {
		t.Fatalf("expected raw version probe to run when glibc compat is unavailable")
	}
}

func TestInvalidateExePatchState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip on windows")
	}

	// Create a fake "claude" binary.
	binDir := t.TempDir()
	fakeClaude := filepath.Join(binDir, "claude")
	if err := os.WriteFile(fakeClaude, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Resolve the path the same way invalidateExePatchState will (follows symlinks).
	resolvedClaude, err := resolveExecutablePath(fakeClaude)
	if err != nil {
		t.Fatalf("resolve path: %v", err)
	}

	// Create a stale backup file alongside the binary (at resolved path).
	backupPath := resolvedClaude + ".claude-proxy.bak"
	if err := os.WriteFile(backupPath, []byte("old-backup"), 0o600); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	// Create a patch history with an entry for this binary (at resolved path).
	store := newTempStore(t)
	historyStore, err2 := config.NewPatchHistoryStore(store.Path())
	if err2 != nil {
		t.Fatalf("new patch history store: %v", err2)
	}
	if err := historyStore.Update(func(h *config.PatchHistory) error {
		h.Upsert(config.PatchHistoryEntry{
			Path:          resolvedClaude,
			SpecsSHA256:   "specs-hash",
			PatchedSHA256: "patched-hash",
			ProxyVersion:  "0.0.38",
		})
		return nil
	}); err != nil {
		t.Fatalf("update history: %v", err)
	}

	// Run invalidation.
	invalidateExePatchState("claude", store.Path())

	// Backup should be removed.
	if _, err := os.Stat(backupPath); !os.IsNotExist(err) {
		t.Fatalf("expected backup to be removed, got err=%v", err)
	}

	// Patch history entry should be removed.
	history, err := historyStore.Load()
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	for _, entry := range history.Entries {
		if entry.Path == resolvedClaude {
			t.Fatalf("expected patch history entry to be removed, found: %+v", entry)
		}
	}
}

func testNativeExecutableStubBytes(goos string) []byte {
	switch strings.ToLower(strings.TrimSpace(goos)) {
	case "windows":
		return []byte{'M', 'Z', 0x90, 0x00, 't', 'e', 's', 't'}
	case "darwin":
		return []byte{0xcf, 0xfa, 0xed, 0xfe, 't', 'e', 's', 't'}
	default:
		return []byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00, 't', 'e', 's', 't'}
	}
}

func testNativeExecutableStubBytesWithMarker(goos string, marker string) []byte {
	stub := append([]byte{}, testNativeExecutableStubBytes(goos)...)
	return append(stub, []byte(marker)...)
}

func testClaudeLauncherName(goos string) string {
	if strings.EqualFold(strings.TrimSpace(goos), "windows") {
		return "claude.exe"
	}
	return "claude"
}

func withClaudeInstallGOOS(t *testing.T, goos string) {
	t.Helper()
	prevGOOS := claudeInstallGOOS
	claudeInstallGOOS = goos
	t.Cleanup(func() {
		claudeInstallGOOS = prevGOOS
	})
}
