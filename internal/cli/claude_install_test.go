package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
	"github.com/baaaaaaaka/claude_code_helper/internal/stack"
)

func TestInstallerCandidatesLinux(t *testing.T) {
	cmds := installerCandidates("linux")
	if len(cmds) != 2 {
		t.Fatalf("expected 2 linux installers, got %d", len(cmds))
	}
	if cmds[0].path != "bash" || cmds[1].path != "sh" {
		t.Fatalf("expected bash then sh installers, got %q then %q", cmds[0].path, cmds[1].path)
	}
	for i, cmd := range cmds {
		if len(cmd.args) < 2 {
			t.Fatalf("expected shell command args for candidate %d, got %v", i, cmd.args)
		}
		if cmd.args[0] != "-c" {
			t.Fatalf("expected non-login shell (-c) for candidate %d, got %q", i, cmd.args[0])
		}
		if strings.Contains(cmd.args[0], "l") {
			t.Fatalf("unexpected login-shell flag for candidate %d: %q", i, cmd.args[0])
		}
		if !strings.Contains(cmd.args[1], "curl") || !strings.Contains(cmd.args[1], "wget") {
			t.Fatalf("expected curl/wget fallback for candidate %d, got %q", i, cmd.args[1])
		}
		if !strings.Contains(cmd.args[1], "https://claude.ai/install.sh") {
			t.Fatalf("expected official install url for candidate %d, got %q", i, cmd.args[1])
		}
	}
}

func TestInstallerCandidatesWindows(t *testing.T) {
	cmds := installerCandidates("windows")
	if len(cmds) < 3 {
		t.Fatalf("expected at least 3 windows installers, got %d", len(cmds))
	}
	if cmds[0].path != "powershell" || cmds[1].path != "pwsh" {
		t.Fatalf("expected powershell then pwsh candidates, got %q then %q", cmds[0].path, cmds[1].path)
	}
	for i, cmd := range cmds[:2] {
		if len(cmd.args) < 5 {
			t.Fatalf("expected bootstrap command args for candidate %d, got %v", i, cmd.args)
		}
		bootstrap := cmd.args[4]
		if strings.Contains(bootstrap, "irm https://claude.ai/install.ps1 | iex") {
			t.Fatalf("candidate %d unexpectedly uses raw irm|iex bootstrap", i)
		}
		if !strings.Contains(bootstrap, "Invoke-RestMethod -Uri $installerUrl") {
			t.Fatalf("candidate %d missing script download step", i)
		}
		if !strings.Contains(bootstrap, "Installer endpoint returned HTML content") {
			t.Fatalf("candidate %d missing HTML guard", i)
		}
		if !strings.Contains(bootstrap, "Out-File -FilePath $logPath") {
			t.Fatalf("candidate %d missing error logging step", i)
		}
		if !strings.Contains(bootstrap, "Invoke-Expression $content") {
			t.Fatalf("candidate %d missing script execution step", i)
		}
	}
}

func TestWindowsGitBashBootstrapUsesOfficialDownloadPage(t *testing.T) {
	if strings.Contains(windowsGitBashBootstrap, "api.github.com/repos/git-for-windows/git/releases/latest") {
		t.Fatalf("expected Git Bash bootstrap to avoid GitHub releases API")
	}
	if !strings.Contains(windowsGitBashBootstrap, "https://git-scm.com/install/windows.html") {
		t.Fatalf("expected Git Bash bootstrap to use official Git for Windows download page")
	}
	if !strings.Contains(windowsGitBashBootstrap, "PortableGit-") {
		t.Fatalf("expected Git Bash bootstrap to resolve PortableGit assets")
	}
}

func TestInstallerAttemptLabelWithoutArgs(t *testing.T) {
	if got := installerAttemptLabel(installCmd{path: "powershell"}); got != "powershell" {
		t.Fatalf("expected plain path label, got %q", got)
	}
}

func TestResolveInstallerProxyRequiresProfile(t *testing.T) {
	if _, _, err := resolveInstallerProxy(context.Background(), installProxyOptions{UseProxy: true}); err == nil {
		t.Fatalf("expected error when proxy enabled without profile")
	}
}

func TestRunClaudeInstallerUsesProxyEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	instanceID := "inst-1"
	mux := http.NewServeMux()
	mux.HandleFunc("/_claude_proxy/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":         true,
			"instanceId": instanceID,
		})
	})
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(ln) }()
	t.Cleanup(func() { _ = server.Close() })

	dir := t.TempDir()
	outFile := filepath.Join(dir, "env.txt")
	script := filepath.Join(dir, "bash")
	scriptBody := "#!/bin/sh\nprintf \"%s\\n%s\\n\" \"$HTTP_PROXY\" \"$HTTPS_PROXY\" > \"$OUT_FILE\"\nexit 0\n"
	if err := os.WriteFile(script, []byte(scriptBody), 0o700); err != nil {
		t.Fatalf("write bash script: %v", err)
	}

	t.Setenv("OUT_FILE", outFile)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	profile := &config.Profile{ID: "profile-1"}
	opts := installProxyOptions{
		UseProxy:  true,
		Profile:   profile,
		Instances: []config.Instance{{ID: instanceID, ProfileID: profile.ID, Kind: config.InstanceKindDaemon, HTTPPort: port, DaemonPID: os.Getpid()}},
	}

	if err := runClaudeInstaller(context.Background(), io.Discard, opts); err != nil {
		t.Fatalf("runClaudeInstaller: %v", err)
	}

	content, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if lines[0] != proxyURL || lines[1] != proxyURL {
		t.Fatalf("expected proxy env %q, got %q", proxyURL, strings.Join(lines, ","))
	}
}

func TestRunClaudeInstallerFallsBackToNextCandidate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "sh-ran")
	bashScript := filepath.Join(dir, "bash")
	shScript := filepath.Join(dir, "sh")

	if err := os.WriteFile(bashScript, []byte("#!/bin/sh\nexit 42\n"), 0o700); err != nil {
		t.Fatalf("write bash script: %v", err)
	}
	shBody := "#!/bin/sh\nprintf \"ok\" > \"" + marker + "\"\nexit 0\n"
	if err := os.WriteFile(shScript, []byte(shBody), 0o700); err != nil {
		t.Fatalf("write sh script: %v", err)
	}

	t.Setenv("PATH", dir)

	if err := runClaudeInstaller(context.Background(), io.Discard, installProxyOptions{}); err != nil {
		t.Fatalf("runClaudeInstaller fallback error: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("expected fallback candidate to run: %v", err)
	}
}

