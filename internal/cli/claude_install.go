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

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
	"github.com/baaaaaaaka/claude_code_helper/internal/env"
	"github.com/baaaaaaaka/claude_code_helper/internal/ids"
	"github.com/baaaaaaaka/claude_code_helper/internal/manager"
	"github.com/baaaaaaaka/claude_code_helper/internal/stack"
)

type installCmd struct {
	path string
	args []string
}

const claudeInstallBootstrap = `url="https://claude.ai/install.sh"; if command -v curl >/dev/null 2>&1; then curl -fsSL "$url" | bash; elif command -v wget >/dev/null 2>&1; then wget -qO- "$url" | bash; else echo "need curl or wget" >&2; exit 1; fi`

const claudeInstallBootstrapWindows = `$installerUrl = 'https://claude.ai/install.ps1'
$logPath = Join-Path ([IO.Path]::GetTempPath()) ('claude-installer-error-' + [DateTime]::UtcNow.ToString('yyyyMMddHHmmssfff') + '.log')
$previousErrorActionPreference = $ErrorActionPreference
$previousProgressPreference = $ProgressPreference
try {
  $ErrorActionPreference = 'Stop'
  $ProgressPreference = 'SilentlyContinue'
  $content = [string](Invoke-RestMethod -Uri $installerUrl -MaximumRedirection 5)
  if ([string]::IsNullOrWhiteSpace($content)) {
    throw "Installer endpoint returned empty content."
  }
  if ($content -match '(?is)^\ufeff?\s*(<!doctype html|<html\b)') {
    throw "Installer endpoint returned HTML content instead of a PowerShell script."
  }
  $ErrorActionPreference = $previousErrorActionPreference
  Invoke-Expression $content
} catch {
  $details = $_ | Out-String
  try {
    "[$([DateTime]::UtcNow.ToString('o'))] $details" | Out-File -FilePath $logPath -Encoding utf8 -Append
  } catch {}
  Write-Host ("Primary Claude installer failed; trying fallback installer. Details: " + $logPath)
  exit 1
} finally {
  $ErrorActionPreference = $previousErrorActionPreference
  $ProgressPreference = $previousProgressPreference
}`

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

	candidates := installerCandidates(runtime.GOOS)
	if len(candidates) == 0 {
		return fmt.Errorf("no supported installer available for %s", runtime.GOOS)
	}
	attemptErrors := make([]string, 0, len(candidates))
	for _, cmd := range candidates {
		if _, err := exec.LookPath(cmd.path); err != nil {
			attemptErrors = append(attemptErrors, fmt.Sprintf("%s: not found in PATH", installerAttemptLabel(cmd)))
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
			attemptErrors = append(attemptErrors, fmt.Sprintf("%s: %v", installerAttemptLabel(cmd), err))
			continue
		}
		return nil
	}
	if len(attemptErrors) == 0 {
		return fmt.Errorf("no supported installer available for %s", runtime.GOOS)
	}
	return fmt.Errorf("failed to run Claude installer for %s (%s)", runtime.GOOS, strings.Join(attemptErrors, "; "))
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
			{path: "powershell", args: []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", claudeInstallBootstrapWindows}},
			{path: "pwsh", args: []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", claudeInstallBootstrapWindows}},
			{path: "cmd.exe", args: []string{"/c", "curl -fsSL https://claude.ai/install.cmd -o install.cmd && install.cmd && del install.cmd"}},
		}
	case "darwin", "linux":
		return []installCmd{
			{path: "bash", args: []string{"-c", claudeInstallBootstrap}},
			{path: "sh", args: []string{"-c", claudeInstallBootstrap}},
		}
	default:
		return nil
	}
}

func installerAttemptLabel(cmd installCmd) string {
	if len(cmd.args) == 0 {
		return cmd.path
	}
	return fmt.Sprintf("%s %s", cmd.path, cmd.args[0])
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
