//go:build windows

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"syscall"
	"time"

	"github.com/UserExistsError/conpty"
)

func runClaudeTUIProbe(path string, cwd string, env []string, timeout time.Duration) (string, error) {
	commandLine := syscall.EscapeArg(path)
	cpty, err := conpty.Start(
		commandLine,
		conpty.ConPtyDimensions(120, 40),
		conpty.ConPtyWorkDir(cwd),
		conpty.ConPtyEnv(env),
	)
	if errors.Is(err, conpty.ErrConPtyUnsupported) {
		return "", errClaudeTUIProbeUnsupported
	}
	if err != nil {
		return "", fmt.Errorf("start ConPTY: %w", err)
	}
	var closeOnce sync.Once
	closePty := func() {
		closeOnce.Do(func() {
			_ = cpty.Close()
		})
	}
	defer closePty()

	output := &synchronizedBuffer{}
	go func() {
		_, _ = io.Copy(output, cpty)
	}()

	type waitResult struct {
		exitCode uint32
		err      error
	}
	waitDone := make(chan waitResult, 1)
	go func() {
		exitCode, waitErr := cpty.Wait(context.Background())
		waitDone <- waitResult{exitCode: exitCode, err: waitErr}
	}()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()

	for {
		select {
		case res := <-waitDone:
			out := output.Snapshot()
			if res.err == nil && res.exitCode == 0 && looksLikeClaudeTUI(out) {
				return out, nil
			}
			if res.err != nil {
				return out, fmt.Errorf("claude exited before TUI markers appeared: %w", res.err)
			}
			return out, fmt.Errorf("claude exited before TUI markers appeared with exit code %d", res.exitCode)
		case <-ticker.C:
			out := output.Snapshot()
			if looksLikeClaudeTUI(out) {
				closePty()
				select {
				case <-waitDone:
				case <-time.After(2 * time.Second):
				}
				return out, nil
			}
		case <-timeoutTimer.C:
			out := output.Snapshot()
			closePty()
			select {
			case <-waitDone:
			case <-time.After(2 * time.Second):
			}
			return out, fmt.Errorf("timed out waiting for Claude TUI after %s", timeout)
		}
	}
}
