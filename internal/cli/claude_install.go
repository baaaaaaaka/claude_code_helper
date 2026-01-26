package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type installCmd struct {
	path string
	args []string
}

func ensureClaudeInstalled(ctx context.Context, claudePath string, out io.Writer) (string, error) {
	if strings.TrimSpace(claudePath) != "" {
		if executableExists(claudePath) {
			return claudePath, nil
		}
		return "", fmt.Errorf("claude not found at %s", claudePath)
	}

	if path, err := exec.LookPath("claude"); err == nil {
		return path, nil
	}

	if out != nil {
		_, _ = fmt.Fprintln(out, "claude not found; installing...")
	}
	if err := runClaudeInstaller(ctx, out); err != nil {
		return "", err
	}

	if path, err := exec.LookPath("claude"); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("claude installation finished but binary not found in PATH")
}

func runClaudeInstaller(ctx context.Context, out io.Writer) error {
	for _, cmd := range installerCandidates(runtime.GOOS) {
		if _, err := exec.LookPath(cmd.path); err != nil {
			continue
		}
		c := exec.CommandContext(ctx, cmd.path, cmd.args...)
		c.Stdout = out
		c.Stderr = out
		c.Stdin = os.Stdin
		if err := c.Run(); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("no supported installer available for %s", runtime.GOOS)
}

func installerCandidates(goos string) []installCmd {
	switch strings.ToLower(goos) {
	case "windows":
		return []installCmd{
			{path: "powershell", args: []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", "irm https://claude.ai/install.ps1 | iex"}},
			{path: "pwsh", args: []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", "irm https://claude.ai/install.ps1 | iex"}},
			{path: "cmd.exe", args: []string{"/c", "curl -fsSL https://claude.ai/install.cmd -o install.cmd && install.cmd && del install.cmd"}},
		}
	case "darwin", "linux":
		return []installCmd{
			{path: "bash", args: []string{"-lc", "curl -fsSL https://claude.ai/install.sh | bash"}},
		}
	default:
		return nil
	}
}

func executableExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return true
}
