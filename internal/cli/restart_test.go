package cli

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/baaaaaaaka/claude_code_helper/internal/update"
)

func TestHandleUpdateAndRestartError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd := &cobra.Command{}
	cmd.SetContext(ctx)
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)

	if err := handleUpdateAndRestart(ctx, cmd); err == nil {
		t.Fatalf("expected update error")
	}
	if !strings.Contains(errBuf.String(), "Upgrade failed:") {
		t.Fatalf("expected error output, got %q", errBuf.String())
	}
}

func TestHandleUpdateAndRestartRestartRequired(t *testing.T) {
	prev := performUpdate
	t.Cleanup(func() { performUpdate = prev })
	performUpdate = func(ctx context.Context, opts update.UpdateOptions) (update.ApplyResult, error) {
		return update.ApplyResult{Version: "1.2.3", RestartRequired: true}, nil
	}

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := handleUpdateAndRestart(context.Background(), cmd); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "Update scheduled for v1.2.3") {
		t.Fatalf("unexpected output: %q", out.String())
	}
}

func TestHandleUpdateAndRestartCallsRestart(t *testing.T) {
	prevUpdate := performUpdate
	prevExec := execSelf
	prevExecutable := executablePath
	prevStart := startSelf
	prevExit := exitFunc
	t.Cleanup(func() {
		performUpdate = prevUpdate
		execSelf = prevExec
		executablePath = prevExecutable
		startSelf = prevStart
		exitFunc = prevExit
	})

	performUpdate = func(ctx context.Context, opts update.UpdateOptions) (update.ApplyResult, error) {
		return update.ApplyResult{Version: "9.9.9", RestartRequired: false}, nil
	}
	executablePath = func() (string, error) { return "/tmp/claude-proxy", nil }

	called := false
	if runtime.GOOS == "windows" {
		startSelf = func(exe string, args []string) error {
			called = true
			return nil
		}
		exitFunc = func(code int) {}
	} else {
		execSelf = func(path string, args []string, env []string) error {
			called = true
			return nil
		}
	}

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := handleUpdateAndRestart(context.Background(), cmd); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatalf("expected restartSelf to call exec")
	}
	if !strings.Contains(out.String(), "Updated to v9.9.9. Restarting") {
		t.Fatalf("unexpected output: %q", out.String())
	}
}

func TestRestartSelfExecutableError(t *testing.T) {
	prevExecutable := executablePath
	t.Cleanup(func() { executablePath = prevExecutable })
	executablePath = func() (string, error) { return "", errors.New("boom") }

	if err := restartSelf(); err == nil {
		t.Fatalf("expected executable error")
	}
}

func TestStartRestartProcessError(t *testing.T) {
	if err := startRestartProcess("/nope", nil); err == nil {
		t.Fatalf("expected start error")
	}
}
