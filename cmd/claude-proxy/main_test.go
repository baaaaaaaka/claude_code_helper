package main

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

// TestMainRunEmptyStdinTakesDefaultAndPropagatesExit is an end-to-end guard
// against the class of regression reviewers caught after the first fix batch.
// With an empty config and stdin closed, `clp run -- sh -c "exit 42"` must:
//   - take the "no proxy" default (instead of surfacing io.EOF from the
//     interactive proxy prompt), AND
//   - propagate the child's exit code 42 (instead of collapsing to 1), AND
//   - emit no "Error: exit status" / "Error: EOF" noise to stderr.
//
// This combines Fix 1 (EOF default acceptance), Fix 2 (exit code passthrough)
// and Fix 3 (stderr silence) into one scripted invocation. A failure here
// means one of those three has regressed.
func TestMainRunEmptyStdinTakesDefaultAndPropagatesExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs /bin/sh to invoke `sh -c 'exit 42'`")
	}

	const envVar = "CLP_HELPER_RUN_EMPTY_STDIN_CFG"
	if cfg := os.Getenv(envVar); cfg != "" {
		os.Args = []string{"claude-proxy", "--config", cfg, "run", "--", "sh", "-c", "exit 42"}
		main()
		return
	}

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"version":1,"profiles":[],"instances":[]}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stderr bytes.Buffer
	cmd := exec.Command(os.Args[0], "-test.run=TestMainRunEmptyStdinTakesDefaultAndPropagatesExit")
	cmd.Env = append(os.Environ(), envVar+"="+cfgPath)
	cmd.Stdin = strings.NewReader("")
	cmd.Stderr = &stderr
	err := cmd.Run()

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected *exec.ExitError, got %T: %v. stderr:\n%s", err, err, stderr.String())
	}
	if exitErr.ExitCode() != 42 {
		t.Fatalf("expected exit code 42, got %d. stderr:\n%s", exitErr.ExitCode(), stderr.String())
	}
	if s := stderr.String(); strings.Contains(s, "Error: exit status") {
		t.Fatalf("stderr should not contain \"Error: exit status\" noise, got:\n%s", s)
	}
	if s := stderr.String(); strings.Contains(s, "Error: EOF") {
		t.Fatalf("stderr should not contain \"Error: EOF\" (empty stdin must fall back to default), got:\n%s", s)
	}
}

// TestMainInitWithEmptyStdinFailsFast is a timeout-backed guard against the
// original infinite-loop bug: `echo "" | clp init` used to spin forever on
// the first promptRequired("SSH host") because ReadString's EOF was silently
// discarded. The subprocess must exit within the context deadline; if it
// doesn't, the fix has regressed and the test FAILS explicitly on hang
// rather than just timing out.
func TestMainInitWithEmptyStdinFailsFast(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows: relies on forked go test binary semantics")
	}

	const envVar = "CLP_HELPER_INIT_EMPTY_STDIN_CFG"
	if cfg := os.Getenv(envVar); cfg != "" {
		os.Args = []string{"claude-proxy", "--config", cfg, "init"}
		main()
		return
	}

	cfgPath := filepath.Join(t.TempDir(), "config.json")

	// 2s is plenty for a clean EOF exit; if the bug regresses, the process
	// would spin forever. ExecCommandContext kills on deadline exceeded.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestMainInitWithEmptyStdinFailsFast")
	cmd.Env = append(os.Environ(), envVar+"="+cfgPath)
	cmd.Stdin = strings.NewReader("")
	cmd.Stderr = &stderr
	err := cmd.Run()

	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("clp init hung on empty stdin (>=2s) — infinite-loop fix has regressed.\nstderr:\n%s", stderr.String())
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected *exec.ExitError, got %T: %v. stderr:\n%s", err, err, stderr.String())
	}
	if exitErr.ExitCode() == 0 {
		t.Fatalf("expected non-zero exit, got 0. stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "EOF") {
		t.Fatalf("expected stderr to mention EOF, got:\n%s", stderr.String())
	}
}
