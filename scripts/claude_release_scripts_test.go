package scripts_test

import (
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
