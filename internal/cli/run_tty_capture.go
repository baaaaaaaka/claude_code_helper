package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/term"
)

var errTTYCaptureUnavailable = errors.New("tty capture unavailable")
var errYoloRuntimeFailure = errors.New("yolo runtime failure")
var prepareTTYRelayFn = prepareTTYRelay
var startTTYCaptureSessionFn = startTTYCaptureSession

var (
	ttyRelayIsTerminalFn = term.IsTerminal
	ttyRelayMakeRawFn    = term.MakeRaw
	ttyRelayRestoreFn    = term.Restore
)

func isYoloRuntimeFailure(err error) bool {
	return errors.Is(err, errYoloRuntimeFailure)
}

type ttyCaptureSession interface {
	Input() io.Writer
	Output() io.Reader
	Wait() error
	Terminate(grace time.Duration) error
	Close() error
}

func runTargetOnceWithCapturedTTYOutput(
	ctx context.Context,
	cmdArgs []string,
	envVars []string,
	healthCheck func() error,
	fatalCh <-chan error,
	stdoutBuf io.Writer,
	stderrBuf io.Writer,
	opts runTargetOptions,
) error {
	session, err := startTTYCaptureSessionFn(cmdArgs, envVars, opts.Cwd)
	if err != nil {
		return err
	}
	defer func() { _ = session.Close() }()
	restoreTTY, err := prepareTTYRelayFn()
	if err != nil {
		return err
	}
	defer restoreTTY()

	outputWriter := io.Writer(os.Stdout)
	if stdoutBuf != nil {
		outputWriter = io.MultiWriter(os.Stdout, stdoutBuf)
	}
	go func() {
		_, _ = io.Copy(outputWriter, session.Output())
	}()
	go func() {
		_, _ = io.Copy(session.Input(), os.Stdin)
	}()

	waitDone := make(chan error, 1)
	go func() { waitDone <- session.Wait() }()

	healthTicker := time.NewTicker(5 * time.Second)
	defer healthTicker.Stop()
	runtimeTicker := time.NewTicker(100 * time.Millisecond)
	defer runtimeTicker.Stop()

	failures := 0
	for {
		select {
		case err := <-waitDone:
			if opts.YoloEnabled && looksLikeYoloRuntimeFailure(capturedTTYOutput(stdoutBuf, stderrBuf)) {
				return errYoloRuntimeFailure
			}
			return err
		case err := <-fatalCh:
			_ = session.Terminate(2 * time.Second)
			waitForTTYSessionExit(waitDone)
			return fmt.Errorf("proxy stack failed; terminated target: %w", err)
		case <-ctx.Done():
			_ = session.Terminate(2 * time.Second)
			waitForTTYSessionExit(waitDone)
			return ctx.Err()
		case <-healthTicker.C:
			if healthCheck == nil {
				continue
			}
			if err := healthCheck(); err != nil {
				failures++
				if failures >= 3 {
					_ = session.Terminate(2 * time.Second)
					waitForTTYSessionExit(waitDone)
					return fmt.Errorf("proxy unhealthy; terminated target: %w", err)
				}
				continue
			}
			failures = 0
		case <-runtimeTicker.C:
			if !opts.YoloEnabled || !looksLikeYoloRuntimeFailure(capturedTTYOutput(stdoutBuf, stderrBuf)) {
				continue
			}
			_ = session.Terminate(2 * time.Second)
			waitForTTYSessionExit(waitDone)
			return errYoloRuntimeFailure
		}
	}
}

func capturedTTYOutput(stdoutBuf io.Writer, stderrBuf io.Writer) string {
	return writerString(stdoutBuf) + writerString(stderrBuf)
}

func writerString(w io.Writer) string {
	type stringer interface {
		String() string
	}
	if s, ok := w.(stringer); ok {
		return s.String()
	}
	return ""
}

func waitForTTYSessionExit(waitDone <-chan error) {
	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
	}
}

func prepareTTYRelay() (func(), error) {
	if os.Stdin == nil {
		return func() {}, nil
	}
	fd := int(os.Stdin.Fd())
	if !ttyRelayIsTerminalFn(fd) {
		return func() {}, nil
	}
	state, err := ttyRelayMakeRawFn(fd)
	if err != nil {
		return nil, err
	}
	return func() {
		_ = ttyRelayRestoreFn(fd, state)
	}, nil
}
