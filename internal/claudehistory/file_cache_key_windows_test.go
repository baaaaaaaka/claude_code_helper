//go:build windows

package claudehistory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCurrentFileCacheKeyChangesAfterSameSizeRewriteWithPreservedMtime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	original := []byte(`{"type":"user","message":{"role":"user","content":"Alpha"},"timestamp":"2026-01-01T00:00:00Z","cwd":"C:\\tmp","sessionId":"sess-1"}`)
	replacement := []byte(`{"type":"user","message":{"role":"user","content":"Omega"},"timestamp":"2026-01-01T00:00:00Z","cwd":"C:\\tmp","sessionId":"sess-1"}`)
	if len(original) != len(replacement) {
		t.Fatalf("test requires same-size contents, got %d vs %d", len(original), len(replacement))
	}
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatalf("write original file: %v", err)
	}

	originalTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, originalTime, originalTime); err != nil {
		t.Fatalf("chtimes original: %v", err)
	}

	before, err := currentFileCacheKey(path)
	if err != nil {
		t.Fatalf("currentFileCacheKey before: %v", err)
	}
	if !before.StrongIdentity {
		t.Fatalf("expected Windows cache key to provide strong identity")
	}

	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, replacement, 0o644); err != nil {
		t.Fatalf("rewrite file: %v", err)
	}
	if err := os.Chtimes(path, originalTime, originalTime); err != nil {
		t.Fatalf("chtimes replacement: %v", err)
	}

	after, err := currentFileCacheKey(path)
	if err != nil {
		t.Fatalf("currentFileCacheKey after: %v", err)
	}
	if before == after {
		t.Fatalf("expected same-size rewrite with preserved mtime to change cache key\nbefore=%#v\nafter=%#v", before, after)
	}
}
