//go:build linux

package proc

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func CommandLine(pid int) ([]string, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("invalid pid %d", pid)
	}

	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return nil, err
	}
	data = bytes.TrimRight(data, "\x00")
	if len(data) == 0 {
		return nil, fmt.Errorf("empty command line for pid %d", pid)
	}

	parts := bytes.Split(data, []byte{0})
	args := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		args = append(args, string(part))
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("empty command line for pid %d", pid)
	}
	return args, nil
}