func TestRunClaudeInstallerReportsAttemptDetails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	dir := t.TempDir()
	failScript := []byte("#!/bin/sh\nexit 7\n")
	for _, name := range []string{"bash", "sh"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, failScript, 0o700); err != nil {
			t.Fatalf("write %s script: %v", name, err)
		}
	}

	t.Setenv("PATH", dir)

	err := runClaudeInstaller(context.Background(), io.Discard, installProxyOptions{})
	if err == nil {
		t.Fatalf("expected installer failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "bash -c") || !strings.Contains(msg, "sh -c") {
		t.Fatalf("expected attempt details in error, got %q", msg)
	}
}

func TestRunClaudeInstallerRecoversEL7GlibcFailure(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("EL7 glibc recovery only applies on linux")
	}

	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	cacheRoot := filepath.Join(dir, "cache")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	existingLauncher := filepath.Join(home, ".local", "bin", "claude")
	if err := os.MkdirAll(filepath.Dir(existingLauncher), 0o755); err != nil {
		t.Fatalf("mkdir existing launcher dir: %v", err)
	}
	if err := os.WriteFile(existingLauncher, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write existing launcher: %v", err)
	}
	writeEL7InstallerFailureScripts(t, dir)

	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv("CLAUDE_PROXY_HOST_ID", "test-host")
	t.Setenv("PATH", dir)
	glibcCompatHostEligibleFn = func() bool { return true }
	t.Cleanup(func() { glibcCompatHostEligibleFn = isEL7GlibcCompatHost })

	var out bytes.Buffer
	if err := runClaudeInstaller(context.Background(), &out, installProxyOptions{}); err != nil {
		t.Fatalf("runClaudeInstaller: %v\noutput:\n%s", err, out.String())
	}

	wantBinary := filepath.Join(home, ".claude", "downloads", "claude-2.1.81-linux-x64")
	gotLauncher := filepath.Join(cacheRoot, "claude-proxy", "hosts", "test-host", "install-recovery", "claude")
	resolved, err := filepath.EvalSymlinks(gotLauncher)
	if err != nil {
		t.Fatalf("resolve launcher symlink: %v", err)
	}
	if !config.PathsEqual(resolved, wantBinary) {
		t.Fatalf("expected launcher to point to %q, got %q", wantBinary, resolved)
	}
	if !strings.Contains(out.String(), "Location: "+gotLauncher) {
		t.Fatalf("expected recovery output to report launcher location, got:\n%s", out.String())
	}
	if got := os.Getenv("PATH"); got != dir {
		t.Fatalf("expected PATH to remain unchanged, got %q", got)
	}
	content, err := os.ReadFile(existingLauncher)
	if err != nil {
		t.Fatalf("read existing launcher: %v", err)
	}
	if string(content) != "#!/bin/sh\nexit 0\n" {
		t.Fatalf("expected existing launcher to stay unchanged, got %q", string(content))
	}
}

func TestEnsureClaudeInstalledRecoversEL7GlibcFailure(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("EL7 glibc recovery only applies on linux")
	}

	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	cacheRoot := filepath.Join(dir, "cache")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	writeEL7InstallerFailureScripts(t, dir)

	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv("CLAUDE_PROXY_HOST_ID", "test-host")
	t.Setenv("PATH", dir)
	glibcCompatHostEligibleFn = func() bool { return true }
	t.Cleanup(func() { glibcCompatHostEligibleFn = isEL7GlibcCompatHost })

	var out bytes.Buffer
	got, err := ensureClaudeInstalled(context.Background(), "", &out, installProxyOptions{})
	if err != nil {
		t.Fatalf("ensureClaudeInstalled: %v\noutput:\n%s", err, out.String())
	}

	wantLauncher := filepath.Join(cacheRoot, "claude-proxy", "hosts", "test-host", "install-recovery", "claude")
	if !config.PathsEqual(got, wantLauncher) {
		t.Fatalf("expected launcher %q, got %q", wantLauncher, got)
	}
	resolved, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("resolve launcher symlink: %v", err)
	}
	wantBinary := filepath.Join(home, ".claude", "downloads", "claude-2.1.81-linux-x64")
	if !config.PathsEqual(resolved, wantBinary) {
		t.Fatalf("expected launcher target %q, got %q", wantBinary, resolved)
	}
}

func TestFindInstalledClaudePathFallsBackToUnixDefaultLocation(t *testing.T) {
	prevLookPath := claudeInstallLookPathFn
	t.Cleanup(func() { claudeInstallLookPathFn = prevLookPath })
	claudeInstallLookPathFn = func(file string) (string, error) {
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	}

	home := t.TempDir()
	claudePath := filepath.Join(home, ".local", "bin", "claude")
	if err := os.MkdirAll(filepath.Dir(claudePath), 0o755); err != nil {
		t.Fatalf("mkdir claude dir: %v", err)
	}
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write claude: %v", err)
	}

	getenv := func(key string) string {
		if key == "HOME" {
			return home
		}
		return ""
	}

	got, ok := findInstalledClaudePath("linux", "", getenv)
	if !ok {
		t.Fatalf("expected linux install path discovery to resolve %q", claudePath)
	}
	if got != claudePath {
		t.Fatalf("expected %q, got %q", claudePath, got)
	}
}

func TestFindManagedClaudePathFallsBackToUnixMigratedLocalInstall(t *testing.T) {
	home := t.TempDir()
	claudePath := filepath.Join(home, ".claude", "local", "claude")
	if err := os.MkdirAll(filepath.Dir(claudePath), 0o755); err != nil {
		t.Fatalf("mkdir claude dir: %v", err)
	}
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write claude: %v", err)
	}

	getenv := func(key string) string {
		if key == "HOME" {
			return home
		}
		return ""
	}

	got, ok := findManagedClaudePath("linux", "", getenv)
	if !ok {
		t.Fatalf("expected linux managed Claude discovery to resolve %q", claudePath)
	}
	if got != claudePath {
		t.Fatalf("expected %q, got %q", claudePath, got)
	}
}

