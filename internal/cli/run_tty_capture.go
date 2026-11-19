package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
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

type ttyCaptureResizer interface {
	Resize()
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
	resizeEvents, stopResize := ttyCaptureResizeEvents()
	defer stopResize()
	session, err := startTTYCaptureSessionFn(cmdArgs, envVars, opts.Cwd)
	if err != nil {
		return err
	}
	restoreTTY, err := prepareTTYRelayFn()
	if err != nil {
		_ = session.Terminate(2 * time.Second)
		_ = session.Close()
		_ = session.Wait()
		return err
	}
	defer restoreTTY()

	outputWriter := io.Writer(os.Stdout)
	if stdoutBuf != nil {
		outputWriter = io.MultiWriter(os.Stdout, stdoutBuf)
	}
	outputDone := make(chan struct{})
	go func() {
		defer close(outputDone)
		_, _ = io.Copy(outputWriter, session.Output())
	}()
	inputCtx, cancelInput := context.WithCancel(ctx)
	input := session.Input()
	waitInput := startTTYInputRelay(inputCtx, input, os.Stdin)
	stopInput := sync.OnceFunc(func() {
		cancelInput()
		if d, ok := input.(interface{ SetWriteDeadline(time.Time) error }); ok {
			_ = d.SetWriteDeadline(time.Now())
		}
		waitInput()
	})
	defer func() {
		stopInput()
		_ = session.Close()
		waitForTTYRelayOutput(outputDone)
	}()

	resizer, _ := session.(ttyCaptureResizer)
	if resizer != nil {
		resizer.Resize()
	}

	waitDone := make(chan error, 1)
	go func() { waitDone <- session.Wait() }()

	healthTicker := time.NewTicker(5 * time.Second)
	defer healthTicker.Stop()
	runtimeTicker := time.NewTicker(100 * time.Millisecond)
	defer runtimeTicker.Stop()

	failures := 0
	var recovery recoveryBudget
	for {
		select {
		case err := <-waitDone:
			// The exit status can arrive before the final startup diagnostic.
			waitForTTYRelayOutput(outputDone)
			if opts.YoloEnabled && looksLikeYoloRuntimeFailure(capturedTTYOutput(stdoutBuf, stderrBuf)) {
				return errYoloRuntimeFailure
			}
			return err
		case err := <-fatalCh:
			stopInput()
			_ = session.Terminate(2 * time.Second)
			waitForTTYSessionExit(waitDone)
			return fmt.Errorf("proxy stack failed; terminated target: %w", err)
		case <-ctx.Done():
			stopInput()
			_ = session.Terminate(2 * time.Second)
			waitForTTYSessionExit(waitDone)
			return ctx.Err()
		case <-healthTicker.C:
			if healthCheck == nil {
				continue
			}
			if err := healthCheck(); err != nil {
				if recovery.Allow(err, time.Now()) {
					continue
				}
				failures++
				if failures >= 3 {
					stopInput()
					_ = session.Terminate(2 * time.Second)
					waitForTTYSessionExit(waitDone)
					return fmt.Errorf("proxy unhealthy; terminated target: %w", err)
				}
				continue
			}
			failures = 0
			recovery = recoveryBudget{}
		case <-resizeEvents:
			if resizer != nil {
				resizer.Resize()
			}
		case <-runtimeTicker.C:
			if resizer != nil && resizeEvents == nil {
				resizer.Resize()
			}
			if !opts.YoloEnabled || !looksLikeYoloRuntimeFailure(capturedTTYOutput(stdoutBuf, stderrBuf)) {
				continue
			}
			stopInput()
			_ = session.Terminate(2 * time.Second)
			waitForTTYSessionExit(waitDone)
			return errYoloRuntimeFailure
		}
	}
}

func waitForTTYRelayOutput(done <-chan struct{}) {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
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
