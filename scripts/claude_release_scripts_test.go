package scripts_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireBash(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	return path
}

func TestClaudeReleaseInfoRejectsUnknownArg(t *testing.T) {
	bashPath := requireBash(t)
	repoRoot := repoRootFromScripts(t)
	script := filepath.Join(repoRoot, "scripts", "claude_release_info.sh")

	cmd := exec.Command(bashPath, script, "--unknown")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected unknown arg error")
	}
	if !strings.Contains(string(out), "Unknown argument") {
		t.Fatalf("unexpected output: %s", string(out))
	}
}

func TestClaudeReleaseInfoFailsWithoutDownloader(t *testing.T) {
	bashPath := requireBash(t)
	repoRoot := repoRootFromScripts(t)
	script := filepath.Join(repoRoot, "scripts", "claude_release_info.sh")

	cmd := exec.Command(bashPath, script, "--json")
	cmd.Dir = repoRoot
	cmd.Env = []string{"PATH="}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected downloader error")
	}
	if !strings.Contains(string(out), "Need curl or wget") {
		t.Fatalf("unexpected output: %s", string(out))
	}
}

func TestClaudeReleaseInfoJSONIncludesInstallCmdURL(t *testing.T) {
	bashPath := requireBash(t)
	repoRoot := repoRootFromScripts(t)
	script := filepath.Join(repoRoot, "scripts", "claude_release_info.sh")

	base := t.TempDir()
	bucketDir := filepath.Join(base, "bucket")
	if err := os.MkdirAll(bucketDir, 0o755); err != nil {
		t.Fatalf("mkdir bucket: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bucketDir, "latest"), []byte("2.1.81\n"), 0o644); err != nil {
		t.Fatalf("write latest: %v", err)
	}

	bucketURL := "file://" + filepath.ToSlash(bucketDir)
	installSh := filepath.Join(base, "install.sh")
	if err := os.WriteFile(installSh, []byte("GCS_BUCKET=\""+bucketURL+"\"\n"), 0o755); err != nil {
		t.Fatalf("write install.sh: %v", err)
	}
	installPs1 := filepath.Join(base, "install.ps1")
	if err := os.WriteFile(installPs1, []byte("$GCS_BUCKET = \""+bucketURL+"\"\n"), 0o644); err != nil {
		t.Fatalf("write install.ps1: %v", err)
	}

	cmd := exec.Command(
		bashPath,
		script,
		"--json",
		"--install-sh", "file://"+filepath.ToSlash(installSh),
		"--install-ps1", "file://"+filepath.ToSlash(installPs1),
		"--install-cmd", "https://claude.ai/install.cmd",
	)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run script: %v\n%s", err, string(out))
	}

	var payload map[string]string
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, string(out))
	}
	if got := payload["install_cmd_url"]; got != "https://claude.ai/install.cmd" {
		t.Fatalf("install_cmd_url=%q", got)
	}
	if got := payload["gcs_bucket"]; got != bucketURL {
		t.Fatalf("gcs_bucket=%q", got)
	}
	if got := payload["release_bucket"]; got != bucketURL {
		t.Fatalf("release_bucket=%q", got)
	}
	if got := payload["latest_version"]; got != "2.1.81" {
		t.Fatalf("latest_version=%q", got)
	}
}

func TestClaudeReleaseInfoJSONSupportsDownloadBaseURL(t *testing.T) {
	bashPath := requireBash(t)
	repoRoot := repoRootFromScripts(t)
	script := filepath.Join(repoRoot, "scripts", "claude_release_info.sh")

	base := t.TempDir()
	bucketDir := filepath.Join(base, "bucket")
	if err := os.MkdirAll(bucketDir, 0o755); err != nil {
		t.Fatalf("mkdir bucket: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bucketDir, "latest"), []byte("2.1.114\n"), 0o644); err != nil {
		t.Fatalf("write latest: %v", err)
	}

	bucketURL := "file://" + filepath.ToSlash(bucketDir)
	installSh := filepath.Join(base, "install.sh")
	if err := os.WriteFile(installSh, []byte("DOWNLOAD_BASE_URL=\""+bucketURL+"\"\n"), 0o755); err != nil {
		t.Fatalf("write install.sh: %v", err)
	}
	installPs1 := filepath.Join(base, "install.ps1")
	if err := os.WriteFile(installPs1, []byte("$DOWNLOAD_BASE_URL = \""+bucketURL+"\"\n"), 0o644); err != nil {
		t.Fatalf("write install.ps1: %v", err)
	}

	cmd := exec.Command(
		bashPath,
		script,
		"--json",
		"--install-sh", "file://"+filepath.ToSlash(installSh),
		"--install-ps1", "file://"+filepath.ToSlash(installPs1),
	)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run script: %v\n%s", err, string(out))
	}

	var payload map[string]string
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, string(out))
	}
	if got := payload["release_bucket"]; got != bucketURL {
		t.Fatalf("release_bucket=%q", got)
	}
	if got := payload["gcs_bucket"]; got != bucketURL {
		t.Fatalf("gcs_bucket=%q", got)
	}
	if got := payload["latest_version"]; got != "2.1.114" {
		t.Fatalf("latest_version=%q", got)
	}
	if got := payload["latest_manifest_url"]; got != bucketURL+"/2.1.114/manifest.json" {
		t.Fatalf("latest_manifest_url=%q", got)
	}
}

