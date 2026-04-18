//go:build !windows

package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

func TestLinuxKernelMajorMinor(t *testing.T) {
	major, minor, ok := linuxKernelMajorMinor("4.18.0-553.el8.x86_64")
	if !ok {
		t.Fatalf("expected kernel release to parse")
	}
	if major != 4 || minor != 18 {
		t.Fatalf("expected 4.18, got %d.%d", major, minor)
	}
}

func TestBunLinuxKernelCompatibilityProblem(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only kernel compatibility check")
	}
	prevReadKernelReleaseFn := readLinuxKernelReleaseFn
	readLinuxKernelReleaseFn = func() ([]byte, error) { return []byte("4.18.0-553.el8.x86_64"), nil }
	t.Cleanup(func() { readLinuxKernelReleaseFn = prevReadKernelReleaseFn })

	reason, unsupported := bunLinuxKernelCompatibilityProblem()
	if !unsupported {
		t.Fatalf("expected kernel 4.18 to be unsupported")
	}
	if !strings.Contains(reason, "Linux kernel >= 5.1") {
		t.Fatalf("unexpected reason: %q", reason)
	}
}

func TestOverrideBunKernelCheckEnabled(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only kernel compatibility check")
	}

	prevReadKernelReleaseFn := readLinuxKernelReleaseFn
	readLinuxKernelReleaseFn = func() ([]byte, error) { return []byte("4.18.0-553.el8.x86_64"), nil }
	t.Cleanup(func() { readLinuxKernelReleaseFn = prevReadKernelReleaseFn })

	t.Setenv(overrideBunKernelCheckEnv, "")
	if !overrideBunKernelCheckEnabled("linux") {
		t.Fatalf("expected override to be enabled by default on unsupported linux kernel")
	}
	if claudeRequiresNPMInstall("linux") {
		t.Fatalf("expected unsupported linux kernel not to require npm install when override uses the default")
	}

	t.Setenv(overrideBunKernelCheckEnv, "true")
	if !overrideBunKernelCheckEnabled("linux") {
		t.Fatalf("expected override to be enabled on unsupported linux kernel")
	}
	if claudeRequiresNPMInstall("linux") {
		t.Fatalf("expected unsupported linux kernel not to require npm install when override is enabled")
	}

	t.Setenv(overrideBunKernelCheckEnv, "false")
	if overrideBunKernelCheckEnabled("linux") {
		t.Fatalf("expected override to be disabled when env is false")
	}
	if !claudeRequiresNPMInstall("linux") {
		t.Fatalf("expected unsupported linux kernel to require npm install when override is disabled")
	}
}

func TestRunClaudeInstallerPrefersOfficialInstallerOnUnsupportedKernelWhenOverrideEnabled(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only installer compatibility check")
	}
	withClaudeInstallGOOS(t, "linux")

	prevReadKernelReleaseFn := readLinuxKernelReleaseFn
	readLinuxKernelReleaseFn = func() ([]byte, error) { return []byte("4.18.0-553.el8.x86_64"), nil }
	t.Cleanup(func() { readLinuxKernelReleaseFn = prevReadKernelReleaseFn })

	dir := t.TempDir()
	home := t.TempDir()
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	npmMarker := filepath.Join(dir, "npm-ran")
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(claudeProxyHostIDEnv, "kernel-test-host")
	setStubPath(t, dir)

	bashBody := "#!/bin/sh\nmkdir -p \"" + filepath.Join(home, ".local", "bin") + "\"\nprintf '%s\\n' '#!/bin/sh' 'exit 0' > \"" + filepath.Join(home, ".local", "bin", "claude") + "\"\nchmod 755 \"" + filepath.Join(home, ".local", "bin", "claude") + "\"\nprintf 'official-ok\\n'\nexit 0\n"
	npmBody := "#!/bin/sh\nprintf 'npm-ran' > \"" + npmMarker + "\"\nexit 1\n"
	writeStub(t, dir, "bash", bashBody, "@echo off\r\nexit /b 0\r\n")
	writeStub(t, dir, "npm", npmBody, "@echo off\r\nexit /b 1\r\n")

	var out bytes.Buffer
	err := runClaudeInstaller(context.Background(), &out, installProxyOptions{})
	if err != nil {
		t.Fatalf("expected official installer to succeed, got %v", err)
	}

	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "claude")); err != nil {
		t.Fatalf("expected official installer to leave a native Claude launcher: %v", err)
	}
	if _, err := os.Stat(npmMarker); !os.IsNotExist(err) {
		t.Fatalf("expected npm fallback to stay unused, marker err=%v", err)
	}
	if strings.Contains(out.String(), "falling back to the npm distribution") {
		t.Fatalf("unexpected npm fallback log, got:\n%s", out.String())
	}
}

