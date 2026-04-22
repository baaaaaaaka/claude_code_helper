package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const claudeInstallIntegrationTimeout = 8 * time.Minute

func TestClaudeInstallLaunchIntegration(t *testing.T) {
	requireClaudeInstallIntegration(t, "CLAUDE_INSTALL_TEST")

	homeDir := resolveClaudeInstallTestHome(t)
	localBinDir, expectWindowsGitBashBootstrap := configureClaudeInstallTestEnv(t, homeDir)
	if err := os.MkdirAll(localBinDir, 0o755); err != nil {
		t.Fatalf("mkdir local bin: %v", err)
	}
	applyInstallProxyEnv(t)

	startupMarkerPath := ""
	if runtime.GOOS != "windows" {
		startupMarkerPath = writeStartupMarkerFiles(t, homeDir)
	}

	if path, err := exec.LookPath("claude"); err == nil {
		t.Fatalf("expected claude to be absent from PATH before installation, found %q", path)
	}

	ctx, cancel := context.WithTimeout(context.Background(), claudeInstallIntegrationTimeout)
	defer cancel()

	var installLog bytes.Buffer
	path, err := ensureClaudeInstalled(ctx, "", &installLog, installProxyOptions{})
	if err != nil {
		t.Fatalf("ensureClaudeInstalled: %v\ninstaller output:\n%s", err, installLog.String())
	}
	if strings.TrimSpace(path) == "" {
		t.Fatalf("ensureClaudeInstalled returned empty path")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("installed claude stat: %v", err)
	}
	if info.IsDir() {
		t.Fatalf("installed claude path is a directory: %q", path)
	}

	out, err := runClaudeProbe(path, "--version")
	if err != nil {
		t.Fatalf("claude --version: %v\noutput: %s\ninstaller output:\n%s", err, out, installLog.String())
	}
	if got := extractVersion(out); got == "" {
		t.Fatalf("failed to parse claude version from output %q", strings.TrimSpace(out))
	}
	if expectWindowsGitBashBootstrap && !strings.Contains(installLog.String(), "installing a private Git for Windows runtime") {
		t.Fatalf("expected installer log to show Git Bash bootstrap on Windows\ninstaller output:\n%s", installLog.String())
	}

	if startupMarkerPath != "" {
		data, err := os.ReadFile(startupMarkerPath)
		if err != nil {
			if !os.IsNotExist(err) {
				t.Fatalf("read startup marker: %v", err)
			}
		} else if strings.TrimSpace(string(data)) != "" {
			t.Fatalf("installer unexpectedly sourced login startup files:\n%s", string(data))
		}
	}
}

func TestClaudeInstallForcedOldKernelNativeIntegration(t *testing.T) {
	requireClaudeInstallIntegration(t, "CLAUDE_INSTALL_TEST_FORCE_OLD_KERNEL_NATIVE")
	if runtime.GOOS != "linux" {
		t.Skip("forced old-kernel native install integration only applies on linux")
	}

	path, installLog := installNativeClaudeWithForcedOldKernel(t)
	if strings.Contains(strings.ToLower(installLog), "legacy compatibility launcher") {
		t.Fatalf("expected forced old-kernel native integration not to switch to the legacy compatibility launcher, got:\n%s", installLog)
	}
	if isManagedNPMClaudeLauncherPath(path, os.Getenv) {
		t.Fatalf("expected native Claude launcher on forced old-kernel path, got managed npm wrapper %q", path)
	}
}

func TestClaudeInstallForcedOldKernelNativeENOSYSIntegration(t *testing.T) {
	requireClaudeInstallIntegration(t, "CLAUDE_INSTALL_TEST_FORCE_OLD_KERNEL_NATIVE_ENOSYS")
	if runtime.GOOS != "linux" {
		t.Skip("forced old-kernel ENOSYS integration only applies on linux")
	}

	path, installLog := installNativeClaudeWithForcedOldKernel(t)
	if strings.Contains(strings.ToLower(installLog), "legacy compatibility launcher") {
		t.Fatalf("expected forced old-kernel native integration not to switch to the legacy compatibility launcher, got:\n%s", installLog)
	}
	if isManagedNPMClaudeLauncherPath(path, os.Getenv) {
		t.Fatalf("expected native Claude launcher on forced old-kernel path, got managed npm wrapper %q", path)
	}

	commands := []string{"--version", "--help"}
	sawInjectedENOSYS := false
	for _, arg := range commands {
		out, trace, err := runClaudeWithInjectedENOSYS(t, path, arg)
		if err != nil {
			t.Fatalf("Claude native ENOSYS probe %s failed: %v\noutput:\n%s\ntrace:\n%s", arg, err, out, trace)
		}
		if arg == "--version" {
			if got := extractVersion(out); got == "" {
				t.Fatalf("failed to parse Claude version from ENOSYS %s output %q", arg, strings.TrimSpace(out))
			}
		} else if strings.TrimSpace(out) == "" {
			t.Fatalf("expected non-empty output from ENOSYS %s probe", arg)
		}
		if traceShowsInjectedENOSYS(trace) {
			sawInjectedENOSYS = true
		}
	}
	if !sawInjectedENOSYS {
		t.Fatalf("expected strace ENOSYS injection to trigger for at least one native Claude probe")
	}
}

