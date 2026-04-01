package claudehistory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestLoadHistoryIndexContextHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	idx, err := loadHistoryIndexContext(ctx, t.TempDir())
	if err != context.Canceled {
		t.Fatalf("expected context canceled, got idx=%#v err=%v", idx, err)
	}
}

func TestHistoryIndexHelpers(t *testing.T) {
	t.Run("loadHistoryIndex handles missing file", func(t *testing.T) {
		idx, err := loadHistoryIndex(t.TempDir())
		if err != nil {
			t.Fatalf("loadHistoryIndex error: %v", err)
		}
		if len(idx.sessions) != 0 {
			t.Fatalf("expected empty index, got %d entries", len(idx.sessions))
		}
	})

	t.Run("loadHistoryIndex parses and filters entries", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "history.jsonl")
		content := `not-json
{"display":"/clear","timestamp":1700000000000,"project":"/tmp/project","sessionId":"sess-1"}
{"display":"hello","timestamp":1700000001000,"project":"/tmp/project","sessionId":"sess-1"}
{"display":"later","timestamp":1700000002000,"project":"/tmp/project","sessionId":"sess-1"}`
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write history: %v", err)
		}

		idx, err := loadHistoryIndex(dir)
		if err != nil {
			t.Fatalf("loadHistoryIndex error: %v", err)
		}
		info, ok := idx.lookup("sess-1")
		if !ok {
			t.Fatalf("expected session to be indexed")
		}
		if info.ProjectPath != "/tmp/project" {
			t.Fatalf("expected project path, got %q", info.ProjectPath)
		}
		if info.FirstPrompt != "hello" {
			t.Fatalf("expected first prompt to pick earliest non-command, got %q", info.FirstPrompt)
		}
	})

	t.Run("lookup handles empty and missing", func(t *testing.T) {
		if _, ok := (historyIndex{}).lookup(""); ok {
			t.Fatalf("expected empty lookup to be false")
		}
		idx := historyIndex{sessions: map[string]*historySessionInfo{}}
		if _, ok := idx.lookup("missing"); ok {
			t.Fatalf("expected missing lookup to be false")
		}
	})

	t.Run("historyTimestamp parses numbers and strings", func(t *testing.T) {
		raw, _ := json.Marshal(int64(1700000000000))
		if ts := historyTimestamp(raw); ts.IsZero() {
			t.Fatalf("expected numeric timestamp to parse")
		}
		raw, _ = json.Marshal("1700000000000")
		if ts := historyTimestamp(raw); ts.IsZero() {
			t.Fatalf("expected string timestamp to parse")
		}
		raw, _ = json.Marshal("2026-01-01T00:00:00Z")
		if ts := historyTimestamp(raw); !ts.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
			t.Fatalf("expected RFC3339 parse, got %v", ts)
		}
		raw, _ = json.Marshal("invalid")
		if ts := historyTimestamp(raw); !ts.IsZero() {
			t.Fatalf("expected invalid timestamp to be zero")
		}
	})

	t.Run("shouldSkipFirstPrompt handles commands and warmup", func(t *testing.T) {
		if !shouldSkipFirstPrompt(" /clear") {
			t.Fatalf("expected /clear to be skipped")
		}
		if shouldSkipFirstPrompt("/help") {
			t.Fatalf("expected /help to be allowed")
		}
		if !shouldSkipFirstPrompt("Warmup") {
			t.Fatalf("expected Warmup to be skipped")
		}
	})
}