func TestFindManagedClaudePathFallsBackToUserHomeDirWhenHomeEnvMissing(t *testing.T) {
	prevUserHomeDirFn := userHomeDirFn
	t.Cleanup(func() { userHomeDirFn = prevUserHomeDirFn })

	home := t.TempDir()
	userHomeDirFn = func() (string, error) { return home, nil }
	claudePath := filepath.Join(home, ".local", "bin", "claude")
	if err := os.MkdirAll(filepath.Dir(claudePath), 0o755); err != nil {
		t.Fatalf("mkdir claude dir: %v", err)
	}
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write claude: %v", err)
	}

	got, ok := findManagedClaudePath("linux", "", func(string) string { return "" })
	if !ok {
		t.Fatalf("expected managed Claude discovery to resolve %q via user home dir fallback", claudePath)
	}
	if got != claudePath {
		t.Fatalf("expected %q, got %q", claudePath, got)
	}
}

func TestFindManagedClaudePathIncludesRecoveredLauncher(t *testing.T) {
	home := t.TempDir()
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	hostID := "test-host"
	launcherPath := filepath.Join(cacheRoot, "claude-proxy", "hosts", hostID, "install-recovery", "claude")
	if err := os.MkdirAll(filepath.Dir(launcherPath), 0o755); err != nil {
		t.Fatalf("mkdir launcher dir: %v", err)
	}
	if err := os.WriteFile(launcherPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write launcher: %v", err)
	}

	getenv := func(key string) string {
		switch key {
		case "HOME":
			return home
		case "XDG_CACHE_HOME":
			return cacheRoot
		case claudeProxyHostIDEnv:
			return hostID
		default:
			return ""
		}
	}

	got, ok := findManagedClaudePath("linux", "", getenv)
	if !ok {
		t.Fatalf("expected managed Claude discovery to resolve recovered launcher %q", launcherPath)
	}
	if got != launcherPath {
		t.Fatalf("expected %q, got %q", launcherPath, got)
	}
}

func TestFindManagedClaudePathIncludesRecoveredLauncherWithoutHomeEnv(t *testing.T) {
	prevUserHomeDirFn := userHomeDirFn
	t.Cleanup(func() { userHomeDirFn = prevUserHomeDirFn })
	userHomeDirFn = func() (string, error) { return "", os.ErrNotExist }

	cacheRoot := filepath.Join(t.TempDir(), "cache")
	hostID := "test-host"
	launcherPath := filepath.Join(cacheRoot, "claude-proxy", "hosts", hostID, "install-recovery", "claude")
	if err := os.MkdirAll(filepath.Dir(launcherPath), 0o755); err != nil {
		t.Fatalf("mkdir launcher dir: %v", err)
	}
	if err := os.WriteFile(launcherPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write launcher: %v", err)
	}

	getenv := func(key string) string {
		switch key {
		case "XDG_CACHE_HOME":
			return cacheRoot
		case claudeProxyHostIDEnv:
			return hostID
		default:
			return ""
		}
	}

	got, ok := findManagedClaudePath("linux", "", getenv)
	if !ok {
		t.Fatalf("expected managed Claude discovery to resolve recovered launcher %q without home env", launcherPath)
	}
	if got != launcherPath {
		t.Fatalf("expected %q, got %q", launcherPath, got)
	}
}

func TestFindInstalledClaudePathPrefersLookPathWhenManagedLocationMissing(t *testing.T) {
	prevLookPath := claudeInstallLookPathFn
	t.Cleanup(func() { claudeInstallLookPathFn = prevLookPath })

	pathClaude := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(pathClaude, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write PATH claude: %v", err)
	}
	claudeInstallLookPathFn = func(file string) (string, error) {
		if file == "claude" {
			return pathClaude, nil
		}
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	}

	home := t.TempDir()
	getenv := func(key string) string {
		if key == "HOME" {
			return home
		}
		return ""
	}

	got, ok := findInstalledClaudePath("linux", "", getenv)
	if !ok {
		t.Fatalf("expected installed Claude discovery to resolve PATH Claude %q", pathClaude)
	}
	if got != pathClaude {
		t.Fatalf("expected %q, got %q", pathClaude, got)
	}
}

func TestEnsureClaudeInstalledIgnoresPathClaudeAndUsesManagedInstall(t *testing.T) {
	prevGOOS := claudeInstallGOOS
	prevInstaller := runClaudeInstallerWithEnvFn
	t.Cleanup(func() {
		claudeInstallGOOS = prevGOOS
		runClaudeInstallerWithEnvFn = prevInstaller
	})

	claudeInstallGOOS = "linux"
	home := t.TempDir()
	t.Setenv("HOME", home)

	pathDir := t.TempDir()
	pathClaude := filepath.Join(pathDir, "claude")
	if err := os.WriteFile(pathClaude, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write PATH claude: %v", err)
	}
	t.Setenv("PATH", pathDir)

	managedClaude := filepath.Join(home, ".local", "bin", "claude")
	installCalls := 0
	runClaudeInstallerWithEnvFn = func(ctx context.Context, out io.Writer, opts installProxyOptions, extraEnv []string) error {
		installCalls++
		if err := os.MkdirAll(filepath.Dir(managedClaude), 0o755); err != nil {
			t.Fatalf("mkdir managed claude dir: %v", err)
		}
		if err := os.WriteFile(managedClaude, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatalf("write managed claude: %v", err)
		}
		_, _ = io.WriteString(out, "Claude Code successfully installed!\n")
		return nil
	}

	got, err := ensureClaudeInstalled(context.Background(), "", io.Discard, installProxyOptions{})
	if err != nil {
		t.Fatalf("ensureClaudeInstalled error: %v", err)
	}
	if got != managedClaude {
		t.Fatalf("expected managed Claude %q, got %q", managedClaude, got)
	}
	if got == pathClaude {
		t.Fatalf("expected PATH Claude %q to be ignored", pathClaude)
	}
	if installCalls != 1 {
		t.Fatalf("expected installer to run once, got %d", installCalls)
	}
}

