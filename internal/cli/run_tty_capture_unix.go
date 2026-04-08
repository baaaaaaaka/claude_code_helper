//go:build !windows

package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
)

type unixTTYCaptureSession struct {
	cmd  *exec.Cmd
	ptmx *os.File
}

func startTTYCaptureSession(cmdArgs []string, envVars []string, cwd string) (ttyCaptureSession, error) {
	if len(cmdArgs) == 0 {
		return nil, fmt.Errorf("missing command")
	}
	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Env = envVars
	if cwd != "" {
		cmd.Dir = cwd
	}
	rows, cols := ttyCaptureSize()
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		return nil, err
	}
	return &unixTTYCaptureSession{cmd: cmd, ptmx: ptmx}, nil
}

func (s *unixTTYCaptureSession) Input() io.Writer {
	return s.ptmx
}

func (s *unixTTYCaptureSession) Output() io.Reader {
	return s.ptmx
}

func (s *unixTTYCaptureSession) Wait() error {
	return s.cmd.Wait()
}

func (s *unixTTYCaptureSession) Terminate(grace time.Duration) error {
	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}
	return terminateProcess(s.cmd.Process, grace)
}

func (s *unixTTYCaptureSession) Close() error {
	if s.ptmx == nil {
		return nil
	}
	return s.ptmx.Close()
}

func ttyCaptureSize() (int, int) {
	for _, file := range []*os.File{os.Stdout, os.Stderr, os.Stdin} {
		if file == nil || !term.IsTerminal(int(file.Fd())) {
			continue
		}
		if cols, rows, err := term.GetSize(int(file.Fd())); err == nil && rows > 0 && cols > 0 {
			return rows, cols
		}
	}
	return 40, 120
}
