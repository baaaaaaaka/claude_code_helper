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

	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/config"
	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/env"
	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/ids"
	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/manager"
	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/stack"
)

type installCmd struct {
	path string
	args []string
}

type installProxyOptions struct {
	UseProxy  bool
	Profile   *config.Profile
	Instances []config.Instance
}

func ensureClaudeInstalled(ctx context.Context, claudePath string, out io.Writer, installOpts installProxyOptions) (string, error) {
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
	if err := runClaudeInstaller(ctx, out, installOpts); err != nil {
		return "", err
	}

	if path, err := exec.LookPath("claude"); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("claude installation finished but binary not found in PATH")
}

func runClaudeInstaller(ctx context.Context, out io.Writer, installOpts installProxyOptions) error {
	proxyURL, cleanup, err := resolveInstallerProxy(ctx, installOpts)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer func() { _ = cleanup() }()
	}
	if proxyURL != "" && out != nil {
		_, _ = fmt.Fprintln(out, "Using SSH proxy for Claude installer.")
	}

	for _, cmd := range installerCandidates(runtime.GOOS) {
		if _, err := exec.LookPath(cmd.path); err != nil {
			continue
		}
		c := exec.CommandContext(ctx, cmd.path, cmd.args...)
		if proxyURL != "" {
			c.Env = env.WithProxy(os.Environ(), proxyURL)
		}
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

func resolveInstallerProxy(ctx context.Context, opts installProxyOptions) (string, func() error, error) {
	if !opts.UseProxy {
		return "", nil, nil
	}
	if opts.Profile == nil {
		return "", nil, fmt.Errorf("proxy mode enabled but no profile configured")
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}

	hc := manager.HealthClient{}
	if inst := manager.FindReusableInstance(opts.Instances, opts.Profile.ID, hc); inst != nil {
		return fmt.Sprintf("http://127.0.0.1:%d", inst.HTTPPort), nil, nil
	}

	instanceID, err := ids.New()
	if err != nil {
		return "", nil, err
	}
	st, err := stack.Start(*opts.Profile, instanceID, stack.Options{})
	if err != nil {
		return "", nil, err
	}
	return st.HTTPProxyURL(), func() error { return st.Close(context.Background()) }, nil
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
