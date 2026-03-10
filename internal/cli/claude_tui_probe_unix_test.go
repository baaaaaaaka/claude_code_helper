//go:build !windows

package cli

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/creack/pty"
)

func runClaudeTUIProbe(path string, cwd string, env []string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, path)
	cmd.Dir = cwd
	cmd.Env = env

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 120})
	if err != nil {
		return "", fmt.Errorf("start PTY: %w", err)
	}
	defer func() { _ = ptmx.Close() }()

	output := &synchronizedBuffer{}
	go func() {
		_, _ = io.Copy(output, ptmx)
	}()

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case err := <-waitDone:
			out := output.Snapshot()
			if err == nil && looksLikeClaudeTUI(out) {
				return out, nil
			}
			if err == nil {
				return out, fmt.Errorf("claude exited before TUI markers appeared")
			}
			return out, fmt.Errorf("claude exited before TUI markers appeared: %w", err)
		case <-ticker.C:
			out := output.Snapshot()
			if looksLikeClaudeTUI(out) {
				if cmd.Process != nil {
					_ = terminateProcess(cmd.Process, 2*time.Second)
				}
				_ = ptmx.Close()
				select {
				case <-waitDone:
				case <-time.After(2 * time.Second):
				}
				return out, nil
			}
		case <-ctx.Done():
			out := output.Snapshot()
			if cmd.Process != nil {
				_ = terminateProcess(cmd.Process, 2*time.Second)
			}
			_ = ptmx.Close()
			select {
			case <-waitDone:
			case <-time.After(2 * time.Second):
			}
			return out, fmt.Errorf("timed out waiting for Claude TUI: %w", ctx.Err())
		}
	}
}
