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
	pathValue := os.Getenv("Path")
	if pathAlreadySet {
		pathValue = installDir + ";" + pathValue
	}
	pathContainsInstall := containsPathEntry(pathValue, installDir)

	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath,
		"-Repo", repo,
		"-Version", "latest",
		"-InstallDir", installDir,
	)
	cmd.Env = append([]string{}, os.Environ()...)
	cmd.Env = append(cmd.Env,
		"CLAUDE_PROXY_API_BASE="+server.URL,
		"CLAUDE_PROXY_RELEASE_BASE="+server.URL,
		"CLAUDE_PROXY_PROFILE_PATH="+profilePath,
		"CLAUDE_PROXY_SKIP_PATH_UPDATE=1",
		"Path="+pathValue,
		"TEMP="+tempDir,
	)
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
		if strings.Contains(profileText, "$env:Path") && strings.Contains(profileText, installDir) {
			t.Fatalf("unexpected PATH update in profile")
		}
	} else {
		if !strings.Contains(profileText, "$env:Path") || !strings.Contains(profileText, installDir) {
			t.Fatalf("missing PATH update in profile")
		}
	}
}

func containsPathEntry(pathValue, entry string) bool {
	target := strings.TrimRight(entry, "\\")
	for _, part := range strings.Split(pathValue, ";") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		if strings.EqualFold(strings.TrimRight(part, "\\"), target) {
			return true
		}
	}
	return false
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
