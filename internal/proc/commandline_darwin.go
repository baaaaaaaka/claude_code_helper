//go:build darwin

package proc

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

var commandLineExecCommand = exec.Command

func CommandLine(pid int) ([]string, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("invalid pid %d", pid)
	}

	// `ps` on macOS truncates long command lines unless `-ww` is used.
	out, err := commandLineExecCommand("ps", "-ww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return nil, err
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return nil, fmt.Errorf("empty command line for pid %d", pid)
	}
	return strings.Fields(line), nil
}