func TestClaudeInstallEL7RecoveryIntegration(t *testing.T) {
	requireClaudeInstallIntegration(t, "CLAUDE_INSTALL_TEST_EL7_GLIBC_RECOVERY")
	if runtime.GOOS != "linux" {
		t.Skip("EL7 glibc recovery integration only applies on linux")
	}
	if _, err := exec.LookPath("patchelf"); err != nil {
		t.Skip("patchelf required for EL7 glibc recovery integration")
	}
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("tar required for EL7 glibc recovery integration")
	}

	homeDir := resolveClaudeInstallTestHome(t)
	localBinDir, _ := configureClaudeInstallTestEnv(t, homeDir)
	if err := os.MkdirAll(localBinDir, 0o755); err != nil {
		t.Fatalf("mkdir local bin: %v", err)
	}
	applyInstallProxyEnv(t)
	cacheRoot := filepath.Join(homeDir, ".cache")
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	hostID := strings.TrimSpace(os.Getenv("CLAUDE_INSTALL_TEST_HOST_ID"))
	if hostID == "" {
		hostID = "install-recovery-test"
	}
	t.Setenv("CLAUDE_PROXY_HOST_ID", hostID)

	if path, err := exec.LookPath("claude"); err == nil {
		t.Fatalf("expected claude to be absent from PATH before installation, found %q", path)
	}

	ctx, cancel := context.WithTimeout(context.Background(), claudeInstallIntegrationTimeout)
	defer cancel()

	var installLog bytes.Buffer
	path, err := ensureClaudeInstalled(ctx, "", &installLog, installProxyOptions{})
	if err != nil {
		t.Fatalf("ensureClaudeInstalled: %v\ninstaller output:\n%s", err, installLog.String())
	}
	if !strings.Contains(installLog.String(), "prepared a claude-proxy-managed launcher") {
		t.Fatalf("expected EL7 recovery log, got:\n%s", installLog.String())
	}

	hostRoot, _, err := resolveClaudeProxyHostRoot()
	if err != nil {
		t.Fatalf("resolveClaudeProxyHostRoot: %v", err)
	}
	wantLauncher := filepath.Join(hostRoot, "install-recovery", "claude")
	if samePath(path, wantLauncher) == false {
		t.Fatalf("expected recovered launcher %q, got %q", wantLauncher, path)
	}

	sourcePath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve recovered launcher symlink: %v", err)
	}
	sourceSHABefore, err := hashFileSHA256(sourcePath)
	if err != nil {
		t.Fatalf("hash recovered source before patch: %v", err)
	}

	configPath := filepath.Join(homeDir, ".config", "claude-proxy", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	var patchLog bytes.Buffer
	outcome, err := maybePatchExecutableWithContext(ctx, []string{path}, exePatchOptions{
		enabledFlag:    true,
		policySettings: false,
		glibcCompat:    true,
	}, configPath, &patchLog)
	if err != nil {
		t.Fatalf("maybePatchExecutableWithContext: %v\ninstall log:\n%s\npatch log:\n%s", err, installLog.String(), patchLog.String())
	}

	out, err := runClaudeProbeOutcome(outcome, path, "--version")
	if err != nil {
		t.Fatalf("runClaudeProbeOutcome: %v\noutput:\n%s\ninstall log:\n%s\npatch log:\n%s", err, out, installLog.String(), patchLog.String())
	}
	if got := extractVersion(out); got == "" {
		t.Fatalf("failed to parse claude version from output %q", strings.TrimSpace(out))
	}
	if strings.Contains(out, "GLIBC_") {
		t.Fatalf("unexpected GLIBC failure after recovery patch:\n%s", out)
	}

	sourceSHAAfter, err := hashFileSHA256(sourcePath)
	if err != nil {
		t.Fatalf("hash recovered source after patch: %v", err)
	}
	if sourceSHABefore != sourceSHAAfter {
		t.Fatalf("expected recovered source binary to stay unchanged, before=%s after=%s", sourceSHABefore, sourceSHAAfter)
	}
	if outcome == nil || strings.TrimSpace(outcome.TargetPath) == "" {
		t.Fatalf("expected patch outcome with target path")
	}
	if samePath(outcome.TargetPath, sourcePath) {
		t.Fatalf("expected glibc compat launch path to differ from recovered source path %q", sourcePath)
	}
}