func TestEnsureClaudeInstalledReusesRecoveredLauncher(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("recovered launcher path currently applies on linux")
	}

	prevGOOS := claudeInstallGOOS
	t.Cleanup(func() { claudeInstallGOOS = prevGOOS })
	claudeInstallGOOS = "linux"

	home := t.TempDir()
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	hostID := "test-host"
	launcherPath := filepath.Join(cacheRoot, "claude-proxy", "hosts", hostID, "install-recovery", "claude")
	if err := os.MkdirAll(filepath.Dir(launcherPath), 0o755); err != nil {
		t.Fatalf("mkdir launcher dir: %v", err)
	}
	if err := os.WriteFile(launcherPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write launcher: %v", err)
	}

	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv("CLAUDE_PROXY_HOST_ID", hostID)
	t.Setenv("PATH", "")

	got, err := ensureClaudeInstalled(context.Background(), "", io.Discard, installProxyOptions{})
	if err != nil {
		t.Fatalf("ensureClaudeInstalled error: %v", err)
	}
	if got != launcherPath {
		t.Fatalf("expected recovered launcher %q, got %q", launcherPath, got)
	}
}

func writeEL7InstallerFailureScripts(t *testing.T, dir string) {
	t.Helper()

	script := "#!/bin/sh\n" +
		"bin=\"$HOME/.claude/downloads/claude-2.1.81-linux-x64\"\n" +
		"\"/bin/mkdir\" -p \"${bin%/*}\"\n" +
		": > \"$bin\"\n" +
		"\"/bin/chmod\" 755 \"$bin\"\n" +
		"echo \"Setting up Claude Code...\"\n" +
		"echo \"$bin: /lib64/libc.so.6: version \\`GLIBC_2.18' not found (required by $bin)\" >&2\n" +
		"echo \"$bin: /lib64/libc.so.6: version \\`GLIBC_2.24' not found (required by $bin)\" >&2\n" +
		"echo \"$bin: /lib64/libc.so.6: version \\`GLIBC_2.25' not found (required by $bin)\" >&2\n" +
		"exit 1\n"
	for _, name := range []string{"bash", "sh"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestInstallerCandidatesAndFailures(t *testing.T) {
	t.Run("unknown os has no installers", func(t *testing.T) {
		if cmds := installerCandidates("plan9"); len(cmds) != 0 {
			t.Fatalf("expected no installers, got %d", len(cmds))
		}
	})

	t.Run("runClaudeInstaller with no candidates", func(t *testing.T) {
		t.Setenv("PATH", "")
		err := runClaudeInstaller(context.Background(), io.Discard, installProxyOptions{})
		if err == nil {
			t.Fatalf("expected error when no installer candidates available")
		}
	})

	t.Run("ensureClaudeInstalled propagates installer error", func(t *testing.T) {
		t.Setenv("PATH", "")
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		_, err := ensureClaudeInstalled(context.Background(), "", io.Discard, installProxyOptions{})
		if err == nil {
			t.Fatalf("expected error when installer is unavailable")
		}
	})
}

func TestEnsureClaudeInstalledWithMissingPath(t *testing.T) {
	_, err := ensureClaudeInstalled(context.Background(), filepath.Join(t.TempDir(), "missing"), io.Discard, installProxyOptions{})
	if err == nil {
		t.Fatalf("expected error for missing claude path")
	}
}

func TestResolveInstallerProxyNoProxyAndCanceled(t *testing.T) {
	t.Run("use proxy disabled", func(t *testing.T) {
		url, cleanup, err := resolveInstallerProxy(context.Background(), installProxyOptions{UseProxy: false})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if url != "" || cleanup != nil {
			t.Fatalf("expected empty proxy and cleanup, got %q cleanup=%v", url, cleanup != nil)
		}
	})

	t.Run("context canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := resolveInstallerProxy(ctx, installProxyOptions{
			UseProxy: true,
			Profile:  &config.Profile{ID: "p1"},
		})
		if err == nil {
			t.Fatalf("expected context error")
		}
	})
}

func TestResolveInstallerProxyUsesReusableInstance(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/_claude_proxy/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":         true,
			"instanceId": "inst-1",
		})
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = ln.Close()
	})
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	tcp, err := net.ResolveTCPAddr("tcp", "127.0.0.1:"+portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	profile := &config.Profile{ID: "p1"}
	opts := installProxyOptions{
		UseProxy: true,
		Profile:  profile,
		Instances: []config.Instance{{
			ID:        "inst-1",
			ProfileID: profile.ID,
			Kind:      config.InstanceKindDaemon,
			HTTPPort:  tcp.Port,
			DaemonPID: os.Getpid(),
		}},
	}
	url, cleanup, err := resolveInstallerProxy(context.Background(), opts)
	if err != nil {
		t.Fatalf("resolveInstallerProxy error: %v", err)
	}
	if cleanup != nil {
		t.Fatalf("expected no cleanup for reusable instance")
	}
	want := fmt.Sprintf("http://127.0.0.1:%d", tcp.Port)
	if url != want {
		t.Fatalf("expected proxy URL %q, got %q", want, url)
	}
}

func TestResolveInstallerProxySkipsNonDaemonInstance(t *testing.T) {
	prevStart := claudeInstallStackStart
	t.Cleanup(func() { claudeInstallStackStart = prevStart })

	claudeInstallStackStart = func(profile config.Profile, instanceID string, opts stack.Options) (*stack.Stack, error) {
		return stack.NewStackForTest(18765, 29876), nil
	}

	profile := &config.Profile{ID: "p1"}
	opts := installProxyOptions{
		UseProxy: true,
		Profile:  profile,
		Instances: []config.Instance{{
			ID:        "inst-1",
			ProfileID: profile.ID,
			HTTPPort:  12345,
			DaemonPID: os.Getpid(),
		}},
	}
	url, cleanup, err := resolveInstallerProxy(context.Background(), opts)
	if err != nil {
		t.Fatalf("resolveInstallerProxy error: %v", err)
	}
	if cleanup == nil {
		t.Fatalf("expected cleanup for temporary stack")
	}
	t.Cleanup(func() {
		if err := cleanup(); err != nil {
			t.Fatalf("cleanup error: %v", err)
		}
	})
	if url != "http://127.0.0.1:18765" {
		t.Fatalf("expected temporary stack URL, got %q", url)
	}
}

