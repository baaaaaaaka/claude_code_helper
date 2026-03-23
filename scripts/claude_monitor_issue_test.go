package scripts_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type monitorIssuePayload struct {
	Body         string `json:"body"`
	BodyPath     string `json:"body_path"`
	ShouldCreate bool   `json:"should_create"`
	Title        string `json:"title"`
}

func writeMonitorResult(t *testing.T, dir string, platform string, results map[string]string) {
	t.Helper()
	path := filepath.Join(dir, platform+".json")
	data := map[string]any{
		"platform": platform,
		"results":  results,
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal %s: %v", platform, err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func runMonitorIssueScript(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	requirePython(t)
	repoRoot := repoRootFromScripts(t)
	script := filepath.Join(repoRoot, "scripts", "claude_monitor_issue.py")
	cmdArgs := append([]string{script}, args...)
	cmd := exec.Command("python3", cmdArgs...)
	cmd.Dir = repoRoot
	return cmd.CombinedOutput()
}

func TestClaudeMonitorIssueRejectsInvalidJSON(t *testing.T) {
	out, err := runMonitorIssueScript(t, "--missing-json", "{", "--proxy-tag", "v1")
	if err == nil {
		t.Fatalf("expected invalid JSON error")
	}
	if !strings.Contains(string(out), "Invalid JSON") {
		t.Fatalf("unexpected output: %s", string(out))
	}
}

func TestClaudeMonitorIssueRequiresProxyTag(t *testing.T) {
	out, err := runMonitorIssueScript(t, "--missing-json", "[]")
	if err == nil {
		t.Fatalf("expected proxy-tag required error")
	}
	if !strings.Contains(string(out), "--proxy-tag is required") {
		t.Fatalf("unexpected output: %s", string(out))
	}
}

func TestClaudeMonitorIssueSkipsWhenAllPlatformsPass(t *testing.T) {
	resultsDir := t.TempDir()
	version := "2.1.80"
	for _, platform := range []string{"linux", "mac", "windows", "centos7", "rockylinux8", "ubuntu20.04"} {
		writeMonitorResult(t, resultsDir, platform, map[string]string{version: "pass"})
	}

	outputPath := filepath.Join(t.TempDir(), "issue.json")
	bodyPath := filepath.Join(t.TempDir(), "issue.md")
	out, err := runMonitorIssueScript(
		t,
		"--missing-json", `["`+version+`"]`,
		"--proxy-tag", "v1.2.3",
		"--results-dir", resultsDir,
		"--output-json", outputPath,
		"--output-body", bodyPath,
	)
	if err != nil {
		t.Fatalf("run script: %v\n%s", err, string(out))
	}

	raw, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var payload monitorIssuePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.ShouldCreate {
		t.Fatalf("expected should_create=false, got %+v", payload)
	}
	if _, err := os.Stat(bodyPath); !os.IsNotExist(err) {
		t.Fatalf("expected no body file, stat err=%v", err)
	}
}

func TestClaudeMonitorIssueCreatesIssueForFailedResult(t *testing.T) {
	resultsDir := t.TempDir()
	version := "2.1.81"
	writeMonitorResult(t, resultsDir, "linux", map[string]string{version: "pass"})
	writeMonitorResult(t, resultsDir, "mac", map[string]string{version: "fail"})
	writeMonitorResult(t, resultsDir, "windows", map[string]string{version: "pass"})
	writeMonitorResult(t, resultsDir, "centos7", map[string]string{version: "pass"})
	writeMonitorResult(t, resultsDir, "rockylinux8", map[string]string{version: "pass"})

	outputDir := t.TempDir()
	outputPath := filepath.Join(outputDir, "issue.json")
	bodyPath := filepath.Join(outputDir, "issue.md")
	out, err := runMonitorIssueScript(
		t,
		"--missing-json", `["`+version+`"]`,
		"--proxy-tag", "v9.9.9",
		"--results-dir", resultsDir,
		"--workflow-run-url", "https://github.com/example/repo/actions/runs/123",
		"--output-json", outputPath,
		"--output-body", bodyPath,
	)
	if err != nil {
		t.Fatalf("run script: %v\n%s", err, string(out))
	}

	raw, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var payload monitorIssuePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if !payload.ShouldCreate {
		t.Fatalf("expected should_create=true, got %+v", payload)
	}
	if !strings.Contains(payload.Title, version) || !strings.Contains(payload.Title, "v9.9.9") {
		t.Fatalf("unexpected title: %s", payload.Title)
	}

	body, err := os.ReadFile(bodyPath)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, "| "+version+" | pass | fail | pass | pass | pass | missing |") {
		t.Fatalf("expected status table row, got:\n%s", text)
	}
	if !strings.Contains(text, "https://github.com/example/repo/actions/runs/123") {
		t.Fatalf("expected workflow URL in body, got:\n%s", text)
	}
}

func TestClaudeMonitorIssueCreatesIssueForFailedJobStatus(t *testing.T) {
	resultsDir := t.TempDir()
	version := "2.1.82"
	for _, platform := range []string{"linux", "mac", "windows", "centos7", "rockylinux8", "ubuntu20.04"} {
		writeMonitorResult(t, resultsDir, platform, map[string]string{version: "pass"})
	}

	outputDir := t.TempDir()
	outputPath := filepath.Join(outputDir, "issue.json")
	bodyPath := filepath.Join(outputDir, "issue.md")
	out, err := runMonitorIssueScript(
		t,
		"--missing-json", `["`+version+`"]`,
		"--proxy-tag", "v2.0.0",
		"--results-dir", resultsDir,
		"--job-status", "test=failure",
		"--job-status", "test-linux-distros=success",
		"--output-json", outputPath,
		"--output-body", bodyPath,
	)
	if err != nil {
		t.Fatalf("run script: %v\n%s", err, string(out))
	}

	raw, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var payload monitorIssuePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if !payload.ShouldCreate {
		t.Fatalf("expected should_create=true, got %+v", payload)
	}
	if payload.Title != "Claude Code monitor workflow failure on v2.0.0" {
		t.Fatalf("unexpected title: %s", payload.Title)
	}
	if !strings.Contains(payload.Body, "## Failed jobs") {
		t.Fatalf("expected failed jobs section, got:\n%s", payload.Body)
	}
	if !strings.Contains(payload.Body, "`test`: `failure`") {
		t.Fatalf("expected failed test job in body, got:\n%s", payload.Body)
	}
	if !strings.Contains(payload.Body, "No failing per-version result rows were captured") {
		t.Fatalf("expected workflow failure note, got:\n%s", payload.Body)
	}
}
