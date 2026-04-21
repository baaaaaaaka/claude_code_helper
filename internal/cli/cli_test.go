package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestBuildVersion(t *testing.T) {
	prevVersion := version
	prevCommit := commit
	prevDate := date
	t.Cleanup(func() {
		version = prevVersion
		commit = prevCommit
		date = prevDate
	})

	version = "1.2.3"
	commit = "abc123"
	date = "2026-01-01"

	got := buildVersion()
	want := "1.2.3 (abc123) 2026-01-01"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestNewRootCmdFlags(t *testing.T) {
	cmd := newRootCmd()
	flag := cmd.PersistentFlags().Lookup("exe-patch-policy-settings")
	if flag == nil {
		t.Fatalf("expected exe-patch-policy-settings flag to exist")
	}
	if flag.DefValue != "true" {
		t.Fatalf("expected policySettings default true, got %q", flag.DefValue)
	}
	if cmd.PersistentFlags().Lookup("exe-patch-glibc-compat") == nil {
		t.Fatalf("expected exe-patch-glibc-compat flag to exist")
	}
	if cmd.PersistentFlags().Lookup("exe-patch-glibc-root") == nil {
		t.Fatalf("expected exe-patch-glibc-root flag to exist")
	}
}

func TestNewNotImplementedCmd(t *testing.T) {
	cmd := newNotImplementedCmd("foo", "bar")
	if cmd.Use != "foo" {
		t.Fatalf("expected Use to be %q, got %q", "foo", cmd.Use)
	}
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Fatalf("expected RunE to return error")
	}
}

func TestExecuteVersion(t *testing.T) {
	prevArgs := os.Args
	t.Cleanup(func() { os.Args = prevArgs })
	os.Args = []string{"claude-proxy", "--version"}
	if code := Execute(); code != 0 {
		t.Fatalf("expected Execute to return 0 for --version, got %d", code)
	}
}

func TestExecuteInvalidArgs(t *testing.T) {
	prevArgs := os.Args
	t.Cleanup(func() { os.Args = prevArgs })
	os.Args = []string{"claude-proxy", "--not-a-flag"}
	if code := Execute(); code != 1 {
		t.Fatalf("expected Execute to return 1 for invalid args, got %d", code)
	}
}

func TestNewRootCmdUnknownCommand(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{"definitely-not-a-command"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected unknown command to return error")
	}
}

// TestMapExecuteErrorNil verifies the happy path returns zero.
func TestMapExecuteErrorNil(t *testing.T) {
	var buf bytes.Buffer
	if code := mapExecuteError(nil, &buf); code != 0 {
		t.Fatalf("expected 0 for nil error, got %d", code)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected no stderr on success, got %q", buf.String())
	}
}

// TestMapExecuteErrorPropagatesExitCode verifies that an unwrapped
// *exec.ExitError propagates the child's exit code and prints NOTHING.
// This is the core of Bug A and Bug B's fix.
func TestMapExecuteErrorPropagatesExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows: relies on /bin/sh")
	}

	cmd := exec.Command("sh", "-c", "exit 42")
	runErr := cmd.Run()
	exitErr, ok := runErr.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected *exec.ExitError from sh -c exit 42, got %T: %v", runErr, runErr)
	}

	var buf bytes.Buffer
	code := mapExecuteError(exitErr, &buf)
	if code != 42 {
		t.Fatalf("expected exit code 42, got %d", code)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected NO stderr output for unwrapped ExitError, got %q", buf.String())
	}
}

// TestMapExecuteErrorPropagatesExitCodeOne verifies the `false` case: an
// unwrapped ExitError with code 1 is propagated silently. Before the fix,
// cobra would print "Error: exit status 1" to stderr.
func TestMapExecuteErrorPropagatesExitCodeOne(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows: relies on /bin/false")
	}

	cmd := exec.Command("sh", "-c", "exit 1")
	runErr := cmd.Run()
	exitErr, ok := runErr.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected *exec.ExitError, got %T: %v", runErr, runErr)
	}

	var buf bytes.Buffer
	code := mapExecuteError(exitErr, &buf)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if s := buf.String(); strings.Contains(s, "exit status") {
		t.Fatalf("stderr should not mention \"exit status\", got %q", s)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected NO stderr output, got %q", buf.String())
	}
}

// TestMapExecuteErrorWrappedExitErrorStillPrints verifies the crucial
// distinction: a WRAPPED *exec.ExitError (e.g. from runTargetOnceWithOptions'
// "proxy stack failed; terminated target: %w") is clp's own failure, must
// still print "Error: ..." and return exit code 1. errors.As would have
// mistakenly silenced these — the type assertion in mapExecuteError is why
// we don't use errors.As.
func TestMapExecuteErrorWrappedExitErrorStillPrints(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows: relies on /bin/sh")
	}

	cmd := exec.Command("sh", "-c", "exit 7")
	raw := cmd.Run()
	if _, ok := raw.(*exec.ExitError); !ok {
		t.Fatalf("setup: expected *exec.ExitError, got %T", raw)
	}
	wrapped := fmt.Errorf("proxy stack failed; terminated target: %w", raw)

	var buf bytes.Buffer
	code := mapExecuteError(wrapped, &buf)
	if code != 1 {
		t.Fatalf("expected wrapped clp failure to map to exit code 1, got %d", code)
	}
	got := buf.String()
	if !strings.Contains(got, "Error:") {
		t.Fatalf("expected fallback \"Error: ...\" line for wrapped error, got %q", got)
	}
	if !strings.Contains(got, "proxy stack failed") {
		t.Fatalf("expected fallback to include wrapped message, got %q", got)
	}
	// Sanity: confirm errors.As WOULD have matched — proving type assertion
	// was the correct choice.
	var asTarget *exec.ExitError
	if !errors.As(wrapped, &asTarget) {
		t.Fatalf("errors.As should have matched wrapped ExitError, guarding that type assertion is required")
	}
}

// TestMapExecuteErrorGenericError verifies a non-ExitError path prints
// "Error: ..." and returns 1.
func TestMapExecuteErrorGenericError(t *testing.T) {
	var buf bytes.Buffer
	code := mapExecuteError(errors.New("boom"), &buf)
	if code != 1 {
		t.Fatalf("expected exit code 1 for generic error, got %d", code)
	}
	got := buf.String()
	if !strings.Contains(got, "Error: boom") {
		t.Fatalf("expected fallback \"Error: boom\" line, got %q", got)
	}
}

// TestRootCmdSilencesErrors is a structural regression guard for Bug B:
// confirms that the root cobra command has SilenceErrors turned on so that
// cobra does NOT print "Error: exit status 1" on top of our own handling.
func TestRootCmdSilencesErrors(t *testing.T) {
	cmd := newRootCmd()
	if !cmd.SilenceErrors {
		t.Fatalf("expected root cmd to have SilenceErrors=true to prevent cobra from auto-printing child ExitErrors")
	}
	if !cmd.SilenceUsage {
		t.Fatalf("expected root cmd to keep SilenceUsage=true")
	}
}