func TestResolveInstallerProxyMissingProfile(t *testing.T) {
	_, _, err := resolveInstallerProxy(context.Background(), installProxyOptions{UseProxy: true})
	if err == nil {
		t.Fatalf("expected missing profile error")
	}
}

func TestEnsureClaudeInstalledUsesProvidedPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte("x"), 0o700); err != nil {
		t.Fatalf("write claude: %v", err)
	}
	got, err := ensureClaudeInstalled(context.Background(), path, io.Discard, installProxyOptions{})
	if err != nil {
		t.Fatalf("ensureClaudeInstalled error: %v", err)
	}
	if got != path {
		t.Fatalf("expected path %q, got %q", path, got)
	}
}

func TestParseInstalledClaudeLocation(t *testing.T) {
	output := `
Setting up Claude Code...
Claude Code successfully installed! Location: C:\Users\local-jawei\.local\bin\claude.exe
`

	got := parseInstalledClaudeLocation(output)
	want := `C:\Users\local-jawei\.local\bin\claude.exe`
	if got != want {
		t.Fatalf("expected location %q, got %q", want, got)
	}
}

func TestNeedsWindowsGitBash(t *testing.T) {
	if !needsWindowsGitBash("windows", "Claude Code on Windows requires git-bash") {
		t.Fatalf("expected git-bash message to match")
	}
	if !needsWindowsGitBash("windows", "Set CLAUDE_CODE_GIT_BASH_PATH=C:\\Program Files\\Git\\bin\\bash.exe") {
		t.Fatalf("expected env var hint to match")
	}
	if needsWindowsGitBash("linux", "requires git-bash") {
		t.Fatalf("did not expect non-windows match")
	}
}

func TestParseInstalledGitBashLocation(t *testing.T) {
	output := "Bootstrapping...\nC:\\Users\\local-jawei\\AppData\\Local\\claude-proxy\\git\\current\\bin\\bash.exe\n"
	got := parseInstalledGitBashLocation(output)
	want := `C:\Users\local-jawei\AppData\Local\claude-proxy\git\current\bin\bash.exe`
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestResolveInstalledClaudeLocationAddsWindowsExtensions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude.cmd")
	if err := os.WriteFile(path, []byte("@echo off\r\n"), 0o600); err != nil {
		t.Fatalf("write claude.cmd: %v", err)
	}

	got := resolveInstalledClaudeLocation("windows", filepath.Join(dir, "claude"))
	if got != path {
		t.Fatalf("expected resolved path %q, got %q", path, got)
	}
}

func TestDefaultClaudeInstallCandidatesWindows(t *testing.T) {
	getenv := func(key string) string {
		switch key {
		case "USERPROFILE":
			return `C:\Users\local-jawei`
		default:
			return ""
		}
	}

	candidates := defaultClaudeInstallCandidates("windows", getenv)
	if len(candidates) == 0 {
		t.Fatalf("expected default windows candidates")
	}
	want := filepath.Join(`C:\Users\local-jawei`, ".local", "bin", "claude.exe")
	found := false
	for _, candidate := range candidates {
		if strings.EqualFold(candidate, want) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %q in candidates, got %v", want, candidates)
	}
}

func TestFindWindowsGitBashPathUsesPortableDefault(t *testing.T) {
	localAppData := t.TempDir()
	bashPath := filepath.Join(localAppData, "claude-proxy", "git", "current", "bin", "bash.exe")
	if err := os.MkdirAll(filepath.Dir(bashPath), 0o755); err != nil {
		t.Fatalf("mkdir bash dir: %v", err)
	}
	if err := os.WriteFile(bashPath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write bash.exe: %v", err)
	}

	getenv := func(key string) string {
		switch key {
		case "LOCALAPPDATA":
			return localAppData
		default:
			return ""
		}
	}

	got := findWindowsGitBashPath(getenv)
	if got != bashPath {
		t.Fatalf("expected %q, got %q", bashPath, got)
	}
}

func TestFindWindowsGitBashPathFallsBackToGitExecutable(t *testing.T) {
	prevGOOS := claudeInstallGOOS
	prevLookPath := claudeInstallLookPathFn
	t.Cleanup(func() {
		claudeInstallGOOS = prevGOOS
		claudeInstallLookPathFn = prevLookPath
	})

	claudeInstallGOOS = "windows"
	root := t.TempDir()
	gitPath := filepath.Join(root, "Git", "cmd", "git.exe")
	bashPath := filepath.Join(root, "Git", "bin", "bash.exe")
	for _, path := range []string{gitPath, bashPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	claudeInstallLookPathFn = func(file string) (string, error) {
		if file == "git" {
			return gitPath, nil
		}
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	}

	got := findWindowsGitBashPath(func(string) string { return "" })
	if got != bashPath {
		t.Fatalf("expected %q, got %q", bashPath, got)
	}
}

func TestResolveLocalAppDataDirFallsBackToHome(t *testing.T) {
	got := resolveLocalAppDataDir(func(key string) string {
		if key == "HOME" {
			return `/tmp/example-home`
		}
		return ""
	})
	want := filepath.Join(`/tmp/example-home`, "AppData", "Local")
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestFindInstalledClaudePathFallsBackToWindowsDefaultLocation(t *testing.T) {
	t.Setenv("PATH", "")

	home := t.TempDir()
	claudePath := filepath.Join(home, ".local", "bin", "claude.exe")
	if err := os.MkdirAll(filepath.Dir(claudePath), 0o755); err != nil {
		t.Fatalf("mkdir claude dir: %v", err)
	}
	if err := os.WriteFile(claudePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write claude.exe: %v", err)
	}

	getenv := func(key string) string {
		switch key {
		case "USERPROFILE":
			return home
		default:
			return ""
		}
	}

	got, ok := findInstalledClaudePath("windows", "", getenv)
	if !ok {
		t.Fatalf("expected fallback location to resolve")
	}
	if got != claudePath {
		t.Fatalf("expected %q, got %q", claudePath, got)
	}
}

func hideWindowsGitBashDiscovery(t *testing.T) {
	t.Helper()
	t.Setenv("CLAUDE_CODE_GIT_BASH_PATH", "")
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ProgramFiles", t.TempDir())
	t.Setenv("ProgramFiles(x86)", t.TempDir())
}

func setIsolatedStubPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir)
	if runtime.GOOS == "windows" {
		t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	}
}

