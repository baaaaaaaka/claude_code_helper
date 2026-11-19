//go:build windows

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"

	"github.com/UserExistsError/conpty"
	"golang.org/x/sys/windows"
	"golang.org/x/term"
)

type windowsTTYCaptureSession struct {
	cpty          *conpty.ConPty
	closeOnce     sync.Once
	width, height int
}

func startTTYCaptureSession(cmdArgs []string, envVars []string, cwd string) (ttyCaptureSession, error) {
	if len(cmdArgs) == 0 {
		return nil, fmt.Errorf("missing command")
	}
	width, height := ttyCaptureDimensions()
	if width == 0 {
		width, height = 120, 40
	}
	commandLine := windowsCommandLine(cmdArgs)
	cpty, err := conpty.Start(
		commandLine,
		conpty.ConPtyDimensions(width, height),
		conpty.ConPtyWorkDir(cwd),
		conpty.ConPtyEnv(envVars),
	)
	if errors.Is(err, conpty.ErrConPtyUnsupported) {
		return nil, errTTYCaptureUnavailable
	}
	if err != nil {
		return nil, err
	}
	return &windowsTTYCaptureSession{cpty: cpty, width: width, height: height}, nil
}

func (s *windowsTTYCaptureSession) Input() io.Writer {
	return s.cpty
}

func (s *windowsTTYCaptureSession) Output() io.Reader {
	return s.cpty
}

func (s *windowsTTYCaptureSession) Wait() error {
	exitCode, err := s.cpty.Wait(context.Background())
	if err != nil {
		return err
	}
	if exitCode == 0 {
		return nil
	}
	return fmt.Errorf("process exited with code %d", exitCode)
}

func (s *windowsTTYCaptureSession) Terminate(grace time.Duration) error {
	_ = grace
	return s.Close()
}

func (s *windowsTTYCaptureSession) Close() error {
	var err error
	s.closeOnce.Do(func() {
		err = s.cpty.Close()
	})
	return err
}

func windowsCommandLine(args []string) string {
	escaped := make([]string, 0, len(args))
	for _, arg := range args {
		escaped = append(escaped, syscall.EscapeArg(arg))
	}
	return strings.Join(escaped, " ")
}

func ttyCaptureDimensions() (int, int) {
	for _, file := range []*os.File{os.Stdout, os.Stderr, os.Stdin} {
		if file == nil || !term.IsTerminal(int(file.Fd())) {
			continue
		}
		if width, height, err := term.GetSize(int(file.Fd())); err == nil && width > 0 && height > 0 {
			return width, height
		}
	}
	return 0, 0
}

func (s *windowsTTYCaptureSession) Resize() {
	width, height := ttyCaptureDimensions()
	if width > 0 && (width != s.width || height != s.height) && s.cpty.Resize(width, height) == nil {
		s.width, s.height = width, height
	}
}

func ttyCaptureResizeEvents() (<-chan os.Signal, func()) {
	return nil, func() {}
}

var cancelTTYThreadIO = windows.NewLazySystemDLL("kernel32.dll").NewProc("CancelSynchronousIo")

func startTTYInputRelay(ctx context.Context, dst io.Writer, src *os.File) func() {
	if src == nil {
		return func() {}
	}
	done := make(chan struct{})
	release := make(chan struct{})
	thread := make(chan windows.Handle, 1)
	console := term.IsTerminal(int(src.Fd()))
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer func() {
			close(done)
			<-release
		}()
		handle, err := windows.OpenThread(windows.THREAD_TERMINATE, false, windows.GetCurrentThreadId())
		thread <- handle
		if err != nil {
			return
		}
		if console {
			relayWindowsConsoleInput(ctx, dst, windows.Handle(src.Fd()))
			return
		}
		var buf [4096]byte
		for ctx.Err() == nil {
			n, err := src.Read(buf[:])
			if ctx.Err() != nil {
				return
			}
			if n > 0 {
				if _, writeErr := dst.Write(buf[:n]); writeErr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	handle := <-thread
	return func() {
		defer close(release)
		if handle != 0 {
			defer windows.CloseHandle(handle)
		}
		// Retry cancellation in case the reader entered Read after the first
		// cancellation request. Only this relay's thread is affected.
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			default:
			}
			if handle != 0 {
				_, _, _ = cancelTTYThreadIO.Call(uintptr(handle))
			}
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}
}

func relayWindowsConsoleInput(ctx context.Context, dst io.Writer, console windows.Handle) {
	var buf [1025]uint16
	carry := 0
	for ctx.Err() == nil {
		// Leave the console without a pending ReadConsole while it is idle.
		// Canceling an idle read can swallow the first line after handoff.
		ready, err := windows.WaitForSingleObject(console, 25)
		if err != nil || ctx.Err() != nil {
			return
		}
		if ready == uint32(windows.WAIT_TIMEOUT) {
			continue
		}
		if ready != windows.WAIT_OBJECT_0 {
			return
		}
		var read uint32
		err = windows.ReadConsole(console, &buf[carry], 1024, &read, nil)
		if err != nil || read == 0 || ctx.Err() != nil {
			return
		}
		n := int(read) + carry
		carry = 0
		last := buf[n-1]
		if last >= 0xD800 && last <= 0xDBFF {
			n--
			carry = 1
		}
		if _, err := io.WriteString(dst, string(utf16.Decode(buf[:n]))); err != nil {
			return
		}
		if carry != 0 {
			buf[0] = last
		}
	}
}
