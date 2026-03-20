//go:build windows

package installtest

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallPs1LatestViaAPI(t *testing.T) {
	runInstallPs1(t, false, false)
}

func TestInstallPs1LatestViaRedirect(t *testing.T) {
	runInstallPs1(t, true, false)
}

func TestInstallPs1SkipsPathUpdateWhenAlreadySet(t *testing.T) {
	runInstallPs1(t, false, true)
}

func TestInstallPs1RemovesLegacyAlias(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("powershell"); err != nil {
		t.Skip("powershell not available")
	}

	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	scriptPath := filepath.Join(repoRoot, "install.ps1")

	repo := "owner/name"
	tag := "v1.2.3"
	verNoV := strings.TrimPrefix(tag, "v")
	asset := fmt.Sprintf("claude-proxy_%s_windows_amd64.exe", verNoV)
	assetData := []byte("fake-binary")
	checksum := sha256.Sum256(assetData)

	server := newInstallServer(t, repo, tag, asset, assetData, false, checksum)
	defer server.Close()

	homeDir := t.TempDir()
	installDir := t.TempDir()
	tempDir := t.TempDir()
	profilePath := filepath.Join(t.TempDir(), "profile.ps1")
	if err := os.WriteFile(profilePath, []byte("Set-Alias -Name clp -Value claude-proxy\n"), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	basePath := os.Getenv("SystemRoot")
	if basePath == "" {
		basePath = `C:\Windows`
	}
	pathValue := filepath.Join(basePath, "System32")
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath,
		"-Repo", repo,
		"-Version", "latest",
		"-InstallDir", installDir,
	)
	cmd.Env = append([]string{}, filterEnvWithoutKey(os.Environ(), "Path")...)
	cmd.Env = append(cmd.Env,
		"CLAUDE_PROXY_API_BASE="+server.URL,
		"CLAUDE_PROXY_RELEASE_BASE="+server.URL,
		"CLAUDE_PROXY_PROFILE_PATH="+profilePath,
		"CLAUDE_PROXY_SKIP_PATH_UPDATE=1",
		"USERPROFILE="+homeDir,
		"HOME="+homeDir,
		"Path="+pathValue,
		"TEMP="+tempDir,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install.ps1 failed: %v\n%s", err, string(output))
	}

	profile, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	if strings.Contains(string(profile), "Set-Alias -Name clp -Value claude-proxy") {
		t.Fatalf("legacy clp alias was not removed from profile")
	}
	commandType, _, err := resolveClpCommandViaPowerShell(cmd.Env, profilePath)
	if err != nil {
		t.Fatalf("resolve clp command: %v", err)
	}
	if !strings.EqualFold(commandType, "Function") {
		t.Fatalf("expected clp command type Function after profile load, got %q", commandType)
	}
}

func TestInstallPs1RefreshesClpFunctionWhenInstallDirChanges(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("powershell"); err != nil {
		t.Skip("powershell not available")
	}

	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	scriptPath := filepath.Join(repoRoot, "install.ps1")

	repo := "owner/name"
	tag := "v1.2.3"
	verNoV := strings.TrimPrefix(tag, "v")
	asset := fmt.Sprintf("claude-proxy_%s_windows_amd64.exe", verNoV)
	assetData := []byte("fake-binary")
	checksum := sha256.Sum256(assetData)

	server := newInstallServer(t, repo, tag, asset, assetData, false, checksum)
	defer server.Close()

	homeDir := t.TempDir()
	oldInstallDir := t.TempDir()
	newInstallDir := t.TempDir()
	tempDir := t.TempDir()
	profilePath := filepath.Join(t.TempDir(), "profile.ps1")
	oldExePath := filepath.Join(oldInstallDir, "claude-proxy.exe")
	oldBlock := fmt.Sprintf("# claude-proxy command clp %s\nif (Test-Path Alias:clp) {\n  Remove-Item Alias:clp -Force -ErrorAction SilentlyContinue\n}\nfunction global:clp {\n  & '%s' @args\n}\n", oldExePath, oldExePath)
	if err := os.WriteFile(profilePath, []byte(oldBlock), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	basePath := os.Getenv("SystemRoot")
	if basePath == "" {
		basePath = `C:\Windows`
	}
	pathValue := filepath.Join(basePath, "System32")
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath,
		"-Repo", repo,
		"-Version", "latest",
		"-InstallDir", newInstallDir,
	)
	cmd.Env = append([]string{}, filterEnvWithoutKey(os.Environ(), "Path")...)
	cmd.Env = append(cmd.Env,
		"CLAUDE_PROXY_API_BASE="+server.URL,
		"CLAUDE_PROXY_RELEASE_BASE="+server.URL,
		"CLAUDE_PROXY_PROFILE_PATH="+profilePath,
		"CLAUDE_PROXY_SKIP_PATH_UPDATE=1",
		"USERPROFILE="+homeDir,
		"HOME="+homeDir,
		"Path="+pathValue,
		"TEMP="+tempDir,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install.ps1 failed: %v\n%s", err, string(output))
	}

	newInstallDirResolved := resolvePathViaPowerShell(t, cmd.Env, newInstallDir)
	newExePath := filepath.Join(newInstallDirResolved, "claude-proxy.exe")
	profile, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	profileText := string(profile)
	if !strings.Contains(profileText, "# claude-proxy command clp "+newExePath) {
		t.Fatalf("missing refreshed clp marker for new install dir")
	}
	if !strings.Contains(profileText, newExePath) {
		t.Fatalf("missing refreshed clp function path")
	}
	commandType, definition, err := resolveClpCommandViaPowerShell(cmd.Env, profilePath)
	if err != nil {
		t.Fatalf("resolve clp command: %v", err)
	}
	if !strings.EqualFold(commandType, "Function") {
		t.Fatalf("expected clp command type Function after profile load, got %q", commandType)
	}
	if !strings.Contains(strings.ToLower(definition), strings.ToLower(newExePath)) {
		t.Fatalf("expected clp definition to reference new install dir, got %q", definition)
	}
	if strings.Contains(strings.ToLower(definition), strings.ToLower(oldExePath)) {
		t.Fatalf("clp definition still references old install dir: %q", definition)
	}
}

