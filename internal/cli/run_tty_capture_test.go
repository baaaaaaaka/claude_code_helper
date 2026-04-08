//go:build !windows

package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/term"
)

type fakeTTYCaptureSession struct {
	inputBuf       bytes.Buffer
	output         io.Reader
	waitCh         chan error
	terminateCalls int
	closeCalls     int
}

func newFakeTTYCaptureSession(output string) *fakeTTYCaptureSession {
	return &fakeTTYCaptureSession{
		output: strings.NewReader(output),
		waitCh: make(chan error, 1),
	}
}

func (s *fakeTTYCaptureSession) Input() io.Writer {
	return &s.inputBuf
}

func (s *fakeTTYCaptureSession) Output() io.Reader {
	return s.output
}

func (s *fakeTTYCaptureSession) Wait() error {
	return <-s.waitCh
}

func (s *fakeTTYCaptureSession) Terminate(grace time.Duration) error {
	_ = grace
	s.terminateCalls++
	select {
	case s.waitCh <- nil:
	default:
	}
	return nil
}

func (s *fakeTTYCaptureSession) Close() error {
	s.closeCalls++
	return nil
}

func writeRuntimeYoloFailureStub(t *testing.T, logPath string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\n" +
		"log=" + shellSingleQuote(logPath) + "\n" +
		"printf '%s\\n' \"$*\" >> \"$log\"\n" +
		"has_continue=0\n" +
		"has_resume=0\n" +
		"has_perm=0\n" +
		"for arg in \"$@\"; do\n" +
		"  case \"$arg\" in\n" +
		"    --continue|-c) has_continue=1 ;;\n" +
		"    --resume|-r) has_resume=1 ;;\n" +
		"    --permission-mode|--dangerously-skip-permissions) has_perm=1 ;;\n" +
		"  esac\n" +
		"done\n" +
		"if [ \"$has_continue\" = \"1\" ]; then\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$has_resume\" = \"1\" ] && [ \"$has_perm\" = \"0\" ]; then\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$has_perm\" = \"1\" ]; then\n" +
		"  echo 'Tool permission request failed: Error: Stream closed'\n" +
		"  while :; do sleep 1; done\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write runtime yolo stub: %v", err)
	}
	return path
}

func TestRunTargetWithFallbackRuntimeYoloRetryAddsContinue(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "launches.log")
	claudePath := writeRuntimeYoloFailureStub(t, logPath)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	fallbackCalled := false
	err := runTargetWithFallbackWithOptions(
		ctx,
		[]string{claudePath, "--permission-mode", "bypassPermissions"},
		"",
		nil,
		nil,
		nil,
		runTargetOptions{
			UseProxy:    false,
			PreserveTTY: true,
			YoloEnabled: true,
			OnYoloFallback: func() error {
				fallbackCalled = true
				return nil
			},
		},
	)
	if err != nil {
		t.Fatalf("runTargetWithFallbackWithOptions error: %v", err)
	}
	if !fallbackCalled {
		t.Fatalf("expected yolo fallback callback")
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 launches, got %d: %q", len(lines), string(data))
	}
	if lines[0] != "--permission-mode bypassPermissions" {
		t.Fatalf("unexpected first launch args: %q", lines[0])
	}
	if lines[1] != "--continue" {
		t.Fatalf("expected runtime retry to use --continue, got %q", lines[1])
	}
}

func TestRunTargetWithFallbackRuntimeYoloRetryKeepsResumeArgs(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "launches.log")
	claudePath := writeRuntimeYoloFailureStub(t, logPath)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	fallbackCalled := false
	err := runTargetWithFallbackWithOptions(
		ctx,
		[]string{claudePath, "--permission-mode", "bypassPermissions", "--resume", "sess-1"},
		"",
		nil,
		nil,
		nil,
		runTargetOptions{
			UseProxy:    false,
			PreserveTTY: true,
			YoloEnabled: true,
			OnYoloFallback: func() error {
				fallbackCalled = true
				return nil
			},
		},
	)
	if err != nil {
		t.Fatalf("runTargetWithFallbackWithOptions error: %v", err)
	}
	if !fallbackCalled {
		t.Fatalf("expected yolo fallback callback")
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 launches, got %d: %q", len(lines), string(data))
	}
	if lines[0] != "--permission-mode bypassPermissions --resume sess-1" {
		t.Fatalf("unexpected first launch args: %q", lines[0])
	}
	if lines[1] != "--resume sess-1" {
		t.Fatalf("expected runtime retry to preserve --resume, got %q", lines[1])
	}
}

