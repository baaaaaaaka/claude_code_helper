package claudehistory

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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
	if sessionIDReads != 0 {
		t.Fatalf("expected 0 session id reads, got %d", sessionIDReads)
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
	if sessionIDReads != 0 {
		t.Fatalf("expected 0 session id reads, got %d", sessionIDReads)
	}
}

func TestSessionFileCacheInvalidatesOnFileReplacementWithSameMtime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("replacement semantics differ on Windows; covered by stronger cross-platform file key tests after implementation")
	}

	resetSessionFileCache()
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	originalContent := `{"type":"user","message":{"role":"user","content":"Alpha"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project","sessionId":"sess-1"}`
	if err := os.WriteFile(path, []byte(originalContent), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	originalTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, originalTime, originalTime); err != nil {
		t.Fatalf("chtimes original: %v", err)
	}

	meta, err := readSessionFileMetaCached(path)
	if err != nil {
		t.Fatalf("readSessionFileMetaCached error: %v", err)
	}
	if meta.FirstPrompt != "Alpha" {
		t.Fatalf("unexpected first prompt: %q", meta.FirstPrompt)
	}

	replacementPath := filepath.Join(dir, "replacement.jsonl")
	replacementContent := `{"type":"user","message":{"role":"user","content":"Omega"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project","sessionId":"sess-1"}`
	if len(replacementContent) != len(originalContent) {
		t.Fatalf("test requires same-size replacement, got %d vs %d", len(replacementContent), len(originalContent))
	}
	if err := os.WriteFile(replacementPath, []byte(replacementContent), 0o644); err != nil {
		t.Fatalf("write replacement: %v", err)
	}
	if err := os.Chtimes(replacementPath, originalTime, originalTime); err != nil {
		t.Fatalf("chtimes replacement: %v", err)
	}
	if err := os.Rename(replacementPath, path); err != nil {
		t.Fatalf("rename replacement: %v", err)
	}
	if err := os.Chtimes(path, originalTime, originalTime); err != nil {
		t.Fatalf("chtimes final: %v", err)
	}

	meta, err = readSessionFileMetaCached(path)
	if err != nil {
		t.Fatalf("readSessionFileMetaCached error after replacement: %v", err)
	}
	if meta.FirstPrompt != "Omega" {
		t.Fatalf("expected cache invalidation after replacement, got first prompt %q", meta.FirstPrompt)
	}
}

func TestSessionMetaPersistentCacheWarmStartMatchesColdLoad(t *testing.T) {
	useTempClaudeHistoryCacheDir(t)

	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-cache")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	mainPath := filepath.Join(projectDir, "sess-main.jsonl")
	mainContent := `{"type":"user","message":{"role":"user","content":"Hello"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project"}
{"type":"assistant","message":{"role":"assistant","content":"Hi"},"timestamp":"2026-01-01T00:01:00Z","cwd":"/tmp/project"}`
	if err := os.WriteFile(mainPath, []byte(mainContent), 0o644); err != nil {
		t.Fatalf("write main session: %v", err)
	}

	snapshotPath := filepath.Join(projectDir, "sess-snapshot.jsonl")
	snapshotContent := `{"type":"file-history-snapshot","messageId":"snap-1","snapshot":{"messageId":"snap-1","trackedFileBackups":{},"timestamp":"2026-01-02T00:00:00Z"},"isSnapshotUpdate":false}`
	if err := os.WriteFile(snapshotPath, []byte(snapshotContent), 0o644); err != nil {
		t.Fatalf("write snapshot session: %v", err)
	}

	agentPath := filepath.Join(projectDir, "agent-abc.jsonl")
	agentContent := `{"type":"user","message":{"role":"user","content":"Sub task"},"timestamp":"2026-01-03T00:00:00Z","cwd":"/tmp/project","sessionId":"sess-main","isSidechain":true}`
	if err := os.WriteFile(agentPath, []byte(agentContent), 0o644); err != nil {
		t.Fatalf("write agent session: %v", err)
	}

	coldProjects, err := DiscoverProjects(root)
	if err != nil {
		t.Fatalf("DiscoverProjects cold error: %v", err)
	}
	coldMetaReads, _ := sessionFileCacheStatsSnapshot()
	if coldMetaReads == 0 {
		t.Fatalf("expected cold load to parse session files")
	}

	resetSessionFileCache()

	warmProjects, err := DiscoverProjects(root)
	if err != nil {
		t.Fatalf("DiscoverProjects warm error: %v", err)
	}
	warmMetaReads, _ := sessionFileCacheStatsSnapshot()
	if warmMetaReads != 0 {
		t.Fatalf("expected warm load to hit persistent cache, got %d file meta reads", warmMetaReads)
	}
	if !reflect.DeepEqual(coldProjects, warmProjects) {
		t.Fatalf("expected warm load to match cold load exactly\ncold=%#v\nwarm=%#v", coldProjects, warmProjects)
	}
}

