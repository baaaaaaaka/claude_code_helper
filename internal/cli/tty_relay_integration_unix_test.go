//go:build !windows

package cli

import (
	"os/exec"
	"testing"
	"time"

	"github.com/creack/pty"
)

func startOuterTestTTY(t *testing.T, args, env []string, width, height int) (ttyCaptureSession, func(int, int) error) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = env
	master, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(height), Cols: uint16(width)})
	if err != nil {
		t.Fatal(err)
	}
	session := &unixTTYCaptureSession{cmd: cmd, ptmx: master}
	t.Cleanup(func() {
		if t.Failed() {
			_ = session.Terminate(500 * time.Millisecond)
		}
		_ = session.Close()
	})
	return session, func(w, h int) error {
		return pty.Setsize(master, &pty.Winsize{Rows: uint16(h), Cols: uint16(w)})
	}
}