func TestClaudeInstallNPMFallbackIntegration(t *testing.T) {
	requireClaudeInstallIntegration(t, "CLAUDE_INSTALL_TEST_NPM_FALLBACK")
	runClaudeInstallNPMFallbackIntegration(t, false)
}

func TestClaudeInstallNPMFallbackSystemNodeIntegration(t *testing.T) {
	requireClaudeInstallIntegration(t, "CLAUDE_INSTALL_TEST_NPM_FALLBACK_SYSTEM_NODE")
	runClaudeInstallNPMFallbackIntegration(t, true)
}

func TestClaudeInstallNPMFallbackNodeWithoutNPMIntegration(t *testing.T) {
	requireClaudeInstallIntegration(t, "CLAUDE_INSTALL_TEST_NPM_FALLBACK_NODE_WITHOUT_NPM")
	runClaudeInstallNPMFallbackIntegration(t, false)
}

func TestClaudeInstallReusesLegacyCompatibilityLauncherAfterBrokenNativeIntegration(t *testing.T) {
	requireClaudeInstallIntegration(t, "CLAUDE_INSTALL_TEST_REUSE_LEGACY_LAUNCHER")
	if runtime.GOOS != "linux" {
		t.Skip("legacy launcher reuse integration only applies on linux")
	}

	homeDir := resolveClaudeInstallTestHome(t)
	localBinDir, _ := configureClaudeInstallTestEnv(t, homeDir)
	if err := os.MkdirAll(localBinDir, 0o755); err != nil {
		t.Fatalf("mkdir local bin: %v", err)
	}
	applyInstallProxyEnv(t)
	cacheRoot := filepath.Join(homeDir, ".cache")
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	hostID := strings.TrimSpace(os.Getenv("CLAUDE_INSTALL_TEST_HOST_ID"))
	if hostID == "" {
		hostID = "reuse-legacy-launcher-test"
	}
	t.Setenv("CLAUDE_PROXY_HOST_ID", hostID)
	t.Setenv(overrideBunKernelCheckEnv, "")

	prevReadKernelReleaseFn := readLinuxKernelReleaseFn
	readLinuxKernelReleaseFn = func() ([]byte, error) { return []byte("4.18.0-553.el8.x86_64"), nil }
	t.Cleanup(func() { readLinuxKernelReleaseFn = prevReadKernelReleaseFn })

	brokenNative := filepath.Join(localBinDir, "claude")
	if err := os.WriteFile(brokenNative, []byte("#!/bin/sh\necho 'broken native claude' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatalf("write broken native claude: %v", err)
	}

	layout, ok := defaultManagedNPMClaudeLayout("linux", os.Getenv)
	if !ok {
		t.Fatalf("expected managed npm layout")
	}
	writeManagedLegacyClaudeLauncherIntegrationFixture(t, layout, "2.1.112")

	ctx, cancel := context.WithTimeout(context.Background(), claudeInstallIntegrationTimeout)
	defer cancel()

	var installLog bytes.Buffer
	path, err := ensureClaudeInstalled(ctx, "", &installLog, installProxyOptions{})
	if err != nil {
		t.Fatalf("ensureClaudeInstalled: %v\ninstaller output:\n%s", err, installLog.String())
	}
	if !samePath(path, layout.WrapperPath) {
		t.Fatalf("expected managed legacy launcher %q, got %q", layout.WrapperPath, path)
	}
	if strings.Contains(strings.ToLower(installLog.String()), "claude not found; installing") {
		t.Fatalf("expected existing launcher reuse without installer, got:\n%s", installLog.String())
	}
	if !strings.Contains(installLog.String(), "trying the next managed launcher") {
		t.Fatalf("expected broken native skip log, got:\n%s", installLog.String())
	}

	out, err := runClaudeProbe(path, "--version")
	if err != nil {
		t.Fatalf("claude --version via reused legacy launcher: %v\noutput: %s\ninstaller output:\n%s", err, out, installLog.String())
	}
	if got := extractVersion(out); got == "" {
		t.Fatalf("failed to parse claude version from reused legacy launcher output %q", strings.TrimSpace(out))
	}
}

func TestClaudeInstallReusesRecoveredLauncherAfterBrokenNativeIntegration(t *testing.T) {
	requireClaudeInstallIntegration(t, "CLAUDE_INSTALL_TEST_REUSE_RECOVERY_LAUNCHER")
	if runtime.GOOS != "linux" {
		t.Skip("recovered launcher reuse integration only applies on linux")
	}

	homeDir := resolveClaudeInstallTestHome(t)
	localBinDir, _ := configureClaudeInstallTestEnv(t, homeDir)
	if err := os.MkdirAll(localBinDir, 0o755); err != nil {
		t.Fatalf("mkdir local bin: %v", err)
	}
	applyInstallProxyEnv(t)
	cacheRoot := filepath.Join(homeDir, ".cache")
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	hostID := strings.TrimSpace(os.Getenv("CLAUDE_INSTALL_TEST_HOST_ID"))
	if hostID == "" {
		hostID = "reuse-recovery-launcher-test"
	}
	t.Setenv("CLAUDE_PROXY_HOST_ID", hostID)
	t.Setenv(overrideBunKernelCheckEnv, "")

	prevReadKernelReleaseFn := readLinuxKernelReleaseFn
	readLinuxKernelReleaseFn = func() ([]byte, error) { return []byte("4.18.0-553.el8.x86_64"), nil }
	t.Cleanup(func() { readLinuxKernelReleaseFn = prevReadKernelReleaseFn })

	brokenNative := filepath.Join(localBinDir, "claude")
	if err := os.WriteFile(brokenNative, []byte("#!/bin/sh\necho 'broken native claude' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatalf("write broken native claude: %v", err)
	}

	recoveryLauncher := defaultRecoveredClaudeLauncherCandidate("linux", os.Getenv)
	if strings.TrimSpace(recoveryLauncher) == "" {
		t.Fatalf("expected recovered launcher candidate")
	}
	if err := os.MkdirAll(filepath.Dir(recoveryLauncher), 0o755); err != nil {
		t.Fatalf("mkdir recovery launcher dir: %v", err)
	}
	recoveryScript := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo 'Claude Code 2.1.112'\n  exit 0\nfi\nexit 0\n"
	if err := os.WriteFile(recoveryLauncher, []byte(recoveryScript), 0o700); err != nil {
		t.Fatalf("write recovery launcher: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), claudeInstallIntegrationTimeout)
	defer cancel()

	var installLog bytes.Buffer
	path, err := ensureClaudeInstalled(ctx, "", &installLog, installProxyOptions{})
	if err != nil {
		t.Fatalf("ensureClaudeInstalled: %v\ninstaller output:\n%s", err, installLog.String())
	}
	if !samePath(path, recoveryLauncher) {
		t.Fatalf("expected recovered launcher %q, got %q", recoveryLauncher, path)
	}
	if strings.Contains(strings.ToLower(installLog.String()), "claude not found; installing") {
		t.Fatalf("expected existing launcher reuse without installer, got:\n%s", installLog.String())
	}
	if !strings.Contains(installLog.String(), "trying the next managed launcher") {
		t.Fatalf("expected broken native skip log, got:\n%s", installLog.String())
	}

	out, err := runClaudeProbe(path, "--version")
	if err != nil {
		t.Fatalf("claude --version via reused recovery launcher: %v\noutput: %s\ninstaller output:\n%s", err, out, installLog.String())
	}
	if got := extractVersion(out); got == "" {
		t.Fatalf("failed to parse claude version from reused recovery launcher output %q", strings.TrimSpace(out))
	}
}

func runClaudeInstallNPMFallbackIntegration(t *testing.T, requireSystemNode bool) {
	if runtime.GOOS != "linux" {
		t.Skip("npm fallback integration only applies on linux")
	}
	if glibcCompatHostEligibleFn() {
		if _, err := exec.LookPath("patchelf"); err != nil {
			t.Skip("patchelf is required when npm fallback needs glibc compat")
		}
		if _, err := exec.LookPath("tar"); err != nil {
			t.Skip("tar is required when npm fallback needs glibc compat")
		}
	}

	homeDir := resolveClaudeInstallTestHome(t)
	localBinDir, _ := configureClaudeInstallTestEnv(t, homeDir)
	if err := os.MkdirAll(localBinDir, 0o755); err != nil {
		t.Fatalf("mkdir local bin: %v", err)
	}
	applyInstallProxyEnv(t)
	cacheRoot := filepath.Join(homeDir, ".cache")
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	ambientPrefix := filepath.Join(homeDir, "ambient-npm-prefix")
	t.Setenv("npm_config_prefix", ambientPrefix)
	t.Setenv("NPM_CONFIG_PREFIX", ambientPrefix)
	hostID := strings.TrimSpace(os.Getenv("CLAUDE_INSTALL_TEST_HOST_ID"))
	if hostID == "" {
		hostID = "npm-fallback-test"
	}
	t.Setenv("CLAUDE_PROXY_HOST_ID", hostID)
	t.Setenv(overrideBunKernelCheckEnv, "false")

	prevReadKernelReleaseFn := readLinuxKernelReleaseFn
	readLinuxKernelReleaseFn = func() ([]byte, error) { return []byte("4.18.0-553.el8.x86_64"), nil }
	t.Cleanup(func() { readLinuxKernelReleaseFn = prevReadKernelReleaseFn })

	if os.Getenv("CLAUDE_INSTALL_TEST_NPM_FALLBACK_NODE_WITHOUT_NPM") == "1" {
		if _, err := exec.LookPath("node"); err != nil {
			t.Fatalf("expected node in PATH for node-without-npm integration: %v", err)
		}
		if npmPath, err := exec.LookPath("npm"); err == nil {
			t.Fatalf("expected npm to be absent from PATH for node-without-npm integration, found %q", npmPath)
		}
	}

	if path, err := exec.LookPath("claude"); err == nil {
		t.Fatalf("expected claude to be absent from PATH before installation, found %q", path)
	}

	ctx, cancel := context.WithTimeout(context.Background(), claudeInstallIntegrationTimeout)
	defer cancel()

	var installLog bytes.Buffer
	path, err := ensureClaudeInstalled(ctx, "", &installLog, installProxyOptions{})
	if err != nil {
		t.Fatalf("ensureClaudeInstalled: %v\ninstaller output:\n%s", err, installLog.String())
	}
	if strings.TrimSpace(path) == "" {
		t.Fatalf("ensureClaudeInstalled returned empty path")
	}

	hostRoot, _, err := resolveClaudeProxyHostRoot()
	if err != nil {
		t.Fatalf("resolveClaudeProxyHostRoot: %v", err)
	}
	wantLauncher := filepath.Join(hostRoot, claudeNPMInstallDirName, "claude")
	if !samePath(path, wantLauncher) {
		t.Fatalf("expected npm fallback launcher %q, got %q", wantLauncher, path)
	}

	out, err := runClaudeProbe(path, "--version")
	if err != nil {
		t.Fatalf("claude --version via npm fallback: %v\noutput: %s\ninstaller output:\n%s", err, out, installLog.String())
	}
	if got := extractVersion(out); got == "" {
		t.Fatalf("failed to parse claude version from npm fallback output %q", strings.TrimSpace(out))
	}
	layout, ok := defaultManagedNPMClaudeLayout("linux", os.Getenv)
	if !ok {
		t.Fatalf("expected managed npm layout")
	}
	if !samePath(layout.WrapperPath, path) {
		t.Fatalf("expected wrapper path %q, got %q", layout.WrapperPath, path)
	}
	if fileExists(filepath.Join(ambientPrefix, "lib", "node_modules", "@anthropic-ai", "claude-code", "cli.js")) {
		t.Fatalf("ambient npm prefix unexpectedly received the Claude Code install: %s", ambientPrefix)
	}
	if !fileExists(layout.CLIPath) {
		t.Fatalf("expected managed npm CLI at %s", layout.CLIPath)
	}
	if requireSystemNode {
		systemNodePath, err := exec.LookPath("node")
		if err != nil {
			t.Fatalf("expected system node in PATH for system-node integration: %v", err)
		}
		nodeWrapperArgs, ok := readManagedNPMExecWrapperArgs(layout.NodePath)
		if !ok || len(nodeWrapperArgs) == 0 {
			t.Fatalf("expected managed npm node launcher args in %s", layout.NodePath)
		}
		systemNodeReferenced := false
		for _, arg := range nodeWrapperArgs {
			if samePath(arg, systemNodePath) {
				systemNodeReferenced = true
				break
			}
		}
		if !systemNodeReferenced && !strings.Contains(installLog.String(), "Node.js at "+systemNodePath+" needs glibc compat") {
			t.Fatalf("expected managed npm node launcher args %q to reference system node %q", nodeWrapperArgs, systemNodePath)
		}
		if fileExists(layout.RuntimeNodePath) {
			t.Fatalf("did not expect private Node runtime at %s when system node is available", layout.RuntimeNodePath)
		}
		if strings.Contains(strings.ToLower(installLog.String()), "retrying with a claude-proxy-managed node.js runtime") {
			t.Fatalf("unexpected private runtime retry in system-node integration log:\n%s", installLog.String())
		}
	} else if os.Getenv("CLAUDE_INSTALL_TEST_NPM_FALLBACK_NODE_WITHOUT_NPM") == "1" {
		if !fileExists(layout.RuntimeNodePath) {
			t.Fatalf("expected private Node runtime at %s when npm is missing from PATH", layout.RuntimeNodePath)
		}
		if !strings.Contains(strings.ToLower(installLog.String()), "npm was not found in path") {
			t.Fatalf("expected missing npm log in node-without-npm integration output, got:\n%s", installLog.String())
		}
	}
	if !strings.Contains(installLog.String(), "legacy compatibility launcher") {
		t.Fatalf("expected legacy compatibility launcher log, got:\n%s", installLog.String())
	}
}

func requireClaudeInstallIntegration(t *testing.T, envName string) {
	t.Helper()
	if os.Getenv(envName) != "1" {
		t.Skipf("set %s=1 to run integration test", envName)
	}
	if os.Getenv("CI") != "true" && os.Getenv("CLAUDE_INSTALL_TEST_ALLOW_LOCAL") != "1" {
		t.Skip("integration test runs only in CI; set CLAUDE_INSTALL_TEST_ALLOW_LOCAL=1 for local runs")
	}
}

func resolveClaudeInstallTestHome(t *testing.T) string {
	t.Helper()

	homeDir := strings.TrimSpace(os.Getenv("CLAUDE_INSTALL_TEST_HOME"))
	if homeDir == "" {
		return t.TempDir()
	}
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("mkdir install test home %s: %v", homeDir, err)
	}
	return homeDir
}

func configureClaudeInstallTestEnv(t *testing.T, homeDir string) (string, bool) {
	t.Helper()

	localBinDir := filepath.Join(homeDir, ".local", "bin")
	configHome := filepath.Join(homeDir, ".config")
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv("XDG_CONFIG_HOME", configHome)

	expectWindowsGitBashBootstrap := false
	pathValue := buildClaudeInstallTestPath(os.Getenv("PATH"), localBinDir, installTestPathOptions{
		PreseedLocalBin: os.Getenv("CLAUDE_INSTALL_TEST_NO_PATH_PRESEED") != "1",
	})

	if runtime.GOOS == "windows" {
		appData := filepath.Join(homeDir, "AppData", "Roaming")
		localAppData := filepath.Join(homeDir, "AppData", "Local")
		for _, dir := range []string{configHome, appData, localAppData, localBinDir} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", dir, err)
			}
		}
		t.Setenv("APPDATA", appData)
		t.Setenv("LOCALAPPDATA", localAppData)

		if os.Getenv("CLAUDE_INSTALL_TEST_HIDE_WINDOWS_GIT_BASH") == "1" {
			pathValue = buildClaudeInstallTestPath(pathValue, localBinDir, installTestPathOptions{
				PreseedLocalBin: false,
				StripWindowsGit: true,
			})
			t.Setenv("CLAUDE_CODE_GIT_BASH_PATH", filepath.Join(homeDir, "missing-git", "bash.exe"))
			t.Setenv("ProgramFiles", filepath.Join(homeDir, "ProgramFiles"))
			t.Setenv("ProgramW6432", filepath.Join(homeDir, "ProgramFiles"))
			t.Setenv("ProgramFiles(x86)", filepath.Join(homeDir, "Program Files (x86)"))
			t.Setenv("PATH", pathValue)
			expectWindowsGitBashBootstrap = os.Getenv("CLAUDE_INSTALL_TEST_REQUIRE_WINDOWS_GIT_BASH_BOOTSTRAP") == "1"
			if path := findWindowsGitBashPath(os.Getenv); path != "" {
				t.Fatalf("expected Git Bash to be hidden for install test, found %q", path)
			}
			if os.Getenv("CLAUDE_INSTALL_TEST_NO_PATH_PRESEED") != "1" {
				pathValue = prependPathEntry(localBinDir, pathValue)
			}
		}
	} else if err := os.MkdirAll(localBinDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", localBinDir, err)
	}

	t.Setenv("PATH", pathValue)
	return localBinDir, expectWindowsGitBashBootstrap
}

func installNativeClaudeWithForcedOldKernel(t *testing.T) (string, string) {
	t.Helper()

	homeDir := resolveClaudeInstallTestHome(t)
	localBinDir, _ := configureClaudeInstallTestEnv(t, homeDir)
	if err := os.MkdirAll(localBinDir, 0o755); err != nil {
		t.Fatalf("mkdir local bin: %v", err)
	}
	applyInstallProxyEnv(t)
	cacheRoot := filepath.Join(homeDir, ".cache")
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	hostID := strings.TrimSpace(os.Getenv("CLAUDE_INSTALL_TEST_HOST_ID"))
	if hostID == "" {
		hostID = "forced-old-kernel-native-test"
	}
	t.Setenv("CLAUDE_PROXY_HOST_ID", hostID)
	t.Setenv(overrideBunKernelCheckEnv, "")

	prevReadKernelReleaseFn := readLinuxKernelReleaseFn
	readLinuxKernelReleaseFn = func() ([]byte, error) { return []byte("4.18.0-553.el8.x86_64"), nil }
	t.Cleanup(func() { readLinuxKernelReleaseFn = prevReadKernelReleaseFn })

	if path, err := exec.LookPath("claude"); err == nil {
		t.Fatalf("expected claude to be absent from PATH before installation, found %q", path)
	}

	ctx, cancel := context.WithTimeout(context.Background(), claudeInstallIntegrationTimeout)
	defer cancel()

	var installLog bytes.Buffer
	path, err := ensureClaudeInstalled(ctx, "", &installLog, installProxyOptions{})
	if err != nil {
		t.Fatalf("ensureClaudeInstalled on forced old kernel: %v\ninstaller output:\n%s", err, installLog.String())
	}
	if strings.TrimSpace(path) == "" {
		t.Fatalf("ensureClaudeInstalled returned empty path on forced old kernel")
	}

	layout, ok := defaultManagedNPMClaudeLayout("linux", os.Getenv)
	if ok && samePath(path, layout.WrapperPath) {
		t.Fatalf("expected forced old-kernel native path, got managed npm wrapper %q", path)
	}
	if isManagedNPMClaudeLauncherPath(path, os.Getenv) {
		t.Fatalf("expected forced old-kernel native path, got managed npm wrapper %q", path)
	}

	out, err := runClaudeProbe(path, "--version")
	if err != nil {
		t.Fatalf("claude --version on forced old kernel: %v\noutput: %s\ninstaller output:\n%s", err, out, installLog.String())
	}
	if got := extractVersion(out); got == "" {
		t.Fatalf("failed to parse claude version from forced old-kernel output %q", strings.TrimSpace(out))
	}

	return path, installLog.String()
}

func runClaudeWithInjectedENOSYS(t *testing.T, path string, arg string) (string, string, error) {
	t.Helper()

	stracePath, err := exec.LookPath("strace")
	if err != nil {
		t.Skip("strace is required for ENOSYS integration")
	}

	tracePath := filepath.Join(t.TempDir(), "strace.log")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmdArgs := []string{
		"-f",
		"-o", tracePath,
		"-e", "inject=clone3:error=ENOSYS",
		"-e", "inject=close_range:error=ENOSYS",
		"-e", "inject=pidfd_open:error=ENOSYS",
		"-e", "inject=openat2:error=ENOSYS",
		"-e", "inject=futex_waitv:error=ENOSYS",
		path,
		arg,
	}
	cmd := exec.CommandContext(ctx, stracePath, cmdArgs...)
	out, runErr := cmd.CombinedOutput()
	traceData, traceErr := os.ReadFile(tracePath)
	if traceErr != nil && runErr == nil {
		runErr = traceErr
	}
	return string(out), string(traceData), runErr
}

func traceShowsInjectedENOSYS(trace string) bool {
	if !strings.Contains(trace, "ENOSYS") {
		return false
	}
	for _, syscall := range []string{"clone3(", "close_range(", "pidfd_open(", "openat2(", "futex_waitv("} {
		if strings.Contains(trace, syscall) && strings.Contains(trace, "ENOSYS") {
			return true
		}
	}
	return false
}

type installTestPathOptions struct {
	PreseedLocalBin bool
	StripWindowsGit bool
}

func buildClaudeInstallTestPath(currentPath string, localBinDir string, opts installTestPathOptions) string {
	parts := filepath.SplitList(currentPath)
	filtered := make([]string, 0, len(parts)+1)
	if opts.PreseedLocalBin {
		filtered = append(filtered, localBinDir)
	}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if samePath(part, localBinDir) {
			continue
		}
		if hasClaudeBinary(part) {
			continue
		}
		if opts.StripWindowsGit && isWindowsGitPath(part) {
			continue
		}
		filtered = append(filtered, part)
	}
	return strings.Join(filtered, string(os.PathListSeparator))
}

