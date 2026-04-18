package cli

import (
	"bytes"
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

var (
	claudeInstallGOOS                 = runtime.GOOS
	runClaudeInstallerWithEnvFn       = runClaudeInstallerWithEnv
	ensureManagedNPMClaudeInstalledFn = ensureManagedNPMClaudeInstalled
	ensureWindowsGitBashFn            = ensureWindowsGitBash
	claudeInstallLookPathFn           = exec.LookPath
	claudeInstallSetenvFn             = os.Setenv
	claudeInstallStackStart           = stack.Start
)

type installCmd struct {
	path string
	args []string
}

const claudeInstallBootstrap = `url="https://claude.ai/install.sh"; if command -v curl >/dev/null 2>&1; then curl -fsSL "$url" | bash; elif command -v wget >/dev/null 2>&1; then wget -qO- "$url" | bash; else echo "need curl or wget" >&2; exit 1; fi`
const windowsGitBashInstallHelp = "Claude Code on Windows needs Git Bash. Install Git for Windows or set CLAUDE_CODE_GIT_BASH_PATH to your bash.exe and rerun the command."

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

const windowsGitBashBootstrap = `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$headers = @{ 'User-Agent' = 'claude-proxy-git-bash-bootstrap' }

function Get-LocalAppDataDir {
  if (-not [string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
    return $env:LOCALAPPDATA
  }
  if (-not [string]::IsNullOrWhiteSpace($env:USERPROFILE)) {
    return (Join-Path $env:USERPROFILE 'AppData\Local')
  }
  throw 'LOCALAPPDATA and USERPROFILE are not set.'
}

function Resolve-KnownBashPath {
  $candidates = @()
  if (-not [string]::IsNullOrWhiteSpace($env:CLAUDE_CODE_GIT_BASH_PATH)) {
    $candidates += $env:CLAUDE_CODE_GIT_BASH_PATH
  }
  $localAppData = Get-LocalAppDataDir
  $candidates += (Join-Path $localAppData 'claude-proxy\git\current\bin\bash.exe')
  if (-not [string]::IsNullOrWhiteSpace($env:ProgramFiles)) {
    $candidates += (Join-Path $env:ProgramFiles 'Git\bin\bash.exe')
  }
  $programFilesX86 = [Environment]::GetEnvironmentVariable('ProgramFiles(x86)')
  if (-not [string]::IsNullOrWhiteSpace($programFilesX86)) {
    $candidates += (Join-Path $programFilesX86 'Git\bin\bash.exe')
  }
  $candidates += (Join-Path $localAppData 'Programs\Git\bin\bash.exe')

  foreach ($candidate in $candidates) {
    if ([string]::IsNullOrWhiteSpace($candidate)) {
      continue
    }
    if (Test-Path -LiteralPath $candidate -PathType Leaf) {
      return [IO.Path]::GetFullPath($candidate)
    }
  }

  return ''
}

$existing = Resolve-KnownBashPath
if (-not [string]::IsNullOrWhiteSpace($existing)) {
  [Environment]::SetEnvironmentVariable('CLAUDE_CODE_GIT_BASH_PATH', $existing, 'User')
  Write-Output $existing
  exit 0
}

$arch = '64-bit'
if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') {
  $arch = 'arm64'
}

$downloadPage = [string](Invoke-WebRequest -Uri 'https://git-scm.com/install/windows.html' -Headers $headers -UseBasicParsing).Content
$pattern = 'https://github\.com/git-for-windows/git/releases/download/[^"]+/PortableGit-[^"]*-' + [regex]::Escape($arch) + '\.7z\.exe'
$match = [regex]::Match($downloadPage, $pattern)
if (-not $match.Success) {
  throw ('PortableGit download URL not found for architecture ' + $arch)
}
$assetUrl = $match.Value
$assetName = [IO.Path]::GetFileName($assetUrl)

$downloadPath = Join-Path ([IO.Path]::GetTempPath()) $assetName
$installRoot = Join-Path (Get-LocalAppDataDir) 'claude-proxy\git'
$currentDir = Join-Path $installRoot 'current'
$stagingDir = Join-Path $installRoot ('staging-' + [Guid]::NewGuid().ToString('N'))

try {
  Invoke-WebRequest -Uri $assetUrl -OutFile $downloadPath -UseBasicParsing
  New-Item -ItemType Directory -Force -Path $stagingDir | Out-Null
  $proc = Start-Process -FilePath $downloadPath -ArgumentList @('-y', ('-o' + $stagingDir)) -Wait -PassThru
  if ($proc.ExitCode -ne 0) {
    throw ('PortableGit extractor exited with code ' + $proc.ExitCode)
  }

  $resolvedRoot = ''
  if (Test-Path -LiteralPath (Join-Path $stagingDir 'bin\bash.exe') -PathType Leaf) {
    $resolvedRoot = $stagingDir
  }
  if ([string]::IsNullOrWhiteSpace($resolvedRoot)) {
    foreach ($child in (Get-ChildItem -LiteralPath $stagingDir -Directory -ErrorAction SilentlyContinue)) {
      if (Test-Path -LiteralPath (Join-Path $child.FullName 'bin\bash.exe') -PathType Leaf) {
        $resolvedRoot = $child.FullName
        break
      }
    }
  }
  if ([string]::IsNullOrWhiteSpace($resolvedRoot)) {
    throw 'PortableGit extracted but bash.exe was not found.'
  }

  New-Item -ItemType Directory -Force -Path $installRoot | Out-Null
  if (Test-Path -LiteralPath $currentDir) {
    Remove-Item -LiteralPath $currentDir -Recurse -Force
  }

  if ([IO.Path]::GetFullPath($resolvedRoot).TrimEnd('\') -ieq [IO.Path]::GetFullPath($stagingDir).TrimEnd('\')) {
    Rename-Item -LiteralPath $stagingDir -NewName 'current'
  } else {
    Move-Item -LiteralPath $resolvedRoot -Destination $currentDir
    Remove-Item -LiteralPath $stagingDir -Recurse -Force -ErrorAction SilentlyContinue
  }

  $bashPath = Join-Path $currentDir 'bin\bash.exe'
  if (-not (Test-Path -LiteralPath $bashPath -PathType Leaf)) {
    throw 'PortableGit install did not produce bash.exe.'
  }
  [Environment]::SetEnvironmentVariable('CLAUDE_CODE_GIT_BASH_PATH', $bashPath, 'User')
  Write-Output $bashPath
} finally {
  if (Test-Path -LiteralPath $downloadPath) {
    Remove-Item -LiteralPath $downloadPath -Force -ErrorAction SilentlyContinue
  }
  if (Test-Path -LiteralPath $stagingDir) {
    Remove-Item -LiteralPath $stagingDir -Recurse -Force -ErrorAction SilentlyContinue
  }
}`

type installProxyOptions struct {
	UseProxy  bool
	Profile   *config.Profile
	Instances []config.Instance
}

func ensureClaudeInstalled(ctx context.Context, claudePath string, out io.Writer, installOpts installProxyOptions) (string, error) {
	if strings.TrimSpace(claudePath) != "" {
		if executableExists(claudePath) {
			if err := exportCurrentProcessGitBashPath(findWindowsGitBashPath(os.Getenv)); err != nil {
				return "", err
			}
			return claudePath, nil
		}
		return "", fmt.Errorf("claude not found at %s", claudePath)
	}

	if err := exportCurrentProcessGitBashPath(findWindowsGitBashPath(os.Getenv)); err != nil {
		return "", err
	}
	if path, ok := findManagedClaudePath(claudeInstallGOOS, "", os.Getenv); ok {
		return path, nil
	}

	if out != nil {
		_, _ = fmt.Fprintln(out, "claude not found; installing...")
	}
	var installLog bytes.Buffer
	installOut := io.Writer(&installLog)
	if out != nil {
		installOut = io.MultiWriter(out, &installLog)
	}
	err := runClaudeInstaller(ctx, installOut, installOpts)
	if err == nil {
		if path, ok := findManagedClaudePath(claudeInstallGOOS, installLog.String(), os.Getenv); ok {
			return path, nil
		}
		if overrideBunKernelCheckEnabled(claudeInstallGOOS) {
			if out != nil {
				_, _ = fmt.Fprintln(out, "Claude's official installer did not leave behind a detectable managed launcher on this old-kernel host; falling back to the npm distribution.")
			}
			return ensureManagedNPMClaudeInstalledFn(ctx, installOut, installOpts, nil)
		}
	} else if !needsWindowsGitBash(claudeInstallGOOS, installLog.String()) {
		return "", err
	}

	if needsWindowsGitBash(claudeInstallGOOS, installLog.String()) {
		if out != nil {
			_, _ = fmt.Fprintln(out, "Claude installer needs Git Bash; installing a private Git for Windows runtime...")
		}
		bashPath, bashErr := ensureWindowsGitBashFn(ctx, out, installOpts)
		if bashErr != nil {
			return "", fmt.Errorf("failed to install Git Bash for Claude Code: %w", bashErr)
		}
		if err := exportCurrentProcessGitBashPath(bashPath); err != nil {
			return "", err
		}
		if out != nil {
			_, _ = fmt.Fprintln(out, "Retrying Claude installer with the configured Git Bash...")
		}
		var retryLog bytes.Buffer
		retryOut := io.Writer(&retryLog)
		if out != nil {
			retryOut = io.MultiWriter(out, &retryLog)
		}
		retryErr := runClaudeInstallerWithEnvFn(ctx, retryOut, installOpts, []string{"CLAUDE_CODE_GIT_BASH_PATH=" + bashPath})
		combinedLog := installLog.String() + "\n" + retryLog.String()
		if retryErr != nil {
			return "", retryErr
		}
		if path, ok := findManagedClaudePath(claudeInstallGOOS, combinedLog, getenvWithInstallOverrides(os.Getenv, map[string]string{
			"CLAUDE_CODE_GIT_BASH_PATH": bashPath,
		})); ok {
			return path, nil
		}
		return "", claudeInstallNotFoundError(claudeInstallGOOS, combinedLog)
	}

	if err == nil {
		return "", claudeInstallNotFoundError(claudeInstallGOOS, installLog.String())
	}
	return "", err
}

func runClaudeInstaller(ctx context.Context, out io.Writer, installOpts installProxyOptions) error {
	return runClaudeInstallerWithEnvFn(ctx, out, installOpts, nil)
}

func runClaudeInstallerWithEnv(ctx context.Context, out io.Writer, installOpts installProxyOptions, extraEnv []string) error {
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
	if claudeRequiresNPMInstall(claudeInstallGOOS) {
		return runNPMClaudeInstallerWithEnv(ctx, out, proxyURL, extraEnv)
	}
	if err := runOfficialClaudeInstallerWithEnv(ctx, out, proxyURL, extraEnv); err == nil {
		return nil
	} else if !overrideBunKernelCheckEnabled(claudeInstallGOOS) {
		return err
	} else {
		if out != nil {
			_, _ = fmt.Fprintf(out, "Claude's official installer failed on this old-kernel Linux host; falling back to the npm distribution. Error: %v\n", err)
		}
		if npmErr := runNPMClaudeInstallerWithEnv(ctx, out, proxyURL, extraEnv); npmErr != nil {
			return fmt.Errorf("official Claude installer failed: %v; npm fallback failed: %w", err, npmErr)
		}
		return nil
	}
}

func runOfficialClaudeInstallerWithEnv(ctx context.Context, out io.Writer, proxyURL string, extraEnv []string) error {
	candidates := installerCandidates(runtime.GOOS)
	if len(candidates) == 0 {
		return fmt.Errorf("no supported installer available for %s", runtime.GOOS)
	}
	attemptErrors := make([]string, 0, len(candidates))
	var combinedOutput bytes.Buffer
	for _, cmd := range candidates {
		if _, err := claudeInstallLookPathFn(cmd.path); err != nil {
			attemptErrors = append(attemptErrors, fmt.Sprintf("%s: not found in PATH", installerAttemptLabel(cmd)))
			continue
		}
		var attemptOutput bytes.Buffer
		writer := io.Writer(&attemptOutput)
		if out != nil {
			writer = io.MultiWriter(out, &attemptOutput)
		}
		c := exec.CommandContext(ctx, cmd.path, cmd.args...)
		envList := append([]string{}, os.Environ()...)
		if proxyURL != "" {
			envList = env.WithProxy(envList, proxyURL)
		}
		if strings.EqualFold(claudeInstallGOOS, "windows") {
			if bashPath := findWindowsGitBashPath(os.Getenv); bashPath != "" {
				envList = setInstallEnvValue(envList, "CLAUDE_CODE_GIT_BASH_PATH", bashPath)
			}
		}
		for _, kv := range extraEnv {
			key, value, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			envList = setInstallEnvValue(envList, key, value)
		}
		c.Env = envList
		c.Stdout = writer
		c.Stderr = writer
		c.Stdin = os.Stdin
		if err := c.Run(); err != nil {
			appendInstallOutput(&combinedOutput, attemptOutput.String())
			attemptErrors = append(attemptErrors, fmt.Sprintf("%s: %v", installerAttemptLabel(cmd), err))
			continue
		}
		return nil
	}
	if recovered, recoverErr := maybeRecoverClaudeInstallOnEL7(combinedOutput.String(), out); recovered {
		if recoverErr == nil {
			return nil
		}
		attemptErrors = append(attemptErrors, fmt.Sprintf("el7 glibc fallback: %v", recoverErr))
	}
	if len(attemptErrors) == 0 {
		return fmt.Errorf("no supported installer available for %s", runtime.GOOS)
	}
	return fmt.Errorf("failed to run Claude installer for %s (%s)", runtime.GOOS, strings.Join(attemptErrors, "; "))
}

func ensureManagedNPMClaudeInstalled(ctx context.Context, out io.Writer, installOpts installProxyOptions, extraEnv []string) (string, error) {
	proxyURL, cleanup, err := resolveInstallerProxy(ctx, installOpts)
	if err != nil {
		return "", err
	}
	if cleanup != nil {
		defer func() { _ = cleanup() }()
	}
	if proxyURL != "" && out != nil {
		_, _ = fmt.Fprintln(out, "Using SSH proxy for Claude installer.")
	}
	if err := runNPMClaudeInstallerWithEnv(ctx, out, proxyURL, extraEnv); err != nil {
		return "", err
	}
	if path, ok := findManagedNPMClaudePath(claudeInstallGOOS, getenvWithInstallOverrides(os.Getenv, envOverridesFromPairs(extraEnv))); ok {
		return path, nil
	}
	return "", fmt.Errorf("managed npm Claude installation finished but the npm launcher was not found")
}

func appendInstallOutput(dst *bytes.Buffer, text string) {
	text = strings.TrimSpace(text)
	if dst == nil || text == "" {
		return
	}
	if dst.Len() > 0 {
		_, _ = dst.WriteString("\n")
	}
	_, _ = dst.WriteString(text)
}

func maybeRecoverClaudeInstallOnEL7(installerOutput string, out io.Writer) (bool, error) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return false, nil
	}
	if !glibcCompatHostEligibleFn() || !isMissingGlibcSymbolError(installerOutput) {
		return false, nil
	}

	binaryPath := parseInstallerDownloadedClaudeBinary(installerOutput)
	if binaryPath == "" {
		return true, fmt.Errorf("installer hit GLIBC compatibility failure but no downloaded Claude binary was found in the installer output")
	}

	launcherPath, err := installRecoveredClaudeLauncher(binaryPath)
	if err != nil {
		return true, err
	}
	if out != nil {
		_, _ = fmt.Fprintln(out, "Claude installer hit GLIBC compatibility limits on this EL7 host; prepared a claude-proxy-managed launcher from the downloaded Claude binary.")
		_, _ = fmt.Fprintf(out, "Location: %s\n", launcherPath)
	}
	return true, nil
}

