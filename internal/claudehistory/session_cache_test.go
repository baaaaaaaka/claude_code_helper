package claudehistory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionFileCacheReusesReads(t *testing.T) {
	resetSessionFileCache()
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	content := `{"type":"user","message":{"role":"user","content":"Hello"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project","sessionId":"sess-1"}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if _, err := readSessionFileMetaCached(path); err != nil {
		t.Fatalf("readSessionFileMetaCached error: %v", err)
	}
	if _, err := readSessionFileMetaCached(path); err != nil {
		t.Fatalf("readSessionFileMetaCached error: %v", err)
	}
	if _, err := resolveSessionIDFromFileCached(path); err != nil {
		t.Fatalf("resolveSessionIDFromFileCached error: %v", err)
	}
	if _, err := resolveSessionIDFromFileCached(path); err != nil {
		t.Fatalf("resolveSessionIDFromFileCached error: %v", err)
	}

	metaReads, sessionIDReads := sessionFileCacheStatsSnapshot()
	if metaReads != 1 {
		t.Fatalf("expected 1 meta read, got %d", metaReads)
	}
	if sessionIDReads != 1 {
		t.Fatalf("expected 1 session id read, got %d", sessionIDReads)
	}
}

func TestSessionFileCacheInvalidatesOnMtimeChange(t *testing.T) {
	resetSessionFileCache()
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	content := `{"type":"user","message":{"role":"user","content":"Hello"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project","sessionId":"sess-1"}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := readSessionFileMetaCached(path); err != nil {
		t.Fatalf("readSessionFileMetaCached error: %v", err)
	}

	content = `{"type":"user","message":{"role":"user","content":"Updated"},"timestamp":"2026-01-02T00:00:00Z","cwd":"/tmp/project","sessionId":"sess-1"}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	mod := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if _, err := readSessionFileMetaCached(path); err != nil {
		t.Fatalf("readSessionFileMetaCached error: %v", err)
	}
	if _, err := resolveSessionIDFromFileCached(path); err != nil {
		t.Fatalf("resolveSessionIDFromFileCached error: %v", err)
	}

	metaReads, sessionIDReads := sessionFileCacheStatsSnapshot()
	if metaReads != 2 {
		t.Fatalf("expected 2 meta reads, got %d", metaReads)
	}
	if sessionIDReads != 1 {
		t.Fatalf("expected 1 session id read, got %d", sessionIDReads)
	}
}
