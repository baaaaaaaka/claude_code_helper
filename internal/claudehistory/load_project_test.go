package claudehistory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProjectFromSessionFiles(t *testing.T) {
	dir := t.TempDir()
	projectPath := t.TempDir()
	sessionPath := filepath.Join(dir, "sess-1.jsonl")
	env := map[string]any{
		"type":      "user",
		"message":   map[string]any{"role": "user", "content": "Hello"},
		"timestamp": "2026-01-01T00:00:00Z",
		"cwd":       projectPath,
	}
	content, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}
	if err := os.WriteFile(sessionPath, append(content, '\n'), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	project, err := loadProjectFromSessionFiles(dir, "proj-1", historyIndex{})
	if err != nil {
		t.Fatalf("loadProjectFromSessionFiles error: %v", err)
	}
	if project.Key != "proj-1" {
		t.Fatalf("expected key proj-1, got %q", project.Key)
	}
	if project.Path != projectPath {
		t.Fatalf("expected project path %q, got %q", projectPath, project.Path)
	}
	if len(project.Sessions) != 1 || project.Sessions[0].SessionID != "sess-1" {
		t.Fatalf("unexpected sessions: %#v", project.Sessions)
	}
}