func TestClaudeReleaseVersionsRejectsUnknownArg(t *testing.T) {
	bashPath := requireBash(t)
	repoRoot := repoRootFromScripts(t)
	script := filepath.Join(repoRoot, "scripts", "claude_release_versions.sh")

	cmd := exec.Command(bashPath, script, "--unknown")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected unknown arg error")
	}
	if !strings.Contains(string(out), "Unknown argument") {
		t.Fatalf("unexpected output: %s", string(out))
	}
}

func TestClaudeReleaseVersionsRequiresPython(t *testing.T) {
	bashPath := requireBash(t)
	repoRoot := repoRootFromScripts(t)
	script := filepath.Join(repoRoot, "scripts", "claude_release_versions.sh")

	cmd := exec.Command(bashPath, script, "--bucket-url", "gs://bucket")
	cmd.Dir = repoRoot
	cmd.Env = []string{"PATH="}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected python missing error")
	}
	if !strings.Contains(string(out), "Need python3 or python") {
		t.Fatalf("unexpected output: %s", string(out))
	}
}

func TestClaudeReleaseVersionsSupportsNPMRegistrySource(t *testing.T) {
	bashPath := requireBash(t)
	repoRoot := repoRootFromScripts(t)
	script := filepath.Join(repoRoot, "scripts", "claude_release_versions.sh")

	registryPath := filepath.Join(t.TempDir(), "registry.json")
	registryJSON := `{"versions":{"2.1.18":{},"2.1.20":{},"2.1.19":{},"invalid":{},"2.1.100":{}}}`
	if err := os.WriteFile(registryPath, []byte(registryJSON), 0o644); err != nil {
		t.Fatalf("write registry fixture: %v", err)
	}

	cmd := exec.Command(
		bashPath,
		script,
		"--source", "npm",
		"--npm-registry-url", "file://"+filepath.ToSlash(registryPath),
		"--json",
		"--min-version", "2.1.19",
	)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run script: %v\n%s", err, string(out))
	}

	var versions []string
	if err := json.Unmarshal(out, &versions); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, string(out))
	}
	got := strings.Join(versions, ",")
	want := "2.1.19,2.1.20,2.1.100"
	if got != want {
		t.Fatalf("versions=%q, want %q", got, want)
	}
}

func TestClaudeReleaseVersionsFiltersNPMVersionsWithoutReleaseManifests(t *testing.T) {
	bashPath := requireBash(t)
	repoRoot := repoRootFromScripts(t)
	script := filepath.Join(repoRoot, "scripts", "claude_release_versions.sh")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/registry":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"versions":{"2.1.19":{},"2.1.20":{},"2.1.100":{}}}`))
		case "/releases/2.1.19/manifest.json", "/releases/2.1.100/manifest.json":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cmd := exec.Command(
		bashPath,
		script,
		"--source", "npm",
		"--npm-registry-url", server.URL+"/registry",
		"--release-bucket-url", server.URL+"/releases",
		"--json",
		"--min-version", "2.1.19",
	)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = string(exitErr.Stderr)
		}
		t.Fatalf("run script: %v\nstdout:\n%s\nstderr:\n%s", err, string(out), stderr)
	}

	var versions []string
	if err := json.Unmarshal(out, &versions); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, string(out))
	}
	got := strings.Join(versions, ",")
	want := "2.1.19,2.1.100"
	if got != want {
		t.Fatalf("versions=%q, want %q\noutput:\n%s", got, want, string(out))
	}
}

func TestClaudeReleaseVersionsRejectsUnknownSource(t *testing.T) {
	bashPath := requireBash(t)
	repoRoot := repoRootFromScripts(t)
	script := filepath.Join(repoRoot, "scripts", "claude_release_versions.sh")

	cmd := exec.Command(bashPath, script, "--source", "unknown", "--bucket-url", "gs://bucket")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected unknown source error")
	}
	if !strings.Contains(string(out), "Unknown version source") {
		t.Fatalf("unexpected output: %s", string(out))
	}
}
