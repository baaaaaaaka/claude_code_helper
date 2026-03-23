//go:build !windows && !linux && !darwin

package proc

import (
	"fmt"
	"runtime"
)

func CommandLine(pid int) ([]string, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("invalid pid %d", pid)
	}
	return nil, fmt.Errorf("command line inspection unsupported on %s", runtime.GOOS)
}