func TestRunClaudeInstallerFallsBackToNPMOnUnsupportedKernelWhenOfficialInstallerFails(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only installer compatibility check")
	}
	withClaudeInstallGOOS(t, "linux")

	prevReadKernelReleaseFn := readLinuxKernelReleaseFn
	readLinuxKernelReleaseFn = func() ([]byte, error) { return []byte("4.18.0-553.el8.x86_64"), nil }
	t.Cleanup(func() { readLinuxKernelReleaseFn = prevReadKernelReleaseFn })

	dir := t.TempDir()
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv(claudeProxyHostIDEnv, "kernel-test-host")
	setStubPath(t, dir)

	nodeBody := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo v20.11.1\n  exit 0\nfi\nif [ \"$2\" = \"--version\" ]; then\n  echo \"Claude Code 2.1.112\"\n  exit 0\nfi\nexit 0\n"
	npmBody := "#!/bin/sh\nprefix=\"${npm_config_prefix:-$NPM_CONFIG_PREFIX}\"\nif [ -z \"$prefix\" ]; then\n  echo missing prefix >&2\n  exit 1\nfi\nmkdir -p \"$prefix/lib/node_modules/@anthropic-ai/claude-code\"\nprintf '%s\\n' '#!/usr/bin/env node' > \"$prefix/lib/node_modules/@anthropic-ai/claude-code/cli.js\"\nchmod 755 \"$prefix/lib/node_modules/@anthropic-ai/claude-code/cli.js\"\nexit 0\n"
	writeStub(t, dir, "bash", "#!/bin/sh\nexit 23\n", "@echo off\r\nexit /b 23\r\n")
	writeStub(t, dir, "node", nodeBody, "@echo off\r\nif \"%~1\"==\"--version\" (\r\necho v20.11.1\r\nexit /b 0\r\n)\r\nif \"%~2\"==\"--version\" (\r\necho Claude Code 2.1.112\r\nexit /b 0\r\n)\r\nexit /b 0\r\n")
	writeStub(t, dir, "npm", npmBody, "@echo off\r\nexit /b 1\r\n")

	var out bytes.Buffer
	err := runClaudeInstaller(context.Background(), &out, installProxyOptions{})
	if err != nil {
		t.Fatalf("expected npm fallback install to succeed, got %v", err)
	}

	wrapper := filepath.Join(cacheRoot, "claude-proxy", "hosts", "kernel-test-host", claudeNPMInstallDirName, "claude")
	if !executableExists(wrapper) {
		t.Fatalf("expected npm wrapper at %s", wrapper)
	}
	versionOut, probeErr := runClaudeProbe(wrapper, "--version")
	if probeErr != nil {
		t.Fatalf("npm wrapper --version probe failed: %v\n%s", probeErr, versionOut)
	}
	if !strings.Contains(versionOut, "Claude Code 2.1.112") {
		t.Fatalf("unexpected npm wrapper output: %q", versionOut)
	}
	if !strings.Contains(out.String(), "falling back to the npm distribution") {
		t.Fatalf("expected npm fallback log, got:\n%s", out.String())
	}
}

func TestMaybePatchExecutableReturnsKernelCompatibilityErrorOnBunCrash(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only Bun kernel compatibility check")
	}
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	prevReadKernelReleaseFn := readLinuxKernelReleaseFn
	readLinuxKernelReleaseFn = func() ([]byte, error) { return []byte("4.18.0-553.el8.x86_64"), nil }
	t.Cleanup(func() { readLinuxKernelReleaseFn = prevReadKernelReleaseFn })

	dir := t.TempDir()
	writeClaudeVersionStub(t, dir, "Claude Code 1.2.3")
	setStubPath(t, dir)

	runClaudeProbeFn = func(path string, arg string) (string, error) {
		return "============================================================\n" +
			"Bun v1.3.13 (743d2a40) Linux x64 (baseline)\n" +
			"Linux Kernel v4.18.0 | glibc v2.28\n" +
			"panic(main thread): Bus error at address 0xE3ED3A5\n" +
			"oh no: Bun has crashed. This indicates a bug in Bun, not your code.\n\n" +
			"Illegal instruction (core dumped)\n", runSignalExit(syscall.SIGBUS)
	}

	outcome, err := maybePatchExecutable([]string{"claude"}, exePatchOptions{
		enabledFlag: true,
		glibcCompat: true,
	}, filepath.Join(dir, "config.json"), io.Discard)
	if err == nil {
		t.Fatalf("expected unsupported-kernel runtime error")
	}
	if !strings.Contains(err.Error(), "bundled Bun runtime requires Linux kernel >= 5.1") {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome != nil {
		t.Fatalf("expected nil outcome, got %#v", outcome)
	}
}

func TestMaybePatchExecutableSkipsBuiltInPatchingForNonNativeClaude(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)
	withClaudeInstallGOOS(t, "linux")

	cacheRoot := filepath.Join(t.TempDir(), "cache")
	hostID := "npm-wrapper-test"
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv(claudeProxyHostIDEnv, hostID)

	layout, ok := defaultManagedNPMClaudeLayout("linux", os.Getenv)
	if !ok {
		t.Fatalf("expected managed npm Claude layout")
	}
	if err := os.MkdirAll(filepath.Dir(layout.WrapperPath), 0o755); err != nil {
		t.Fatalf("mkdir npm wrapper dir: %v", err)
	}
	claudePath := layout.WrapperPath
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\necho \"Claude Code 2.1.112\"\n"), 0o700); err != nil {
		t.Fatalf("write claude wrapper: %v", err)
	}
	setStubPath(t, filepath.Dir(claudePath))

	patchExecutableFn = func(path string, specs []exePatchSpec, log io.Writer, preview bool, dryRun bool, historyStore *config.PatchHistoryStore, proxyVersion string) (*patchOutcome, error) {
		t.Fatalf("patchExecutable should not run for non-native Claude wrapper")
		return nil, nil
	}
	applyClaudeGlibcCompatPatchFn = func(path string, opts exePatchOptions, log io.Writer, dryRun bool, outcome *patchOutcome) (*patchOutcome, bool, error) {
		t.Fatalf("glibc compat patch should not run for non-native Claude wrapper")
		return nil, false, nil
	}

	outcome, err := maybePatchExecutable([]string{"claude"}, exePatchOptions{
		enabledFlag:    true,
		policySettings: true,
		glibcCompat:    true,
	}, filepath.Join(cacheRoot, "config.json"), io.Discard)
	if err != nil {
		t.Fatalf("maybePatchExecutable error: %v", err)
	}
	if outcome != nil {
		t.Fatalf("expected nil outcome for non-native Claude wrapper, got %#v", outcome)
	}
}