func TestEnsureWindowsGitBashUsesExistingPath(t *testing.T) {
	prevGOOS := claudeInstallGOOS
	t.Cleanup(func() { claudeInstallGOOS = prevGOOS })
	claudeInstallGOOS = "windows"

	hideWindowsGitBashDiscovery(t)
	bashPath := filepath.Join(t.TempDir(), "portable", "bash.exe")
	if err := os.MkdirAll(filepath.Dir(bashPath), 0o755); err != nil {
		t.Fatalf("mkdir bash dir: %v", err)
	}
	if err := os.WriteFile(bashPath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write bash.exe: %v", err)
	}
	t.Setenv("CLAUDE_CODE_GIT_BASH_PATH", bashPath)

	got, err := ensureWindowsGitBash(context.Background(), io.Discard, installProxyOptions{})
	if err != nil {
		t.Fatalf("ensureWindowsGitBash error: %v", err)
	}
	if got != bashPath {
		t.Fatalf("expected %q, got %q", bashPath, got)
	}
}

func TestEnsureWindowsGitBashFallsBackToNextCandidate(t *testing.T) {
	prevGOOS := claudeInstallGOOS
	prevLookPath := claudeInstallLookPathFn
	t.Cleanup(func() {
		claudeInstallGOOS = prevGOOS
		claudeInstallLookPathFn = prevLookPath
	})
	claudeInstallGOOS = "windows"
	claudeInstallLookPathFn = exec.LookPath

	dir := t.TempDir()
	hideWindowsGitBashDiscovery(t)
	bashPath := filepath.Join(t.TempDir(), "portable", "bash.exe")
	if err := os.MkdirAll(filepath.Dir(bashPath), 0o755); err != nil {
		t.Fatalf("mkdir bash dir: %v", err)
	}
	if err := os.WriteFile(bashPath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write bash.exe: %v", err)
	}
	t.Setenv("TARGET_BASH_PATH", bashPath)
	writeStub(t, dir, "powershell", "#!/bin/sh\nexit 1\n", "@echo off\r\nexit /b 1\r\n")
	writeStub(t, dir, "pwsh", "#!/bin/sh\nprintf '%s\\n' \"$TARGET_BASH_PATH\"\nexit 0\n", "@echo off\r\necho %TARGET_BASH_PATH%\r\nexit /b 0\r\n")
	setIsolatedStubPath(t, dir)

	got, err := ensureWindowsGitBash(context.Background(), io.Discard, installProxyOptions{})
	if err != nil {
		t.Fatalf("ensureWindowsGitBash error: %v", err)
	}
	if got != bashPath {
		t.Fatalf("expected %q, got %q", bashPath, got)
	}
}

func TestEnsureWindowsGitBashFailsWhenBootstrapDoesNotProduceBash(t *testing.T) {
	prevGOOS := claudeInstallGOOS
	prevLookPath := claudeInstallLookPathFn
	t.Cleanup(func() {
		claudeInstallGOOS = prevGOOS
		claudeInstallLookPathFn = prevLookPath
	})
	claudeInstallGOOS = "windows"
	claudeInstallLookPathFn = exec.LookPath

	dir := t.TempDir()
	hideWindowsGitBashDiscovery(t)
	writeStub(t, dir, "powershell", "#!/bin/sh\necho not-a-bash-path\nexit 0\n", "@echo off\r\necho not-a-bash-path\r\nexit /b 0\r\n")
	writeStub(t, dir, "pwsh", "#!/bin/sh\necho still-not-a-bash-path\nexit 0\n", "@echo off\r\necho still-not-a-bash-path\r\nexit /b 0\r\n")
	setIsolatedStubPath(t, dir)

	errOut := &strings.Builder{}
	_, err := ensureWindowsGitBash(context.Background(), errOut, installProxyOptions{})
	if err == nil {
		t.Fatalf("expected ensureWindowsGitBash failure")
	}
	if !strings.Contains(err.Error(), "bootstrap completed but bash.exe was not found") {
		t.Fatalf("expected missing bash error, got %v", err)
	}
}

func TestRunClaudeInstallerWithEnvInjectsWindowsGitBashPath(t *testing.T) {
	prevGOOS := claudeInstallGOOS
	prevLookPath := claudeInstallLookPathFn
	t.Cleanup(func() {
		claudeInstallGOOS = prevGOOS
		claudeInstallLookPathFn = prevLookPath
	})
	claudeInstallGOOS = "windows"
	claudeInstallLookPathFn = exec.LookPath

	dir := t.TempDir()
	outFile := filepath.Join(dir, "env.txt")
	setIsolatedStubPath(t, dir)
	bashPath := filepath.Join(t.TempDir(), "portable", "bash.exe")
	if err := os.MkdirAll(filepath.Dir(bashPath), 0o755); err != nil {
		t.Fatalf("mkdir bash dir: %v", err)
	}
	if err := os.WriteFile(bashPath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write bash.exe: %v", err)
	}
	t.Setenv("CLAUDE_CODE_GIT_BASH_PATH", bashPath)
	t.Setenv("OUT_FILE", outFile)
	if runtime.GOOS == "windows" {
		writeStub(t, dir, "powershell", "#!/bin/sh\nexit 0\n", "@echo off\r\n(\r\n  echo %CLAUDE_CODE_GIT_BASH_PATH%\r\n  echo %TEST_EXTRA%\r\n) > \"%OUT_FILE%\"\r\nexit /b 0\r\n")
	} else {
		writeStub(t, dir, "bash", "#!/bin/sh\nprintf \"%s\\n%s\\n\" \"$CLAUDE_CODE_GIT_BASH_PATH\" \"$TEST_EXTRA\" > \"$OUT_FILE\"\nexit 0\n", "@echo off\r\n")
	}

	if err := runClaudeInstallerWithEnv(context.Background(), io.Discard, installProxyOptions{}, []string{"TEST_EXTRA=ok", "MALFORMED"}); err != nil {
		t.Fatalf("runClaudeInstallerWithEnv error: %v", err)
	}
	content, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	normalized := strings.ReplaceAll(strings.TrimSpace(string(content)), "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if lines[0] != bashPath {
		t.Fatalf("expected bash path %q, got %q", bashPath, lines[0])
	}
	if lines[1] != "ok" {
		t.Fatalf("expected extra env %q, got %q", "ok", lines[1])
	}
}