func runInstallPs1(t *testing.T, apiFail bool, pathAlreadySet bool) {
	t.Helper()
	if _, err := exec.LookPath("powershell"); err != nil {
		t.Skip("powershell not available")
	}

	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	scriptPath := filepath.Join(repoRoot, "install.ps1")

	repo := "owner/name"
	tag := "v1.2.3"
	verNoV := strings.TrimPrefix(tag, "v")
	asset := fmt.Sprintf("claude-proxy_%s_windows_amd64.exe", verNoV)
	assetData := []byte("fake-binary")
	checksum := sha256.Sum256(assetData)

	server := newInstallServer(t, repo, tag, asset, assetData, apiFail, checksum)
	defer server.Close()

	homeDir := t.TempDir()
	installDir := t.TempDir()
	tempDir := t.TempDir()
	profilePath := filepath.Join(t.TempDir(), "profile.ps1")
	basePath := os.Getenv("SystemRoot")
	if basePath == "" {
		basePath = `C:\Windows`
	}
	pathValue := filepath.Join(basePath, "System32")
	if pathAlreadySet {
		pathValue = installDir + ";" + pathValue
	}
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath,
		"-Repo", repo,
		"-Version", "latest",
		"-InstallDir", installDir,
	)
	cmd.Env = append([]string{}, filterEnvWithoutKey(os.Environ(), "Path")...)
	cmd.Env = append(cmd.Env,
		"CLAUDE_PROXY_API_BASE="+server.URL,
		"CLAUDE_PROXY_RELEASE_BASE="+server.URL,
		"CLAUDE_PROXY_PROFILE_PATH="+profilePath,
		"CLAUDE_PROXY_SKIP_PATH_UPDATE=1",
		"USERPROFILE="+homeDir,
		"HOME="+homeDir,
		"Path="+pathValue,
		"TEMP="+tempDir,
	)
	claudeBinDir := filepath.Join(homeDir, ".local", "bin")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install.ps1 failed: %v\n%s", err, string(output))
	}
	installDirResolved := resolvePathViaPowerShell(t, cmd.Env, installDir)
	claudeBinDirResolved := resolvePathViaPowerShell(t, cmd.Env, claudeBinDir)

	installed := filepath.Join(installDir, "claude-proxy.exe")
	got, err := os.ReadFile(installed)
	if err != nil {
		t.Fatalf("read installed: %v", err)
	}
	if !bytes.Equal(got, assetData) {
		t.Fatalf("installed payload mismatch")
	}
	clpCmd := filepath.Join(installDir, "clp.cmd")
	cmdData, err := os.ReadFile(clpCmd)
	if err != nil {
		t.Fatalf("read clp.cmd: %v", err)
	}
	if !strings.Contains(strings.ToLower(string(cmdData)), "claude-proxy.exe") {
		t.Fatalf("clp.cmd does not reference claude-proxy.exe")
	}
	clpSh := filepath.Join(installDir, "clp")
	shData, err := os.ReadFile(clpSh)
	if err != nil {
		t.Fatalf("read clp shim: %v", err)
	}
	if !strings.Contains(strings.ToLower(string(shData)), "claude-proxy.exe") {
		t.Fatalf("clp shim does not reference claude-proxy.exe")
	}

	profile, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	profileText := string(profile)
	if strings.Contains(profileText, "Set-Alias -Name clp -Value claude-proxy") {
		t.Fatalf("unexpected legacy clp alias in profile")
	}
	if !strings.Contains(profileText, "function global:clp") {
		t.Fatalf("missing clp function in profile")
	}
	if !strings.Contains(profileText, "Remove-Item Alias:clp") {
		t.Fatalf("clp function block does not clear Alias:clp")
	}
	if !strings.Contains(strings.ToLower(profileText), strings.ToLower("claude-proxy.exe")) {
		t.Fatalf("clp function does not reference claude-proxy.exe")
	}
	commandType, definition, err := resolveClpCommandViaPowerShell(cmd.Env, profilePath)
	if err != nil {
		t.Fatalf("resolve clp command: %v", err)
	}
	if !strings.EqualFold(commandType, "Function") {
		t.Fatalf("expected clp command type Function after profile load, got %q", commandType)
	}
	if !strings.Contains(strings.ToLower(definition), strings.ToLower(installDirResolved)) {
		t.Fatalf("expected clp definition to reference install dir, got %q", definition)
	}
	if pathAlreadySet {
		if hasPathMarker(profileText, installDirResolved) {
			t.Fatalf("unexpected install dir PATH update in profile")
		}
	} else {
		if !hasPathMarker(profileText, installDirResolved) {
			t.Fatalf("missing install dir PATH update in profile")
		}
	}
	if !hasPathMarker(profileText, claudeBinDirResolved) {
		t.Fatalf("missing claude PATH update in profile")
	}
}

