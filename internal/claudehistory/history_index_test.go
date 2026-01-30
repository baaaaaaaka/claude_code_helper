package claudehistory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
{"display":"/command","timestamp":1700000000000,"project":"/tmp/project","sessionId":"sess-1"}
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

	t.Run("isHistoryCommandDisplay trims whitespace", func(t *testing.T) {
		if !isHistoryCommandDisplay(" /cmd") {
			t.Fatalf("expected command display to be true")
		}
		if isHistoryCommandDisplay("hello") {
			t.Fatalf("expected non-command display to be false")
		}
	})
}