func TestExportCurrentProcessGitBashPathSetenvError(t *testing.T) {
	prevGOOS := claudeInstallGOOS
	prevSetenv := claudeInstallSetenvFn
	t.Cleanup(func() {
		claudeInstallGOOS = prevGOOS
		claudeInstallSetenvFn = prevSetenv
	})
	claudeInstallGOOS = "windows"

	bashPath := filepath.Join(t.TempDir(), "portable", "bash.exe")
	if err := os.MkdirAll(filepath.Dir(bashPath), 0o755); err != nil {
		t.Fatalf("mkdir bash dir: %v", err)
	}
	if err := os.WriteFile(bashPath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write bash.exe: %v", err)
	}
	claudeInstallSetenvFn = func(key string, value string) error {
		return errors.New("boom")
	}

	err := exportCurrentProcessGitBashPath(bashPath)
	if err == nil || !strings.Contains(err.Error(), "set CLAUDE_CODE_GIT_BASH_PATH for current process") {
		t.Fatalf("expected setenv failure, got %v", err)
	}
}

func TestInstallEnvHelpersWindowsCaseInsensitive(t *testing.T) {
	prevGOOS := claudeInstallGOOS
	t.Cleanup(func() { claudeInstallGOOS = prevGOOS })
	claudeInstallGOOS = "windows"

	env := []string{"claude_code_git_bash_path=old", "OTHER=value"}
	updated := setInstallEnvValue(append([]string{}, env...), "CLAUDE_CODE_GIT_BASH_PATH", "new")
	if len(updated) != 2 {
		t.Fatalf("expected 2 env entries, got %d", len(updated))
	}
	if updated[0] != "CLAUDE_CODE_GIT_BASH_PATH=new" {
		t.Fatalf("expected replaced env entry, got %q", updated[0])
	}
	if !sameInstallEnvKey("Path", "path") {
		t.Fatalf("expected case-insensitive env key comparison on windows")
	}

	getenv := getenvWithInstallOverrides(func(key string) string { return "base:" + key }, map[string]string{
		"CLAUDE_CODE_GIT_BASH_PATH": "override",
	})
	if got := getenv("claude_code_git_bash_path"); got != "override" {
		t.Fatalf("expected override value, got %q", got)
	}
	if got := getenv("UNRELATED"); got != "base:UNRELATED" {
		t.Fatalf("expected base fallback, got %q", got)
	}
}

func TestSameInstallEnvKeyNonWindowsIsCaseSensitive(t *testing.T) {
	prevGOOS := claudeInstallGOOS
	t.Cleanup(func() { claudeInstallGOOS = prevGOOS })
	claudeInstallGOOS = "linux"

	if sameInstallEnvKey("Path", "path") {
		t.Fatalf("expected case-sensitive env key comparison outside windows")
	}
}

func TestEnsureClaudeInstalledReturnsExportGitBashError(t *testing.T) {
	prevGOOS := claudeInstallGOOS
	prevLookPath := claudeInstallLookPathFn
	prevSetenv := claudeInstallSetenvFn
	t.Cleanup(func() {
		claudeInstallGOOS = prevGOOS
		claudeInstallLookPathFn = prevLookPath
		claudeInstallSetenvFn = prevSetenv
	})
	claudeInstallGOOS = "windows"
	claudeInstallLookPathFn = func(file string) (string, error) {
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	}
	claudeInstallSetenvFn = func(key string, value string) error {
		return errors.New("cannot export")
	}

	localAppData := t.TempDir()
	home := t.TempDir()
	t.Setenv("LOCALAPPDATA", localAppData)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	bashPath := filepath.Join(localAppData, "claude-proxy", "git", "current", "bin", "bash.exe")
	if err := os.MkdirAll(filepath.Dir(bashPath), 0o755); err != nil {
		t.Fatalf("mkdir bash dir: %v", err)
	}
	if err := os.WriteFile(bashPath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write bash.exe: %v", err)
	}

	_, err := ensureClaudeInstalled(context.Background(), "", io.Discard, installProxyOptions{})
	if err == nil || !strings.Contains(err.Error(), "set CLAUDE_CODE_GIT_BASH_PATH for current process") {
		t.Fatalf("expected export git bash error, got %v", err)
	}
}

