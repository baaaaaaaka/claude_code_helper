package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func requirePython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
}

func repoRootFromScripts(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Dir(cwd)
}

func TestClaudeCodeSyncRejectsInvalidJSON(t *testing.T) {
	requirePython(t)
	repoRoot := repoRootFromScripts(t)
	script := filepath.Join(repoRoot, "scripts", "claude_code_sync.py")

	cmd := exec.Command("python3", script, "--missing-json", "{", "--proxy-tag", "v1")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected invalid JSON error")
	}
	if !strings.Contains(string(out), "Invalid JSON") {
		t.Fatalf("unexpected output: %s", string(out))
	}
}

func TestClaudeCodeSyncRequiresProxyTag(t *testing.T) {
	requirePython(t)
	repoRoot := repoRootFromScripts(t)
	script := filepath.Join(repoRoot, "scripts", "claude_code_sync.py")

	cmd := exec.Command("python3", script, "--missing-json", "[]")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected proxy-tag required error")
	}
	if !strings.Contains(string(out), "--proxy-tag is required") {
		t.Fatalf("unexpected output: %s", string(out))
	}
}

func TestClaudeCodeSyncFailsOnWriteError(t *testing.T) {
	requirePython(t)
	if runtime.GOOS == "windows" {
		t.Skip("skip chmod permission test on windows")
	}
	repoRoot := repoRootFromScripts(t)
	script := filepath.Join(repoRoot, "scripts", "claude_code_sync.py")

	base := t.TempDir()
	readOnly := filepath.Join(base, "readonly")
	if err := os.MkdirAll(readOnly, 0o500); err != nil {
		t.Fatalf("mkdir readonly: %v", err)
	}
	tablePath := filepath.Join(readOnly, "compat.md")

	cmd := exec.Command("python3", script,
		"--missing-json", "[]",
		"--proxy-tag", "v1",
		"--table-path", tablePath,
		"--results-dir", filepath.Join(base, "results"),
	)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected write error, got output: %s", string(out))
	}
}

func TestClaudeCodeSyncAddsCentOS7ColumnWithoutBackfillingLegacyRows(t *testing.T) {
	requirePython(t)
	repoRoot := repoRootFromScripts(t)
	script := filepath.Join(repoRoot, "scripts", "claude_code_sync.py")

	base := t.TempDir()
	tablePath := filepath.Join(base, "compat.md")
	resultsDir := filepath.Join(base, "results")
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		t.Fatalf("mkdir results: %v", err)
	}
	if err := os.WriteFile(tablePath, []byte(`# Claude Code compatibility

Rows are added automatically after tests pass for a Claude Code release.

| Claude Code version | claude-proxy tag | linux | mac | windows | rockylinux8 | ubuntu20.04 |
| --- | --- | --- | --- | --- | --- | --- |
| 2.1.80 | v0.0.46 | pass | pass | pass | pass | pass |
`), 0o644); err != nil {
		t.Fatalf("write table: %v", err)
	}

	version := "2.1.81"
	for _, platform := range []string{"linux", "mac", "windows", "centos7", "rockylinux8", "ubuntu20.04"} {
		writeMonitorResult(t, resultsDir, platform, map[string]string{version: "pass"})
	}

	cmd := exec.Command("python3", script,
		"--missing-json", `["`+version+`"]`,
		"--proxy-tag", "v0.0.47",
		"--table-path", tablePath,
		"--results-dir", resultsDir,
	)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run script: %v\n%s", err, string(out))
	}

	raw, err := os.ReadFile(tablePath)
	if err != nil {
		t.Fatalf("read table: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "| Claude Code version | claude-proxy tag | linux | mac | windows | centos7 | rockylinux8 | ubuntu20.04 |") {
		t.Fatalf("expected centos7 column in table, got:\n%s", text)
	}
	if !strings.Contains(text, "| 2.1.80 | v0.0.46 | pass | pass | pass |  | pass | pass |") {
		t.Fatalf("expected legacy row without centos7 backfill, got:\n%s", text)
	}
	if !strings.Contains(text, "| 2.1.81 | v0.0.47 | pass | pass | pass | pass | pass | pass |") {
		t.Fatalf("expected latest row with centos7 status, got:\n%s", text)
	}
}
