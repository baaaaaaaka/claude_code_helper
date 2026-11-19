//go:build !windows

package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

type unixTTYCaptureSession struct {
	cmd  *exec.Cmd
	ptmx *os.File
	size pty.Winsize
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
	size, ok := ttyCaptureSize()
	if !ok {
		size = pty.Winsize{Rows: 40, Cols: 120}
	}
	master, err := pty.StartWithSize(cmd, &size)
	if err != nil {
		return nil, err
	}
	// StartWithSize exposes the original descriptor in blocking mode. Give Go
	// a pollable descriptor so closing the relay can unblock both I/O pumps.
	fd, err := unix.FcntlInt(master.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err == nil {
		err = unix.SetNonblock(fd, true)
		if err != nil {
			_ = unix.Close(fd)
		}
	}
	_ = master.Close()
	if err != nil {
		_ = terminateProcess(cmd.Process, 2*time.Second)
		_ = cmd.Wait()
		return nil, err
	}
	return &unixTTYCaptureSession{cmd: cmd, ptmx: os.NewFile(uintptr(fd), master.Name()), size: size}, nil
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

func (s *unixTTYCaptureSession) Resize() {
	size, ok := ttyCaptureSize()
	if ok && size != s.size && pty.Setsize(s.ptmx, &size) == nil {
		s.size = size
	}
}

func ttyCaptureSize() (pty.Winsize, bool) {
	for _, file := range []*os.File{os.Stdout, os.Stderr, os.Stdin} {
		if file == nil {
			continue
		}
		if size, err := pty.GetsizeFull(file); err == nil && size.Rows > 0 && size.Cols > 0 {
			return *size, true
		}
	}
	return pty.Winsize{}, false
}

func ttyCaptureResizeEvents() (<-chan os.Signal, func()) {
	events := make(chan os.Signal, 1)
	signal.Notify(events, syscall.SIGWINCH)
	return events, func() { signal.Stop(events) }
}

func startTTYInputRelay(ctx context.Context, dst io.Writer, src *os.File) func() {
	done := make(chan struct{})
	if src == nil {
		return func() {}
	}
	fd := int(src.Fd())
	go func() {
		defer close(done)
		var buf [4096]byte
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		for ctx.Err() == nil {
			// Poll the parent's stdin without closing it or changing its file
			// status flags. The relay is the sole input reader on this path.
			ready, err := unix.Poll(fds, 25)
			if err == unix.EINTR {
				continue
			}
			if err != nil || ctx.Err() != nil {
				return
			}
			if ready == 0 {
				continue
			}
			n, err := unix.Read(fd, buf[:])
			if err == unix.EINTR || err == unix.EAGAIN {
				continue
			}
			if err != nil || n == 0 {
				return
			}
			if ctx.Err() != nil {
				return
			}
			if _, err := dst.Write(buf[:n]); err != nil {
				return
			}
		}
	}()
	return func() { <-done }
}