func TestEnsureClaudeInstalledExportsGitBashBeforeReturningManagedWindowsInstall(t *testing.T) {
	prevGOOS := claudeInstallGOOS
	prevSetenv := claudeInstallSetenvFn
	t.Cleanup(func() {
		claudeInstallGOOS = prevGOOS
		claudeInstallSetenvFn = prevSetenv
	})
	claudeInstallGOOS = "windows"

	home := t.TempDir()
	localAppData := filepath.Join(home, "AppData", "Local")
	claudePath := filepath.Join(home, ".local", "bin", "claude.exe")
	bashPath := filepath.Join(localAppData, "claude-proxy", "git", "current", "bin", "bash.exe")
	if err := os.MkdirAll(filepath.Dir(claudePath), 0o755); err != nil {
		t.Fatalf("mkdir claude dir: %v", err)
	}
	if err := os.WriteFile(claudePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write claude.exe: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(bashPath), 0o755); err != nil {
		t.Fatalf("mkdir bash dir: %v", err)
	}
	if err := os.WriteFile(bashPath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write bash.exe: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", localAppData)

	var exports []string
	claudeInstallSetenvFn = func(key string, value string) error {
		exports = append(exports, key+"="+value)
		return nil
	}

	got, err := ensureClaudeInstalled(context.Background(), "", io.Discard, installProxyOptions{})
	if err != nil {
		t.Fatalf("ensureClaudeInstalled error: %v", err)
	}
	if got != claudePath {
		t.Fatalf("expected managed Claude path %q, got %q", claudePath, got)
	}
	if len(exports) != 1 || exports[0] != "CLAUDE_CODE_GIT_BASH_PATH="+bashPath {
		t.Fatalf("expected CLAUDE_CODE_GIT_BASH_PATH export %q, got %v", bashPath, exports)
	}
}

func TestEnsureClaudeInstalledPropagatesRetryInstallerError(t *testing.T) {
	prevGOOS := claudeInstallGOOS
	prevInstaller := runClaudeInstallerWithEnvFn
	prevEnsureGitBash := ensureWindowsGitBashFn
	prevLookPath := claudeInstallLookPathFn
	t.Cleanup(func() {
		claudeInstallGOOS = prevGOOS
		runClaudeInstallerWithEnvFn = prevInstaller
		ensureWindowsGitBashFn = prevEnsureGitBash
		claudeInstallLookPathFn = prevLookPath
	})

	claudeInstallGOOS = "windows"
	claudeInstallLookPathFn = func(file string) (string, error) {
		switch file {
		case "claude", "git":
			return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
		default:
			return exec.LookPath(file)
		}
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	bashPath := filepath.Join(t.TempDir(), "portable", "bash.exe")
	if err := os.MkdirAll(filepath.Dir(bashPath), 0o755); err != nil {
		t.Fatalf("mkdir bash dir: %v", err)
	}
	if err := os.WriteFile(bashPath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write bash.exe: %v", err)
	}

	installCalls := 0
	runClaudeInstallerWithEnvFn = func(ctx context.Context, out io.Writer, opts installProxyOptions, extraEnv []string) error {
		installCalls++
		if installCalls == 1 {
			_, _ = io.WriteString(out, "Claude Code on Windows requires git-bash\n")
			return nil
		}
		return errors.New("retry failed")
	}
	ensureWindowsGitBashFn = func(ctx context.Context, out io.Writer, opts installProxyOptions) (string, error) {
		return bashPath, nil
	}

	_, err := ensureClaudeInstalled(context.Background(), "", io.Discard, installProxyOptions{})
	if err == nil || !strings.Contains(err.Error(), "retry failed") {
		t.Fatalf("expected retry installer error, got %v", err)
	}
}

func TestEnsureClaudeInstalledRetriesAfterInstallingWindowsGitBash(t *testing.T) {
	prevGOOS := claudeInstallGOOS
	prevInstaller := runClaudeInstallerWithEnvFn
	prevEnsureGitBash := ensureWindowsGitBashFn
	prevLookPath := claudeInstallLookPathFn
	prevSetenv := claudeInstallSetenvFn
	t.Cleanup(func() {
		claudeInstallGOOS = prevGOOS
		runClaudeInstallerWithEnvFn = prevInstaller
		ensureWindowsGitBashFn = prevEnsureGitBash
		claudeInstallLookPathFn = prevLookPath
		claudeInstallSetenvFn = prevSetenv
	})

	claudeInstallGOOS = "windows"
	t.Setenv("PATH", "")
	t.Setenv("CLAUDE_CODE_GIT_BASH_PATH", "")
	claudeInstallLookPathFn = func(file string) (string, error) {
		switch file {
		case "claude", "git":
			return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
		default:
			return exec.LookPath(file)
		}
	}

	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	claudePath := filepath.Join(home, ".local", "bin", "claude.exe")
	bashPath := filepath.Join(home, "AppData", "Local", "claude-proxy", "git", "current", "bin", "bash.exe")
	if err := os.MkdirAll(filepath.Dir(bashPath), 0o755); err != nil {
		t.Fatalf("mkdir bash dir: %v", err)
	}
	if err := os.WriteFile(bashPath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write bash.exe: %v", err)
	}

	installCalls := 0
	runClaudeInstallerWithEnvFn = func(ctx context.Context, out io.Writer, opts installProxyOptions, extraEnv []string) error {
		installCalls++
		switch installCalls {
		case 1:
			_, _ = io.WriteString(out, "Claude Code on Windows requires git-bash\n")
			return nil
		case 2:
			found := false
			for _, kv := range extraEnv {
				if kv == "CLAUDE_CODE_GIT_BASH_PATH="+bashPath {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected CLAUDE_CODE_GIT_BASH_PATH override, got %v", extraEnv)
			}
			if err := os.MkdirAll(filepath.Dir(claudePath), 0o755); err != nil {
				t.Fatalf("mkdir claude dir: %v", err)
			}
			if err := os.WriteFile(claudePath, []byte("x"), 0o600); err != nil {
				t.Fatalf("write claude.exe: %v", err)
			}
			_, _ = io.WriteString(out, "Claude Code successfully installed! Location: "+claudePath+"\n")
			return nil
		default:
			t.Fatalf("unexpected installer call %d", installCalls)
			return nil
		}
	}
	ensureWindowsGitBashFn = func(ctx context.Context, out io.Writer, opts installProxyOptions) (string, error) {
		return bashPath, nil
	}

	got, err := ensureClaudeInstalled(context.Background(), "", io.Discard, installProxyOptions{})
	if err != nil {
		t.Fatalf("ensureClaudeInstalled error: %v", err)
	}
	if got != claudePath {
		t.Fatalf("expected %q, got %q", claudePath, got)
	}
	if installCalls != 2 {
		t.Fatalf("expected 2 installer calls, got %d", installCalls)
	}
	if got := os.Getenv("CLAUDE_CODE_GIT_BASH_PATH"); got != bashPath {
		t.Fatalf("expected current process CLAUDE_CODE_GIT_BASH_PATH %q, got %q", bashPath, got)
	}
}

func TestClaudeInstallNotFoundErrorIncludesGitBashHelp(t *testing.T) {
	err := claudeInstallNotFoundError("windows", "Claude Code on Windows requires git-bash")
	if err == nil {
		t.Fatalf("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "managed Claude binary was not found") {
		t.Fatalf("expected binary not found message, got %q", msg)
	}
	if !strings.Contains(msg, "CLAUDE_CODE_GIT_BASH_PATH") {
		t.Fatalf("expected git-bash help in message, got %q", msg)
	}
}

func TestClaudeInstallNotFoundErrorIncludesReportedLocation(t *testing.T) {
	location := `C:\Users\local-jawei\.local\bin\claude.exe`
	err := claudeInstallNotFoundError("windows", "Claude Code successfully installed! Location: "+location)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), location) {
		t.Fatalf("expected reported location in error, got %q", err.Error())
	}
}