func TestPrepareTTYRelayMakesTerminalRawAndRestores(t *testing.T) {
	origIsTerminal := ttyRelayIsTerminalFn
	origMakeRaw := ttyRelayMakeRawFn
	origRestore := ttyRelayRestoreFn
	t.Cleanup(func() {
		ttyRelayIsTerminalFn = origIsTerminal
		ttyRelayMakeRawFn = origMakeRaw
		ttyRelayRestoreFn = origRestore
	})

	makeRawCalled := false
	restoreCalled := false
	restoreFD := -1
	ttyRelayIsTerminalFn = func(fd int) bool {
		return true
	}
	ttyRelayMakeRawFn = func(fd int) (*term.State, error) {
		makeRawCalled = true
		return &term.State{}, nil
	}
	ttyRelayRestoreFn = func(fd int, state *term.State) error {
		restoreCalled = true
		restoreFD = fd
		if state == nil {
			t.Fatalf("expected non-nil tty state")
		}
		return nil
	}

	restore, err := prepareTTYRelay()
	if err != nil {
		t.Fatalf("prepareTTYRelay error: %v", err)
	}
	if !makeRawCalled {
		t.Fatalf("expected MakeRaw to be called")
	}
	restore()
	if !restoreCalled {
		t.Fatalf("expected Restore to be called")
	}
	if restoreFD != int(os.Stdin.Fd()) {
		t.Fatalf("expected restore fd %d, got %d", os.Stdin.Fd(), restoreFD)
	}
}

func TestPrepareTTYRelayNoopWhenInputIsNotTTY(t *testing.T) {
	origIsTerminal := ttyRelayIsTerminalFn
	origMakeRaw := ttyRelayMakeRawFn
	t.Cleanup(func() {
		ttyRelayIsTerminalFn = origIsTerminal
		ttyRelayMakeRawFn = origMakeRaw
	})

	makeRawCalled := false
	ttyRelayIsTerminalFn = func(fd int) bool {
		return false
	}
	ttyRelayMakeRawFn = func(fd int) (*term.State, error) {
		makeRawCalled = true
		return &term.State{}, nil
	}

	restore, err := prepareTTYRelay()
	if err != nil {
		t.Fatalf("prepareTTYRelay error: %v", err)
	}
	restore()
	if makeRawCalled {
		t.Fatalf("expected MakeRaw not to be called for non-tty input")
	}
}

func TestRunTargetOnceWithCapturedTTYOutputDetectsRuntimeFailure(t *testing.T) {
	withExePatchTestHooks(t)
	setRunTestStdin(t, "")

	session := newFakeTTYCaptureSession("Tool permission request failed: Error: Stream closed\n")
	startTTYCaptureSessionFn = func(cmdArgs []string, envVars []string, cwd string) (ttyCaptureSession, error) {
		return session, nil
	}
	prepareTTYRelayFn = func() (func(), error) {
		return func() {}, nil
	}

	stdoutBuf := &synchronizedLimitedBuffer{max: maxOutputCaptureBytes}
	err := runTargetOnceWithCapturedTTYOutput(
		context.Background(),
		[]string{"claude"},
		nil,
		nil,
		nil,
		stdoutBuf,
		nil,
		runTargetOptions{PreserveTTY: true, CaptureTTYOutput: true, YoloEnabled: true},
	)
	if !errors.Is(err, errYoloRuntimeFailure) {
		t.Fatalf("expected errYoloRuntimeFailure, got %v", err)
	}
	if session.terminateCalls == 0 {
		t.Fatalf("expected runtime failure to terminate tty session")
	}
	if !strings.Contains(stdoutBuf.String(), "Tool permission request failed") {
		t.Fatalf("expected captured output to include runtime failure, got %q", stdoutBuf.String())
	}
}

func TestRunTargetOnceWithOptionsFallsBackWhenTTYCaptureUnavailable(t *testing.T) {
	withExePatchTestHooks(t)
	setRunTestStdin(t, "")

	startTTYCaptureSessionFn = func(cmdArgs []string, envVars []string, cwd string) (ttyCaptureSession, error) {
		return nil, errTTYCaptureUnavailable
	}
	prepareTTYRelayFn = func() (func(), error) {
		return func() {}, nil
	}

	shellPath := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(shellPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write shell script: %v", err)
	}

	if err := runTargetOnceWithOptions(
		context.Background(),
		[]string{shellPath},
		"",
		nil,
		nil,
		nil,
		nil,
		runTargetOptions{UseProxy: false, PreserveTTY: true, CaptureTTYOutput: true},
	); err != nil {
		t.Fatalf("expected fallback to plain stdio launch, got %v", err)
	}
}
