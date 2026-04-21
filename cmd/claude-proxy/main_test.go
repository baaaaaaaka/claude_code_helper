package main

import (
	"bytes"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestMainVersionExitZero(t *testing.T) {
	if os.Getenv("CLAUDE_PROXY_HELPER") == "1" {
		os.Args = []string{"claude-proxy", "--version"}
		main()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestMainVersionExitZero")
	cmd.Env = append(os.Environ(), "CLAUDE_PROXY_HELPER=1")
	if err := cmd.Run(); err != nil {
		t.Fatalf("expected exit 0, got error: %v", err)
	}
}

func TestMainInvalidArgsExitOne(t *testing.T) {
	if os.Getenv("CLAUDE_PROXY_HELPER_INVALID") == "1" {
		os.Args = []string{"claude-proxy", "--not-a-flag"}
		main()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestMainInvalidArgsExitOne")
	cmd.Env = append(os.Environ(), "CLAUDE_PROXY_HELPER_INVALID=1")
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected non-zero exit, got nil error")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("expected exit code 1, got %d", exitErr.ExitCode())
	}
}

// TestMainInvalidFlagStderr verifies that the post-fix error reporting on a
// cobra flag error (SilenceErrors:true + Execute's fallback printer) still
// emits an "Error: ..." line to stderr, and emits it exactly once (no double
// print from cobra + fallback). This is the regression guard for the
// TestMainInvalidArgsExitOne companion behavior.
func TestMainInvalidFlagStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows: relies on forked go test binary semantics")
	}

	if os.Getenv("CLAUDE_PROXY_HELPER_FLAG_STDERR") == "1" {
		os.Args = []string{"claude-proxy", "--not-a-flag"}
		main()
		return
	}

	var stderrBuf bytes.Buffer
	cmd := exec.Command(os.Args[0], "-test.run=TestMainInvalidFlagStderr")
	cmd.Env = append(os.Environ(), "CLAUDE_PROXY_HELPER_FLAG_STDERR=1")
	cmd.Stderr = &stderrBuf
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected non-zero exit, got nil error")
	}
	got := stderrBuf.String()
	// The fallback printer should emit exactly one "Error:" line.
	if count := strings.Count(got, "Error:"); count != 1 {
		t.Fatalf("expected exactly one \"Error:\" line in stderr, got %d:\n%s", count, got)
	}
	// Must not contain "exit status" — that's the silenced ExitError shape.
	if strings.Contains(got, "Error: exit status") {
		t.Fatalf("stderr should not contain \"Error: exit status\" noise, got:\n%s", got)
	}
}
