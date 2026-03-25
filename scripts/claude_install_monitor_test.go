package scripts_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type installMonitorPayload struct {
	Body         string `json:"body"`
	BodyPath     string `json:"body_path"`
	ShouldCreate bool   `json:"should_create"`
	ShouldFail   bool   `json:"should_fail"`
	Title        string `json:"title"`
}

func writeJSONFile(t *testing.T, path string, data map[string]string) {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func runInstallMonitorScript(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	requirePython(t)
	repoRoot := repoRootFromScripts(t)
	script := filepath.Join(repoRoot, "scripts", "claude_install_monitor.py")
	cmdArgs := append([]string{script}, args...)
	cmd := exec.Command("python3", cmdArgs...)
	cmd.Dir = repoRoot
	return cmd.CombinedOutput()
}

func TestClaudeInstallMonitorSkipsWhenBaselineMatchesAndSmokePasses(t *testing.T) {
	base := t.TempDir()
	baselinePath := filepath.Join(base, "baseline.json")
	currentPath := filepath.Join(base, "current.json")
	payloadPath := filepath.Join(base, "payload.json")
	bodyPath := filepath.Join(base, "payload.md")
	current := map[string]string{
		"install_sh_url":      "https://claude.ai/install.sh",
		"install_ps1_url":     "https://claude.ai/install.ps1",
		"install_cmd_url":     "https://claude.ai/install.cmd",
		"gcs_bucket":          "https://storage.googleapis.com/example/releases",
		"latest_version":      "2.1.81",
		"latest_manifest_url": "https://storage.googleapis.com/example/releases/2.1.81/manifest.json",
	}
	writeJSONFile(t, baselinePath, map[string]string{
		"install_sh_url":  current["install_sh_url"],
		"install_ps1_url": current["install_ps1_url"],
		"install_cmd_url": current["install_cmd_url"],
		"gcs_bucket":      current["gcs_bucket"],
	})
	writeJSONFile(t, currentPath, current)

	out, err := runInstallMonitorScript(
		t,
		"--baseline-path", baselinePath,
		"--current-path", currentPath,
		"--resolve-status", "success",
		"--smoke-status", "success",
		"--output-json", payloadPath,
		"--output-body", bodyPath,
	)
	if err != nil {
		t.Fatalf("run script: %v\n%s", err, string(out))
	}

	raw, err := os.ReadFile(payloadPath)
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}
	var payload installMonitorPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.ShouldCreate || payload.ShouldFail {
		t.Fatalf("expected no issue payload, got %+v", payload)
	}
	if _, err := os.Stat(bodyPath); !os.IsNotExist(err) {
		t.Fatalf("expected no body file, stat err=%v", err)
	}
}

func TestClaudeInstallMonitorCreatesIssueForChangedBucket(t *testing.T) {
	base := t.TempDir()
	baselinePath := filepath.Join(base, "baseline.json")
	currentPath := filepath.Join(base, "current.json")
	payloadPath := filepath.Join(base, "payload.json")
	bodyPath := filepath.Join(base, "payload.md")
	writeJSONFile(t, baselinePath, map[string]string{
		"install_sh_url":  "https://claude.ai/install.sh",
		"install_ps1_url": "https://claude.ai/install.ps1",
		"install_cmd_url": "https://claude.ai/install.cmd",
		"gcs_bucket":      "https://storage.googleapis.com/example/releases-old",
	})
	writeJSONFile(t, currentPath, map[string]string{
		"install_sh_url":      "https://claude.ai/install.sh",
		"install_ps1_url":     "https://claude.ai/install.ps1",
		"install_cmd_url":     "https://claude.ai/install.cmd",
		"gcs_bucket":          "https://storage.googleapis.com/example/releases-new",
		"latest_version":      "2.1.81",
		"latest_manifest_url": "https://storage.googleapis.com/example/releases-new/2.1.81/manifest.json",
	})

	out, err := runInstallMonitorScript(
		t,
		"--baseline-path", baselinePath,
		"--current-path", currentPath,
		"--resolve-status", "success",
		"--smoke-status", "success",
		"--workflow-run-url", "https://github.com/example/repo/actions/runs/123",
		"--output-json", payloadPath,
		"--output-body", bodyPath,
	)
	if err != nil {
		t.Fatalf("run script: %v\n%s", err, string(out))
	}

	raw, err := os.ReadFile(payloadPath)
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}
	var payload installMonitorPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if !payload.ShouldCreate || !payload.ShouldFail {
		t.Fatalf("expected issue payload, got %+v", payload)
	}
	if payload.Title != "Claude Code install entry changed: gcs_bucket" {
		t.Fatalf("unexpected title: %s", payload.Title)
	}
	body, err := os.ReadFile(bodyPath)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, "`gcs_bucket`: `https://storage.googleapis.com/example/releases-old` -> `https://storage.googleapis.com/example/releases-new`") {
		t.Fatalf("expected changed bucket in body, got:\n%s", text)
	}
	if !strings.Contains(text, "https://github.com/example/repo/actions/runs/123") {
		t.Fatalf("expected workflow URL in body, got:\n%s", text)
	}
}

func TestClaudeInstallMonitorCreatesIssueForSmokeFailure(t *testing.T) {
	base := t.TempDir()
	baselinePath := filepath.Join(base, "baseline.json")
	currentPath := filepath.Join(base, "current.json")
	payloadPath := filepath.Join(base, "payload.json")
	current := map[string]string{
		"install_sh_url":      "https://claude.ai/install.sh",
		"install_ps1_url":     "https://claude.ai/install.ps1",
		"install_cmd_url":     "https://claude.ai/install.cmd",
		"gcs_bucket":          "https://storage.googleapis.com/example/releases",
		"latest_version":      "2.1.81",
		"latest_manifest_url": "https://storage.googleapis.com/example/releases/2.1.81/manifest.json",
	}
	writeJSONFile(t, baselinePath, map[string]string{
		"install_sh_url":  current["install_sh_url"],
		"install_ps1_url": current["install_ps1_url"],
		"install_cmd_url": current["install_cmd_url"],
		"gcs_bucket":      current["gcs_bucket"],
	})
	writeJSONFile(t, currentPath, current)

	out, err := runInstallMonitorScript(
		t,
		"--baseline-path", baselinePath,
		"--current-path", currentPath,
		"--resolve-status", "success",
		"--smoke-status", "failure",
		"--output-json", payloadPath,
	)
	if err != nil {
		t.Fatalf("run script: %v\n%s", err, string(out))
	}

	raw, err := os.ReadFile(payloadPath)
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}
	var payload installMonitorPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if !payload.ShouldCreate || !payload.ShouldFail {
		t.Fatalf("expected issue payload, got %+v", payload)
	}
	if payload.Title != "Claude Code install smoke test failed" {
		t.Fatalf("unexpected title: %s", payload.Title)
	}
	if !strings.Contains(payload.Body, "- Install smoke: `failure`") {
		t.Fatalf("expected smoke failure in body, got:\n%s", payload.Body)
	}
}