func hasPathMarker(profileText, installDir string) bool {
	return strings.Contains(strings.ToLower(profileText), strings.ToLower("# claude-proxy PATH "+installDir))
}

func resolvePathViaPowerShell(t *testing.T, env []string, installDir string) string {
	t.Helper()
	script := `[IO.Path]::GetFullPath($env:TEST_INSTALL_DIR)`
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.Env = append([]string{}, env...)
	cmd.Env = append(cmd.Env, "TEST_INSTALL_DIR="+installDir)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("resolve path: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func resolveClpCommandViaPowerShell(env []string, profilePath string) (string, string, error) {
	script := `. $env:TEST_PROFILE_PATH;` +
		`$cmd = Get-Command clp -ErrorAction Stop;` +
		`$scriptBlock = '';` +
		`if ($null -ne $cmd.ScriptBlock) { $scriptBlock = [string]$cmd.ScriptBlock };` +
		`[pscustomobject]@{ CommandType = [string]$cmd.CommandType; Definition = [string]$cmd.Definition; ScriptBlock = $scriptBlock } | ConvertTo-Json -Compress`
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.Env = append([]string{}, env...)
	cmd.Env = append(cmd.Env, "TEST_PROFILE_PATH="+profilePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("%v\n%s", err, string(out))
	}
	var command struct {
		CommandType string `json:"CommandType"`
		Definition  string `json:"Definition"`
		ScriptBlock string `json:"ScriptBlock"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &command); err != nil {
		return "", "", fmt.Errorf("decode command info: %w\n%s", err, string(out))
	}
	definition := command.Definition
	if strings.TrimSpace(command.ScriptBlock) != "" {
		definition = command.ScriptBlock
	}
	return command.CommandType, definition, nil
}

func filterEnvWithoutKey(env []string, key string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if strings.EqualFold(k, key) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func newInstallServer(
	t *testing.T,
	repo string,
	tag string,
	asset string,
	assetData []byte,
	apiFail bool,
	checksum [32]byte,
) *httptest.Server {
	t.Helper()
	apiPath := "/repos/" + repo + "/releases/latest"
	latestPath := "/" + repo + "/releases/latest"
	tagPath := "/" + repo + "/releases/tag/" + tag
	assetPath := "/" + repo + "/releases/download/" + tag + "/" + asset
	checksumsPath := "/" + repo + "/releases/download/" + tag + "/checksums.txt"

	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == apiPath:
			if apiFail {
				http.Error(w, "api unavailable", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"tag_name": "%s"}`, tag)
		case r.URL.Path == latestPath:
			http.Redirect(w, r, tagPath, http.StatusFound)
		case r.URL.Path == tagPath:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		case r.URL.Path == assetPath:
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(assetData)
		case r.URL.Path == checksumsPath:
			w.Header().Set("Content-Type", "text/plain")
			_, _ = fmt.Fprintf(w, "%x  %s\n", checksum, asset)
		default:
			http.NotFound(w, r)
		}
	}

	return httptest.NewServer(http.HandlerFunc(handler))
}
