package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCheckForUpdateAvailable(t *testing.T) {
	requireRuntimeAsset(t)
	tag := "v1.2.3"
	ver := "1.2.3"
	asset, err := assetName(ver, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatalf("assetName error: %v", err)
	}

	server := newReleaseServer(t, tag, asset, []byte("payload"))
	defer server.Close()

	restore := overrideGitHubBases(server.URL)
	defer restore()

	st := CheckForUpdate(context.Background(), CheckOptions{
		Repo:             "owner/name",
		InstalledVersion: "1.0.0",
		Timeout:          time.Second,
	})

	if !st.Supported {
		t.Fatalf("expected supported update check, got error=%q", st.Error)
	}
	if !st.UpdateAvailable {
		t.Fatalf("expected update available")
	}
	if st.RemoteVersion != ver {
		t.Fatalf("expected remote version %s, got %s", ver, st.RemoteVersion)
	}
}

func TestCheckForUpdateFallbackRedirect(t *testing.T) {
	requireRuntimeAsset(t)
	tag := "v1.4.0"
	ver := "1.4.0"

	server := newRedirectReleaseServer(t, "owner/name", tag)
	defer server.Close()

	restore := overrideGitHubBases(server.URL)
	defer restore()

	st := CheckForUpdate(context.Background(), CheckOptions{
		Repo:             "owner/name",
		InstalledVersion: "1.0.0",
		Timeout:          time.Second,
	})

	if !st.Supported {
		t.Fatalf("expected supported update check, got error=%q", st.Error)
	}
	if st.RemoteVersion != ver {
		t.Fatalf("expected remote version %s, got %s", ver, st.RemoteVersion)
	}
}

func TestCheckForUpdateRejectsDev(t *testing.T) {
	st := CheckForUpdate(context.Background(), CheckOptions{InstalledVersion: "dev"})
	if st.Supported {
		t.Fatalf("expected unsupported when local version is dev")
	}
}

func TestPerformUpdateExplicitVersion(t *testing.T) {
	requireRuntimeAsset(t)
	tag := "v1.2.3"
	ver := "1.2.3"
	asset, err := assetName(ver, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatalf("assetName error: %v", err)
	}
	payload := []byte("binary-payload")

	server := newReleaseServer(t, tag, asset, payload)
	defer server.Close()

	restore := overrideGitHubBases(server.URL)
	defer restore()

	tmpDir := t.TempDir()
	dest := filepath.Join(tmpDir, "claude-proxy")
	if err := os.WriteFile(dest, []byte("old"), 0o755); err != nil {
		t.Fatalf("write dest: %v", err)
	}

	res, err := PerformUpdate(context.Background(), UpdateOptions{
		Repo:        "owner/name",
		Version:     tag,
		InstallPath: dest,
		Timeout:     time.Second,
	})
	if err != nil {
		t.Fatalf("PerformUpdate error: %v", err)
	}
	if res.Version != ver {
		t.Fatalf("expected version %s, got %s", ver, res.Version)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("expected payload to be installed")
	}
}

func TestPerformUpdateChecksumMismatch(t *testing.T) {
	requireRuntimeAsset(t)
	tag := "v2.0.0"
	ver := "2.0.0"
	asset, err := assetName(ver, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatalf("assetName error: %v", err)
	}
	payload := []byte("binary-payload")

	server := newReleaseServerWithChecksum(t, tag, asset, payload, strings.Repeat("0", 64))
	defer server.Close()

	restore := overrideGitHubBases(server.URL)
	defer restore()

	dest := filepath.Join(t.TempDir(), "claude-proxy")
	_, err = PerformUpdate(context.Background(), UpdateOptions{
		Repo:        "owner/name",
		Version:     tag,
		InstallPath: dest,
		Timeout:     time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch error, got %v", err)
	}
}

func TestPerformUpdateLatest(t *testing.T) {
	requireRuntimeAsset(t)
	tag := "v3.1.0"
	ver := "3.1.0"
	asset, err := assetName(ver, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatalf("assetName error: %v", err)
	}
	payload := []byte("latest-binary")

	server := newReleaseServer(t, tag, asset, payload)
	defer server.Close()

	restore := overrideGitHubBases(server.URL)
	defer restore()

	dest := filepath.Join(t.TempDir(), "claude-proxy")
	_, err = PerformUpdate(context.Background(), UpdateOptions{
		Repo:        "owner/name",
		Version:     "latest",
		InstallPath: dest,
		Timeout:     time.Second,
	})
	if err != nil {
		t.Fatalf("PerformUpdate latest error: %v", err)
	}
}

func TestAssetNameErrors(t *testing.T) {
	if _, err := assetName("1.0.0", "solaris", "amd64"); err == nil {
		t.Fatalf("expected error for unsupported os")
	}
	if _, err := assetName("1.0.0", "linux", "arm64"); err == nil {
		t.Fatalf("expected error for linux arm64 asset")
	}
}

func requireRuntimeAsset(t *testing.T) {
	t.Helper()
	if _, err := assetName("1.0.0", runtime.GOOS, runtime.GOARCH); err != nil {
		t.Skipf("runtime asset unsupported: %v", err)
	}
}

func newReleaseServer(t *testing.T, tag, asset string, payload []byte) *httptest.Server {
	sum := sha256.Sum256(payload)
	checksum := hex.EncodeToString(sum[:])
	return newReleaseServerWithChecksum(t, tag, asset, payload, checksum)
}

func newReleaseServerWithChecksum(t *testing.T, tag, asset string, payload []byte, checksum string) *httptest.Server {
	t.Helper()
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/releases/latest"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"tag_name": "%s"}`, tag)
		case strings.Contains(r.URL.Path, "/checksums.txt"):
			w.Header().Set("Content-Type", "text/plain")
			_, _ = fmt.Fprintf(w, "%s  %s\n", checksum, asset)
		case strings.HasSuffix(r.URL.Path, "/"+asset) || strings.Contains(r.URL.Path, "/"+asset):
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}
	return httptest.NewServer(http.HandlerFunc(handler))
}

func newRedirectReleaseServer(t *testing.T, repo, tag string) *httptest.Server {
	t.Helper()
	tagPath := "/" + repo + "/releases/tag/" + tag
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/repos/") && strings.Contains(r.URL.Path, "/releases/latest"):
			http.Error(w, "api unavailable", http.StatusServiceUnavailable)
		case strings.Contains(r.URL.Path, "/releases/latest"):
			w.Header().Set("Location", tagPath)
			w.WriteHeader(http.StatusFound)
		case strings.Contains(r.URL.Path, tagPath):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		default:
			http.NotFound(w, r)
		}
	}
	return httptest.NewServer(http.HandlerFunc(handler))
}

func overrideGitHubBases(base string) func() {
	prevAPI := githubAPIBase
	prevRelease := githubReleaseBase
	githubAPIBase = base
	githubReleaseBase = base
	return func() {
		githubAPIBase = prevAPI
		githubReleaseBase = prevRelease
	}
}
