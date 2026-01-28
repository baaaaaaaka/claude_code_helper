//go:build !windows

package cli

import (
	"errors"
	"os/exec"
	"syscall"
)

var fatalExitSignals = map[syscall.Signal]struct{}{
	syscall.SIGABRT: {},
	syscall.SIGBUS:  {},
	syscall.SIGFPE:  {},
	syscall.SIGILL:  {},
	syscall.SIGSEGV: {},
	syscall.SIGSYS:  {},
	syscall.SIGTRAP: {},
}

func exitDueToFatalSignal(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		if ptr, ok := exitErr.Sys().(*syscall.WaitStatus); ok && ptr != nil {
			status = *ptr
		} else {
			return false
		}
	}
	if !status.Signaled() {
		return false
	}
	_, ok = fatalExitSignals[status.Signal()]
	return ok
}