func TestLoadHistoryIndexPersistentCacheWarmStartMatchesColdLoad(t *testing.T) {
	useTempClaudeHistoryCacheDir(t)

	root := t.TempDir()
	path := filepath.Join(root, "history.jsonl")
	content := `{"display":"hello","timestamp":1700000001000,"project":"/tmp/project","sessionId":"sess-1"}
{"display":"later","timestamp":1700000002000,"project":"/tmp/project","sessionId":"sess-1"}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write history: %v", err)
	}

	coldIdx, err := loadHistoryIndex(root)
	if err != nil {
		t.Fatalf("loadHistoryIndex cold: %v", err)
	}

	resetHistoryIndexPersistentCacheForTest()

	warmIdx, err := loadHistoryIndex(root)
	if err != nil {
		t.Fatalf("loadHistoryIndex warm: %v", err)
	}
	if !reflect.DeepEqual(historyIndexValue(coldIdx), historyIndexValue(warmIdx)) {
		t.Fatalf("expected warm history index load to match cold load\ncold=%#v\nwarm=%#v", coldIdx, warmIdx)
	}
}

func historyIndexValue(idx historyIndex) map[string]historySessionInfo {
	out := make(map[string]historySessionInfo, len(idx.sessions))
	for sessionID, info := range idx.sessions {
		if info == nil {
			continue
		}
		out[sessionID] = *info
	}
	return out
}

func TestLoadHistoryIndexPersistentCacheIgnoresCorruptFile(t *testing.T) {
	cacheDir := useTempClaudeHistoryCacheDir(t)

	cachePath, err := historyIndexPersistentCachePath()
	if err != nil {
		t.Fatalf("historyIndexPersistentCachePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.WriteFile(cachePath, []byte("{not-json"), 0o600); err != nil {
		t.Fatalf("write corrupt cache: %v", err)
	}

	root := t.TempDir()
	path := filepath.Join(root, "history.jsonl")
	content := `{"display":"hello","timestamp":1700000001000,"project":"/tmp/project","sessionId":"sess-1"}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write history: %v", err)
	}

	idx, err := loadHistoryIndex(root)
	if err != nil {
		t.Fatalf("loadHistoryIndex: %v", err)
	}
	info, ok := idx.lookup("sess-1")
	if !ok || info.FirstPrompt != "hello" {
		t.Fatalf("unexpected history info after corrupt cache recovery: %#v ok=%v", info, ok)
	}

	data, err := os.ReadFile(filepath.Join(cacheDir, "claude-proxy", "claudehistory", "history_index_cache.json"))
	if err != nil {
		t.Fatalf("read repaired cache: %v", err)
	}
	if len(data) == 0 || data[0] != '{' {
		t.Fatalf("expected repaired JSON cache, got %q", string(data))
	}
}

func TestLoadHistoryIndexPersistentCacheMissesAfterReplacementWithSameMtime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("replacement semantics differ on Windows; covered by stronger cross-platform file key tests after implementation")
	}
	useTempClaudeHistoryCacheDir(t)

	root := t.TempDir()
	path := filepath.Join(root, "history.jsonl")
	originalContent := `{"display":"alpha","timestamp":1700000001000,"project":"/tmp/project","sessionId":"sess-1"}`
	if err := os.WriteFile(path, []byte(originalContent), 0o644); err != nil {
		t.Fatalf("write history: %v", err)
	}
	originalTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, originalTime, originalTime); err != nil {
		t.Fatalf("chtimes original: %v", err)
	}

	idx, err := loadHistoryIndex(root)
	if err != nil {
		t.Fatalf("loadHistoryIndex initial: %v", err)
	}
	info, ok := idx.lookup("sess-1")
	if !ok || info.FirstPrompt != "alpha" {
		t.Fatalf("unexpected initial info: %#v ok=%v", info, ok)
	}

	resetHistoryIndexPersistentCacheForTest()

	replacementPath := filepath.Join(root, "history-new.jsonl")
	replacementContent := `{"display":"omega","timestamp":1700000001000,"project":"/tmp/project","sessionId":"sess-1"}`
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

	idx, err = loadHistoryIndex(root)
	if err != nil {
		t.Fatalf("loadHistoryIndex after replacement: %v", err)
	}
	info, ok = idx.lookup("sess-1")
	if !ok || info.FirstPrompt != "omega" {
		t.Fatalf("expected replacement to miss stale persistent cache, got %#v ok=%v", info, ok)
	}
}