func prependPathEntry(entry string, currentPath string) string {
	if strings.TrimSpace(currentPath) == "" {
		return entry
	}
	return entry + string(os.PathListSeparator) + currentPath
}

func hasClaudeBinary(dir string) bool {
	names := []string{"claude"}
	if runtime.GOOS == "windows" {
		names = []string{"claude.exe", "claude.cmd", "claude.bat", "claude.com", "claude"}
	}
	for _, name := range names {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func isWindowsGitPath(dir string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	clean := strings.ToLower(filepath.Clean(dir))
	if strings.Contains(clean, `\git\`) || strings.HasSuffix(clean, `\git`) {
		return true
	}
	base := strings.ToLower(filepath.Base(clean))
	switch base {
	case "git", "mingw64", "usr":
		return true
	case "bin", "cmd":
		parent := strings.ToLower(filepath.Base(filepath.Dir(clean)))
		return parent == "git" || parent == "usr"
	default:
		return false
	}
}

func samePath(a string, b string) bool {
	cleanA := filepath.Clean(a)
	cleanB := filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(cleanA, cleanB)
	}
	return cleanA == cleanB
}

func writeStartupMarkerFiles(t *testing.T, homeDir string) string {
	t.Helper()
	markerPath := filepath.Join(homeDir, ".claude_install_startup_marker")
	escapedMarker := strings.ReplaceAll(markerPath, "\"", "\\\"")
	payload := "echo startup_sourced >> \"" + escapedMarker + "\"\n"
	for _, name := range []string{".bash_profile", ".profile"} {
		path := filepath.Join(homeDir, name)
		if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return markerPath
}

func applyInstallProxyEnv(t *testing.T) {
	t.Helper()
	proxyURL := strings.TrimSpace(os.Getenv("CLAUDE_INSTALL_TEST_PROXY_URL"))
	if proxyURL == "" {
		return
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		t.Setenv(key, proxyURL)
	}
	mergedNoProxy := mergeNoProxyForInstallTest(
		firstNonEmptyNonBlank(os.Getenv("NO_PROXY"), os.Getenv("no_proxy")),
		[]string{"localhost", "127.0.0.1", "::1"},
	)
	t.Setenv("NO_PROXY", mergedNoProxy)
	t.Setenv("no_proxy", mergedNoProxy)
}

func writeManagedLegacyClaudeLauncherIntegrationFixture(t *testing.T, layout managedNPMClaudeLayout, version string) {
	t.Helper()

	nodeTarget := filepath.Join(layout.RootDir, "integration-node", "node")
	if err := os.MkdirAll(filepath.Dir(nodeTarget), 0o755); err != nil {
		t.Fatalf("mkdir node target dir: %v", err)
	}
	nodeScript := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo 'v18.20.8'\n  exit 0\nfi\nif [ \"$2\" = \"--version\" ]; then\n  echo 'Claude Code " + version + "'\n  exit 0\nfi\nexit 0\n"
	if err := os.WriteFile(nodeTarget, []byte(nodeScript), 0o700); err != nil {
		t.Fatalf("write node target: %v", err)
	}
	if err := os.MkdirAll(layout.LauncherDir, 0o755); err != nil {
		t.Fatalf("mkdir launcher dir: %v", err)
	}
	if err := writeManagedNPMExecWrapper(layout.NodePath, []string{nodeTarget}); err != nil {
		t.Fatalf("write managed npm node launcher: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(layout.CLIPath), 0o755); err != nil {
		t.Fatalf("mkdir CLI dir: %v", err)
	}
	if err := os.WriteFile(layout.CLIPath, []byte("#!/usr/bin/env node\n"), 0o755); err != nil {
		t.Fatalf("write CLI path: %v", err)
	}
	if err := writeManagedNPMExecWrapper(layout.WrapperPath, []string{layout.NodePath, layout.CLIPath}); err != nil {
		t.Fatalf("write managed npm Claude wrapper: %v", err)
	}
}

func firstNonEmptyNonBlank(a string, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func mergeNoProxyForInstallTest(existing string, required []string) string {
	seen := map[string]bool{}
	merged := make([]string, 0, len(required)+1)
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		key := strings.ToLower(v)
		if seen[key] {
			return
		}
		seen[key] = true
		merged = append(merged, v)
	}
	for _, part := range strings.Split(existing, ",") {
		add(part)
	}
	for _, item := range required {
		add(item)
	}
	return strings.Join(merged, ",")
}