func parseInstallerDownloadedClaudeBinary(output string) string {
	const marker = ".claude/downloads/claude-"

	var candidate string
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if !strings.Contains(line, marker) || !isMissingGlibcSymbolError(line) {
			continue
		}
		path, _, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "" || !executableExists(path) {
			continue
		}
		candidate = path
	}
	return candidate
}

func installRecoveredClaudeLauncher(binaryPath string) (string, error) {
	if !executableExists(binaryPath) {
		return "", fmt.Errorf("recovered Claude binary not found at %s", binaryPath)
	}

	hostRoot, _, err := resolveClaudeProxyHostRoot()
	if err != nil {
		return "", fmt.Errorf("resolve claude-proxy host root for recovered Claude launcher: %w", err)
	}
	launcherDir := filepath.Join(hostRoot, "install-recovery")
	if err := os.MkdirAll(launcherDir, 0o755); err != nil {
		return "", fmt.Errorf("create Claude launcher dir %s: %w", launcherDir, err)
	}
	launcherPath := filepath.Join(launcherDir, "claude")

	if info, err := os.Lstat(launcherPath); err == nil {
		if info.IsDir() {
			return "", fmt.Errorf("existing Claude launcher path is a directory: %s", launcherPath)
		}
		if resolved, resolveErr := filepath.EvalSymlinks(launcherPath); resolveErr == nil && config.PathsEqual(resolved, binaryPath) {
			return launcherPath, nil
		}
		if err := os.Remove(launcherPath); err != nil {
			return "", fmt.Errorf("replace Claude launcher %s: %w", launcherPath, err)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect Claude launcher %s: %w", launcherPath, err)
	}

	if err := os.Symlink(binaryPath, launcherPath); err != nil {
		return "", fmt.Errorf("create Claude launcher symlink %s -> %s: %w", launcherPath, binaryPath, err)
	}
	return launcherPath, nil
}

func ensureWindowsGitBash(ctx context.Context, out io.Writer, installOpts installProxyOptions) (string, error) {
	if path := findWindowsGitBashPath(os.Getenv); path != "" {
		return path, nil
	}

	proxyURL, cleanup, err := resolveInstallerProxy(ctx, installOpts)
	if err != nil {
		return "", err
	}
	if cleanup != nil {
		defer func() { _ = cleanup() }()
	}
	if proxyURL != "" && out != nil {
		_, _ = fmt.Fprintln(out, "Using SSH proxy for Git for Windows bootstrap.")
	}

	candidates := []installCmd{
		{path: "powershell", args: []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", windowsGitBashBootstrap}},
		{path: "pwsh", args: []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", windowsGitBashBootstrap}},
	}
	attemptErrors := make([]string, 0, len(candidates))
	for _, cmd := range candidates {
		if _, err := claudeInstallLookPathFn(cmd.path); err != nil {
			attemptErrors = append(attemptErrors, fmt.Sprintf("%s: not found in PATH", installerAttemptLabel(cmd)))
			continue
		}
		var output bytes.Buffer
		writer := io.Writer(&output)
		if out != nil {
			writer = io.MultiWriter(out, &output)
		}
		c := exec.CommandContext(ctx, cmd.path, cmd.args...)
		envList := append([]string{}, os.Environ()...)
		if proxyURL != "" {
			envList = env.WithProxy(envList, proxyURL)
		}
		c.Env = envList
		c.Stdout = writer
		c.Stderr = writer
		c.Stdin = os.Stdin
		if err := c.Run(); err != nil {
			attemptErrors = append(attemptErrors, fmt.Sprintf("%s: %v", installerAttemptLabel(cmd), err))
			continue
		}
		if path := resolveWindowsGitBashLocation(parseInstalledGitBashLocation(output.String())); path != "" {
			return path, nil
		}
		if path := findWindowsGitBashPath(os.Getenv); path != "" {
			return path, nil
		}
		attemptErrors = append(attemptErrors, fmt.Sprintf("%s: bootstrap completed but bash.exe was not found", installerAttemptLabel(cmd)))
	}
	if len(attemptErrors) == 0 {
		return "", fmt.Errorf("no supported Git Bash bootstrap available for %s", claudeInstallGOOS)
	}
	return "", fmt.Errorf("failed to install Git Bash for %s (%s)", claudeInstallGOOS, strings.Join(attemptErrors, "; "))
}

func exportCurrentProcessGitBashPath(path string) error {
	if !strings.EqualFold(claudeInstallGOOS, "windows") {
		return nil
	}
	path = resolveWindowsGitBashLocation(path)
	if path == "" {
		return nil
	}
	if err := claudeInstallSetenvFn("CLAUDE_CODE_GIT_BASH_PATH", path); err != nil {
		return fmt.Errorf("set CLAUDE_CODE_GIT_BASH_PATH for current process: %w", err)
	}
	return nil
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
	st, err := claudeInstallStackStart(*opts.Profile, instanceID, stack.Options{})
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

func findManagedClaudePath(goos string, installOutput string, getenv func(string) string) (string, bool) {
	if path := resolveInstalledClaudeLocation(goos, parseInstalledClaudeLocation(installOutput)); path != "" {
		return path, true
	}

	for _, candidate := range defaultClaudeInstallCandidates(goos, getenv) {
		if executableExists(candidate) {
			return candidate, true
		}
	}

	return "", false
}

func findInstalledClaudePath(goos string, installOutput string, getenv func(string) string) (string, bool) {
	if path, ok := findManagedClaudePath(goos, installOutput, getenv); ok {
		return path, true
	}

	if path, err := claudeInstallLookPathFn("claude"); err == nil {
		return path, true
	}

	return "", false
}

func defaultClaudeInstallCandidates(goos string, getenv func(string) string) []string {
	npmCandidate := defaultManagedNPMClaudeLauncherCandidate(goos, getenv)
	if claudeRequiresNPMInstall(goos) {
		return appendInstallCandidate(nil, goos, npmCandidate)
	}

	homes := installHomeCandidates(goos, getenv)

	if !strings.EqualFold(goos, "windows") {
		candidates := make([]string, 0, len(homes)*2+2)
		for _, home := range homes {
			for _, candidate := range []string{
				filepath.Join(home, ".local", "bin", "claude"),
				filepath.Join(home, ".claude", "local", "claude"),
			} {
				candidates = appendInstallCandidate(candidates, goos, candidate)
			}
		}
		if candidate := defaultRecoveredClaudeLauncherCandidate(goos, getenv); candidate != "" {
			candidates = appendInstallCandidate(candidates, goos, candidate)
		}
		if npmCandidate != "" {
			candidates = appendInstallCandidate(candidates, goos, npmCandidate)
		}
		return candidates
	}

	if len(homes) == 0 {
		return nil
	}
	candidates := make([]string, 0, len(homes)*5)
	for _, home := range homes {
		base := filepath.Join(home, ".local", "bin")
		for _, name := range []string{"claude.exe", "claude.cmd", "claude.bat", "claude.com", "claude"} {
			candidates = appendInstallCandidate(candidates, goos, filepath.Join(base, name))
		}
	}
	return candidates
}

func installHomeCandidates(goos string, getenv func(string) string) []string {
	keys := []string{"HOME", "USERPROFILE"}
	if strings.EqualFold(goos, "windows") {
		keys = []string{"USERPROFILE", "HOME"}
	}

	homes := make([]string, 0, len(keys))
	for _, key := range keys {
		homes = appendInstallHomeCandidate(homes, goos, getenv(key))
	}

	if len(homes) == 0 {
		if home, err := userHomeDirFn(); err == nil {
			homes = appendInstallHomeCandidate(homes, goos, home)
		}
	}
	return homes
}

func installPathEqual(goos string, a string, b string) bool {
	if strings.EqualFold(goos, "windows") {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func appendInstallCandidate(candidates []string, goos string, candidate string) []string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return candidates
	}
	for _, existing := range candidates {
		if installPathEqual(goos, existing, candidate) {
			return candidates
		}
	}
	return append(candidates, candidate)
}

func appendInstallHomeCandidate(homes []string, goos string, candidate string) []string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return homes
	}
	for _, existing := range homes {
		if installPathEqual(goos, existing, candidate) {
			return homes
		}
	}
	return append(homes, candidate)
}

func defaultRecoveredClaudeLauncherCandidate(goos string, getenv func(string) string) string {
	hostRoot := defaultManagedClaudeHostRoot(goos, getenv)
	if hostRoot == "" {
		return ""
	}
	return filepath.Join(hostRoot, "install-recovery", "claude")
}

func needsWindowsGitBash(goos string, output string) bool {
	if !strings.EqualFold(goos, "windows") {
		return false
	}
	lower := strings.ToLower(output)
	return strings.Contains(lower, "git-bash") || strings.Contains(lower, "claude_code_git_bash_path")
}

func parseInstalledClaudeLocation(output string) string {
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		idx := strings.Index(lower, "location:")
		if idx < 0 {
			continue
		}
		location := strings.TrimSpace(line[idx+len("location:"):])
		location = strings.Trim(location, `"'`)
		if location != "" {
			return location
		}
	}
	return ""
}

func parseInstalledGitBashLocation(output string) string {
	lines := strings.Split(output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		line = strings.Trim(line, `"'`)
		if line == "" {
			continue
		}
		if strings.HasSuffix(strings.ToLower(line), `\bash.exe`) || strings.HasSuffix(strings.ToLower(line), `/bash.exe`) {
			return line
		}
	}
	return ""
}

func resolveInstalledClaudeLocation(goos string, location string) string {
	location = strings.TrimSpace(location)
	if location == "" {
		return ""
	}

	candidates := []string{filepath.Clean(location)}
	if strings.EqualFold(goos, "windows") && filepath.Ext(location) == "" {
		for _, ext := range []string{".exe", ".cmd", ".bat", ".com"} {
			candidates = append(candidates, filepath.Clean(location+ext))
		}
	}

	for _, candidate := range candidates {
		if executableExists(candidate) {
			return candidate
		}
	}
	return ""
}

func resolveWindowsGitBashLocation(location string) string {
	location = strings.TrimSpace(location)
	if location == "" {
		return ""
	}
	if executableExists(location) {
		return filepath.Clean(location)
	}
	return ""
}

func findWindowsGitBashPath(getenv func(string) string) string {
	if path := resolveWindowsGitBashLocation(strings.TrimSpace(getenv("CLAUDE_CODE_GIT_BASH_PATH"))); path != "" {
		return path
	}

	candidates := []string{}
	if path := defaultWindowsPortableGitBashPath(getenv); path != "" {
		candidates = append(candidates, path)
	}
	if programFiles := strings.TrimSpace(getenv("ProgramFiles")); programFiles != "" {
		candidates = append(candidates, filepath.Join(programFiles, "Git", "bin", "bash.exe"))
	}
	if programFilesX86 := strings.TrimSpace(getenv("ProgramFiles(x86)")); programFilesX86 != "" {
		candidates = append(candidates, filepath.Join(programFilesX86, "Git", "bin", "bash.exe"))
	}
	if localAppData := strings.TrimSpace(resolveLocalAppDataDir(getenv)); localAppData != "" {
		candidates = append(candidates, filepath.Join(localAppData, "Programs", "Git", "bin", "bash.exe"))
	}
	if gitPath, err := claudeInstallLookPathFn("git"); err == nil {
		gitDir := filepath.Dir(filepath.Clean(gitPath))
		parent := filepath.Dir(gitDir)
		candidates = append(candidates, filepath.Join(parent, "bin", "bash.exe"))
	}

	for _, candidate := range candidates {
		if path := resolveWindowsGitBashLocation(candidate); path != "" {
			return path
		}
	}
	return ""
}

func defaultWindowsPortableGitBashPath(getenv func(string) string) string {
	localAppData := strings.TrimSpace(resolveLocalAppDataDir(getenv))
	if localAppData == "" {
		return ""
	}
	return filepath.Join(localAppData, "claude-proxy", "git", "current", "bin", "bash.exe")
}

func resolveLocalAppDataDir(getenv func(string) string) string {
	if localAppData := strings.TrimSpace(getenv("LOCALAPPDATA")); localAppData != "" {
		return localAppData
	}
	if home := strings.TrimSpace(getenv("USERPROFILE")); home != "" {
		return filepath.Join(home, "AppData", "Local")
	}
	if home := strings.TrimSpace(getenv("HOME")); home != "" {
		return filepath.Join(home, "AppData", "Local")
	}
	return ""
}

func getenvWithInstallOverrides(base func(string) string, overrides map[string]string) func(string) string {
	return func(key string) string {
		for overrideKey, value := range overrides {
			if sameInstallEnvKey(key, overrideKey) {
				return value
			}
		}
		return base(key)
	}
}

func envOverridesFromPairs(extraEnv []string) map[string]string {
	if len(extraEnv) == 0 {
		return nil
	}
	overrides := make(map[string]string, len(extraEnv))
	for _, kv := range extraEnv {
		key, value, ok := strings.Cut(kv, "=")
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		overrides[key] = value
	}
	return overrides
}

func setInstallEnvValue(env []string, key string, value string) []string {
	prefix := key + "="
	for i, entry := range env {
		k, _, ok := strings.Cut(entry, "=")
		if ok && sameInstallEnvKey(k, key) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func sameInstallEnvKey(a string, b string) bool {
	if strings.EqualFold(claudeInstallGOOS, "windows") {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func claudeInstallNotFoundError(goos string, installOutput string) error {
	msg := "claude installation finished but managed Claude binary was not found"
	if strings.EqualFold(goos, "windows") && strings.Contains(strings.ToLower(installOutput), "git-bash") {
		return fmt.Errorf("%s; %s", msg, windowsGitBashInstallHelp)
	}
	if location := parseInstalledClaudeLocation(installOutput); location != "" {
		return fmt.Errorf("%s; installer reported location %s", msg, location)
	}
	return fmt.Errorf("%s", msg)
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
