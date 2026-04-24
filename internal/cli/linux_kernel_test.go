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

	t.Setenv(overrideBunKernelCheckEnv, "true")
	if !overrideBunKernelCheckEnabled("linux") {
		t.Fatalf("expected override to be enabled on unsupported linux kernel")
	}

	t.Setenv(overrideBunKernelCheckEnv, "false")
	if overrideBunKernelCheckEnabled("linux") {
		t.Fatalf("expected override to be disabled when env is false")
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
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(claudeProxyHostIDEnv, "kernel-test-host")
	setStubPath(t, dir)

	bashBody := "#!/bin/sh\nmkdir -p \"" + filepath.Join(home, ".local", "bin") + "\"\nprintf '%s\\n' '#!/bin/sh' 'exit 0' > \"" + filepath.Join(home, ".local", "bin", "claude") + "\"\nchmod 755 \"" + filepath.Join(home, ".local", "bin", "claude") + "\"\nprintf 'official-ok\\n'\nexit 0\n"
	writeStub(t, dir, "bash", bashBody, "@echo off\r\nexit /b 0\r\n")

	var out bytes.Buffer
	err := runClaudeInstaller(context.Background(), &out, installProxyOptions{})
	if err != nil {
		t.Fatalf("expected official installer to succeed, got %v", err)
	}

	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "claude")); err != nil {
		t.Fatalf("expected official installer to leave a native Claude launcher: %v", err)
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