func TestSessionMetaPersistentCacheIgnoresCorruptFile(t *testing.T) {
	useTempClaudeHistoryCacheDir(t)

	path := filepath.Join(t.TempDir(), "sess.jsonl")
	cachePath, err := sessionMetaPersistentCachePathForFile(path)
	if err != nil {
		t.Fatalf("sessionMetaPersistentCachePathForFile: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.WriteFile(cachePath, []byte("{not-json"), 0o600); err != nil {
		t.Fatalf("write corrupt cache: %v", err)
	}

	content := `{"type":"user","message":{"role":"user","content":"Hello"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project","sessionId":"sess-1"}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	ctx := withSessionMetaPersistentBatch(context.Background())
	meta, err := readSessionFileMetaCachedContext(ctx, path)
	if err != nil {
		t.Fatalf("readSessionFileMetaCachedContext: %v", err)
	}
	if meta.FirstPrompt != "Hello" {
		t.Fatalf("unexpected first prompt: %q", meta.FirstPrompt)
	}
	flushSessionMetaPersistentBatchContext(ctx)

	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("read repaired cache: %v", err)
	}
	if len(data) == 0 || data[0] != '{' {
		t.Fatalf("expected repaired JSON cache, got %q", string(data))
	}
}

func TestSessionMetaPersistentCacheMissesAfterReplacementWithSameMtime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("replacement semantics differ on Windows; covered by stronger cross-platform file key tests after implementation")
	}

	useTempClaudeHistoryCacheDir(t)
	resetSessionFileCache()

	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	originalContent := `{"type":"user","message":{"role":"user","content":"Alpha"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project","sessionId":"sess-1"}`
	if err := os.WriteFile(path, []byte(originalContent), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	originalTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, originalTime, originalTime); err != nil {
		t.Fatalf("chtimes original: %v", err)
	}

	ctx := withSessionMetaPersistentBatch(context.Background())
	meta, err := readSessionFileMetaCachedContext(ctx, path)
	if err != nil {
		t.Fatalf("initial read: %v", err)
	}
	if meta.FirstPrompt != "Alpha" {
		t.Fatalf("unexpected first prompt: %q", meta.FirstPrompt)
	}
	flushSessionMetaPersistentBatchContext(ctx)

	resetSessionFileCache()

	replacementPath := filepath.Join(dir, "replacement.jsonl")
	replacementContent := `{"type":"user","message":{"role":"user","content":"Omega"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project","sessionId":"sess-1"}`
	if len(replacementContent) != len(originalContent) {
		t.Fatalf("test requires same-size replacement, got %d vs %d", len(replacementContent), len(originalContent))
	}
	if err := os.WriteFile(replacementPath, []byte(replacementContent), 0o644); err != nil {
		t.Fatalf("write replacement: %v", err)
	}
	if err := os.Chtimes(replacementPath, originalTime, originalTime); err != nil {
		t.Fatalf("chtimes replacement: %v", err)
	}
	if err := os.Rename(replacementPath, path); err != nil {
		t.Fatalf("rename replacement: %v", err)
	}
	if err := os.Chtimes(path, originalTime, originalTime); err != nil {
		t.Fatalf("chtimes final: %v", err)
	}

	ctx = withSessionMetaPersistentBatch(context.Background())
	meta, err = readSessionFileMetaCachedContext(ctx, path)
	if err != nil {
		t.Fatalf("read after replacement: %v", err)
	}
	if meta.FirstPrompt != "Omega" {
		t.Fatalf("expected replacement to miss stale persistent cache, got %q", meta.FirstPrompt)
	}
	metaReads, _ := sessionFileCacheStatsSnapshot()
	if metaReads != 1 {
		t.Fatalf("expected replacement miss to parse once, got %d meta reads", metaReads)
	}
}
