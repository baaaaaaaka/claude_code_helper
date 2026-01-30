package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestHistoryListCmdOutputsJSON(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "projects"), 0o755); err != nil {
		t.Fatalf("mkdir projects: %v", err)
	}
	claudeDir := root
	cmd := newHistoryListCmd(&claudeDir)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	var payload struct {
		Projects []any `json:"projects"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if len(payload.Projects) != 0 {
		t.Fatalf("expected empty projects, got %#v", payload.Projects)
	}
}

func TestHistoryListCmdErrorsWhenMissingDir(t *testing.T) {
	claudeDir := filepath.Join(t.TempDir(), "missing")
	cmd := newHistoryListCmd(&claudeDir)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error for missing claude dir")
	}
}

func TestHistoryShowCmdSessionNotFound(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "projects"), 0o755); err != nil {
		t.Fatalf("mkdir projects: %v", err)
	}
	claudeDir := root
	cmd := newHistoryShowCmd(&claudeDir)
	cmd.SetArgs([]string{"missing"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected missing session error")
	}
}

func TestHistoryShowCmdMissingFile(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-1")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	index := `{"version":1,"entries":[{"sessionId":"sess-1","fullPath":"/missing/session.jsonl","messageCount":1,"projectPath":"/tmp/project"}],"originalPath":"/tmp/project"}`
	if err := os.WriteFile(filepath.Join(projectDir, "sessions-index.json"), []byte(index), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	claudeDir := root
	cmd := newHistoryShowCmd(&claudeDir)
	cmd.SetArgs([]string{"sess-1"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error for missing session file")
	}
}
