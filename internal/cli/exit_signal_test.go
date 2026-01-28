//go:build !windows

package cli

import (
	"fmt"
	"os/exec"
	"syscall"
	"testing"
)

func TestExitDueToFatalSignal(t *testing.T) {
	signals := []syscall.Signal{
		syscall.SIGSEGV,
		syscall.SIGBUS,
		syscall.SIGILL,
		syscall.SIGABRT,
		syscall.SIGFPE,
		syscall.SIGTRAP,
		syscall.SIGSYS,
	}
	for _, sig := range signals {
		sig := sig
		t.Run(sig.String(), func(t *testing.T) {
			err := runSignalExit(sig)
			if err == nil {
				t.Fatalf("expected %v exit error", sig)
			}
			if !exitDueToFatalSignal(err) {
				t.Fatalf("expected %v to be detected as fatal", sig)
			}
		})
	}
}

func TestExitDueToFatalSignalIgnoresSIGTERM(t *testing.T) {
	err := runSignalExit(syscall.SIGTERM)
	if err == nil {
		t.Fatalf("expected SIGTERM exit error")
	}
	if exitDueToFatalSignal(err) {
		t.Fatalf("unexpected fatal detection for SIGTERM")
	}
}

func runSignalExit(sig syscall.Signal) error {
	cmd := exec.Command("sh", "-c", fmt.Sprintf("kill -%d $$", sig))
	return cmd.Run()
}
