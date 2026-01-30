package claudehistory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParentSessionIDForAgentFileUsesDirectory(t *testing.T) {
	root := t.TempDir()
	subagentsDir := filepath.Join(root, "sess-parent", "subagents")
	if err := os.MkdirAll(subagentsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	filePath := filepath.Join(subagentsDir, "agent-abc.jsonl")
	content := `{"type":"user","message":{"role":"user","content":"Sub task"},"timestamp":"2026-01-01T00:00:00Z","sessionId":"sess-other","isSidechain":true}`
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}

	got, err := parentSessionIDForAgentFile(filePath)
	if err != nil {
		t.Fatalf("parentSessionIDForAgentFile error: %v", err)
	}
	if got != "sess-parent" {
		t.Fatalf("expected parent sess-parent, got %q", got)
	}
}

func TestParentSessionIDForAgentFileFallsBackToFile(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "agent-abc.jsonl")
	content := `{"type":"user","message":{"role":"user","content":"Sub task"},"timestamp":"2026-01-01T00:00:00Z","sessionId":"sess-main","isSidechain":true}`
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}

	got, err := parentSessionIDForAgentFile(filePath)
	if err != nil {
		t.Fatalf("parentSessionIDForAgentFile error: %v", err)
	}
	if got != "sess-main" {
		t.Fatalf("expected parent sess-main, got %q", got)
	}
}

func TestAttachSubagentsSkipsOrphans(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "agent-orphan.jsonl")
	content := `{"type":"user","message":{"role":"user","content":"Sub task"},"timestamp":"2026-01-01T00:00:00Z","sessionId":"sess-orphan","isSidechain":true}`
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}

	sessions := []Session{{SessionID: "sess-main"}}
	out, err := attachSubagents(root, sessions, false)
	if err != nil {
		t.Fatalf("attachSubagents error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 session, got %d", len(out))
	}
	if len(out[0].Subagents) != 0 {
		t.Fatalf("expected no subagents attached, got %d", len(out[0].Subagents))
	}
}

func TestAttachSubagentsSortsByModified(t *testing.T) {
	root := t.TempDir()
	olderPath := filepath.Join(root, "agent-old.jsonl")
	older := `{"type":"user","message":{"role":"user","content":"older"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project","sessionId":"sess-main","isSidechain":true}`
	if err := os.WriteFile(olderPath, []byte(older), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}
	newerPath := filepath.Join(root, "agent-new.jsonl")
	newer := `{"type":"user","message":{"role":"user","content":"newer"},"timestamp":"2026-01-02T00:00:00Z","cwd":"/tmp/project","sessionId":"sess-main","isSidechain":true}`
	if err := os.WriteFile(newerPath, []byte(newer), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}

	sessions := []Session{{SessionID: "sess-main"}}
	out, err := attachSubagents(root, sessions, false)
	if err != nil {
		t.Fatalf("attachSubagents error: %v", err)
	}
	if len(out) != 1 || len(out[0].Subagents) != 2 {
		t.Fatalf("expected 2 subagents, got %#v", out)
	}
	if out[0].Subagents[0].ModifiedAt.Before(out[0].Subagents[1].ModifiedAt) {
		t.Fatalf("expected subagents sorted by modified desc")
	}
}

func TestAttachSubagentsEmptySessions(t *testing.T) {
	out, err := attachSubagents(t.TempDir(), nil, false)
	if err != nil {
		t.Fatalf("attachSubagents error: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty sessions, got %d", len(out))
	}
}
