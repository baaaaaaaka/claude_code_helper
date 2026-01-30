package claudehistory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCollectSessionFilesExcludesAgentFiles(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		filepath.Join(dir, "sess-1.jsonl"),
		filepath.Join(dir, "agent-abc.jsonl"),
		filepath.Join(dir, "note.txt"),
	}
	for _, path := range files {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}
	subDir := filepath.Join(dir, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "sess-2.jsonl"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "agent-xyz.jsonl"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	nonRecursive, err := collectSessionFiles(dir, false)
	if err != nil {
		t.Fatalf("collectSessionFiles error: %v", err)
	}
	if len(nonRecursive) != 1 || filepath.Base(nonRecursive[0]) != "sess-1.jsonl" {
		t.Fatalf("unexpected non-recursive files: %#v", nonRecursive)
	}

	recursive, err := collectSessionFiles(dir, true)
	if err != nil {
		t.Fatalf("collectSessionFiles error: %v", err)
	}
	if len(recursive) != 2 {
		t.Fatalf("expected 2 session files, got %#v", recursive)
	}
}

func TestCollectAgentSessionFilesOnlyAgents(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		filepath.Join(dir, "sess-1.jsonl"),
		filepath.Join(dir, "agent-abc.jsonl"),
	}
	for _, path := range files {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}
	subDir := filepath.Join(dir, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "agent-xyz.jsonl"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	recursive, err := collectAgentSessionFiles(dir, true)
	if err != nil {
		t.Fatalf("collectAgentSessionFiles error: %v", err)
	}
	if len(recursive) != 2 {
		t.Fatalf("expected 2 agent files, got %#v", recursive)
	}
}

func TestReadSessionFileSessionIDSkipsInvalidLines(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "agent-abc.jsonl")
	content := "not-json\n{\"sessionId\":\"sess-1\"}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	sessionID, err := readSessionFileSessionID(filePath)
	if err != nil {
		t.Fatalf("readSessionFileSessionID error: %v", err)
	}
	if sessionID != "sess-1" {
		t.Fatalf("expected sess-1, got %q", sessionID)
	}
}
