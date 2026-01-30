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
