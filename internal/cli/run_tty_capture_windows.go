//go:build windows

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/UserExistsError/conpty"
	"golang.org/x/term"
)

type windowsTTYCaptureSession struct {
	cpty      *conpty.ConPty
	closeOnce sync.Once
}

func startTTYCaptureSession(cmdArgs []string, envVars []string, cwd string) (ttyCaptureSession, error) {
	if len(cmdArgs) == 0 {
		return nil, fmt.Errorf("missing command")
	}
	width, height := ttyCaptureDimensions()
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
	return &windowsTTYCaptureSession{cpty: cpty}, nil
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
	return 120, 40
}
