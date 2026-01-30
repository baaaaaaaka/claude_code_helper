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

func TestSessionFileUtilities(t *testing.T) {
	t.Run("isAgentSessionFileName edge cases", func(t *testing.T) {
		if !isAgentSessionFileName("agent-.jsonl") {
			t.Fatalf("expected agent-.jsonl to be treated as agent file")
		}
		if isAgentSessionFileName("Agent-abc.jsonl") {
			t.Fatalf("expected case-sensitive match")
		}
		if isAgentSessionFileName("agent-abc.txt") {
			t.Fatalf("expected non-jsonl suffix to be false")
		}
	})

	t.Run("resolveSessionFilePath handles empty and recursive", func(t *testing.T) {
		dir := t.TempDir()
		if path, err := resolveSessionFilePath(dir, "", false); err != nil || path != "" {
			t.Fatalf("expected empty session id to return empty path")
		}
		sub := filepath.Join(dir, "sub")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		target := filepath.Join(sub, "sess-1.jsonl")
		if err := os.WriteFile(target, []byte("{}"), 0o644); err != nil {
			t.Fatalf("write session: %v", err)
		}
		found, err := resolveSessionFilePath(dir, "sess-1", true)
		if err != nil || found != target {
			t.Fatalf("expected recursive match %q, got %q err=%v", target, found, err)
		}
	})

	t.Run("collectSessionFiles errors on missing dir", func(t *testing.T) {
		_, err := collectSessionFiles(filepath.Join(t.TempDir(), "missing"), false)
		if err == nil {
			t.Fatalf("expected error for missing dir")
		}
	})

	t.Run("rehydrateSessionsFromFiles updates and sorts", func(t *testing.T) {
		dir := t.TempDir()
		file1 := filepath.Join(dir, "sess-1.jsonl")
		file2 := filepath.Join(dir, "sess-2.jsonl")
		content1 := `{"type":"user","message":{"role":"user","content":"first"},"timestamp":"2026-01-02T00:00:00Z","cwd":"/tmp/project"}`
		content2 := `{"type":"user","message":{"role":"user","content":"second"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project"}`
		if err := os.WriteFile(file1, []byte(content1), 0o644); err != nil {
			t.Fatalf("write sess-1: %v", err)
		}
		if err := os.WriteFile(file2, []byte(content2), 0o644); err != nil {
			t.Fatalf("write sess-2: %v", err)
		}

		sessions := []Session{
			{SessionID: "sess-2"},
			{SessionID: "sess-1"},
		}
		out, valid, err := rehydrateSessionsFromFiles(dir, sessions, false)
		if err != nil {
			t.Fatalf("rehydrateSessionsFromFiles error: %v", err)
		}
		if valid != 2 {
			t.Fatalf("expected 2 valid files, got %d", valid)
		}
		if len(out) != 2 {
			t.Fatalf("expected 2 sessions, got %d", len(out))
		}
		if out[0].SessionID != "sess-1" {
			t.Fatalf("expected sessions sorted by modified desc, got %q", out[0].SessionID)
		}
		if out[0].FilePath == "" || out[0].FirstPrompt == "" {
			t.Fatalf("expected rehydrated fields, got %#v", out[0])
		}
	})
}
