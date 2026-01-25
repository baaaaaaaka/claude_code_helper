//go:build windows

package update

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func replaceBinary(tmpPath, destPath string) (replaceResult, error) {
	if err := os.Rename(tmpPath, destPath); err == nil {
		return replaceResult{restartRequired: false}, nil
	}
	if err := scheduleWindowsMove(tmpPath, destPath); err != nil {
		return replaceResult{}, err
	}
	return replaceResult{restartRequired: true}, nil
}

func scheduleWindowsMove(src, dest string) error {
	script := fmt.Sprintf(
		"Start-Sleep -Milliseconds 400; Move-Item -Force -LiteralPath '%s' -Destination '%s'",
		escapePowerShellString(src),
		escapePowerShellString(dest),
	)
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("schedule update: %w", err)
	}
	return nil
}

func escapePowerShellString(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
