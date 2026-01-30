package claudehistory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProjectFromSessionFiles(t *testing.T) {
	dir := t.TempDir()
	projectPath := t.TempDir()
	sessionPath := filepath.Join(dir, "sess-1.jsonl")
	content := `{"type":"user","message":{"role":"user","content":"Hello"},"timestamp":"2026-01-01T00:00:00Z","cwd":"` + projectPath + `"}`
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
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
