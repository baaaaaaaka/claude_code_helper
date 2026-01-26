//go:build windows

package installtest

import (
	"bytes"
	"crypto/sha256"
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
		"Path="+pathValue,
		"TEMP="+tempDir,
	)
	pathContainsInstall := pathInEnvViaPowerShell(t, cmd.Env, installDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install.ps1 failed: %v\n%s", err, string(output))
	}

	installed := filepath.Join(installDir, "claude-proxy.exe")
	got, err := os.ReadFile(installed)
	if err != nil {
		t.Fatalf("read installed: %v", err)
	}
	if !bytes.Equal(got, assetData) {
		t.Fatalf("installed payload mismatch")
	}

	profile, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	profileText := string(profile)
	if !strings.Contains(profileText, "Set-Alias -Name clp -Value claude-proxy") {
		t.Fatalf("missing clp alias in profile")
	}
	if pathContainsInstall {
		if hasPathLineForDir(profileText, installDir) {
			t.Fatalf("unexpected PATH update in profile")
		}
	} else {
		if !hasPathLineForDir(profileText, installDir) {
			t.Fatalf("missing PATH update in profile")
		}
	}
}

func hasPathLineForDir(profileText, installDir string) bool {
	for _, line := range strings.Split(profileText, "\n") {
		if !strings.Contains(line, "$env:Path") {
			continue
		}
		if strings.Contains(strings.ToLower(line), strings.ToLower(installDir)) {
			return true
		}
	}
	return false
}

func pathInEnvViaPowerShell(t *testing.T, env []string, installDir string) bool {
	t.Helper()
	script := `$target = [IO.Path]::GetFullPath($env:TEST_INSTALL_DIR);` +
		`$parts = $env:Path -split ';';` +
		`$found = $false;` +
		`foreach ($part in $parts) {` +
		` if ([string]::IsNullOrWhiteSpace($part)) { continue }` +
		` if ($part.TrimEnd('\') -ieq $target) { $found = $true; break }` +
		`}` +
		`if ($found) { 'true' } else { 'false' }`
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.Env = append([]string{}, env...)
	cmd.Env = append(cmd.Env, "TEST_INSTALL_DIR="+installDir)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("compute pathInEnv: %v", err)
	}
	return strings.EqualFold(strings.TrimSpace(string(out)), "true")
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
