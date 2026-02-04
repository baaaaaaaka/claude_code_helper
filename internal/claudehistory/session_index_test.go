package claudehistory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionIndexHelpers(t *testing.T) {
	t.Run("readSessionFileMeta falls back to mod time", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "sess.jsonl")
		if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		mod := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatalf("chtimes: %v", err)
		}

		meta, err := readSessionFileMeta(path)
		if err != nil {
			t.Fatalf("readSessionFileMeta error: %v", err)
		}
		if meta.CreatedAt.IsZero() || meta.ModifiedAt.IsZero() {
			t.Fatalf("expected timestamps to be set")
		}
		if !meta.CreatedAt.Equal(mod) || !meta.ModifiedAt.Equal(mod) {
			t.Fatalf("expected mod time fallback, got %v %v", meta.CreatedAt, meta.ModifiedAt)
		}
	})

	t.Run("readSessionFileMeta tolerates invalid lines without newline", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "sess.jsonl")
		content := `{"type":"user","message":{"role":"user","content":"Hello"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project"}
{invalid-json}
{"type":"assistant","message":{"role":"assistant","content":"Hi"},"timestamp":"2026-01-01T00:01:00Z","cwd":"/tmp/project"}`
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		meta, err := readSessionFileMeta(path)
		if err != nil {
			t.Fatalf("readSessionFileMeta error: %v", err)
		}
		if meta.MessageCount != 2 {
			t.Fatalf("expected 2 messages, got %d", meta.MessageCount)
		}
		if meta.FirstPrompt != "Hello" {
			t.Fatalf("unexpected first prompt: %q", meta.FirstPrompt)
		}
		if meta.ParseErrors != 1 {
			t.Fatalf("expected 1 parse error, got %d", meta.ParseErrors)
		}
		if !meta.ModifiedAt.After(meta.CreatedAt) {
			t.Fatalf("expected modified after created")
		}
	})

	t.Run("readSessionFileMeta skips noisy first prompts", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "sess.jsonl")
		content := `{"type":"user","message":{"role":"user","content":"/clear"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project"}
{"type":"user","message":{"role":"user","content":"Real prompt"},"timestamp":"2026-01-01T00:00:01Z","cwd":"/tmp/project"}`
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		meta, err := readSessionFileMeta(path)
		if err != nil {
			t.Fatalf("readSessionFileMeta error: %v", err)
		}
		if meta.FirstPrompt != "Real prompt" {
			t.Fatalf("unexpected first prompt: %q", meta.FirstPrompt)
		}
	})

	t.Run("readSessionFileMeta detects snapshot-only sessions", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "sess.jsonl")
		content := `{"type":"file-history-snapshot","messageId":"snap-1","snapshot":{"messageId":"snap-1","trackedFileBackups":{},"timestamp":"2026-01-01T00:00:00Z"},"isSnapshotUpdate":false}`
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		meta, err := readSessionFileMeta(path)
		if err != nil {
			t.Fatalf("readSessionFileMeta error: %v", err)
		}
		if !meta.SnapshotOnly {
			t.Fatalf("expected snapshot-only to be true")
		}
	})

	t.Run("readSessionFileMeta ignores snapshot-only when mixed", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "sess.jsonl")
		content := `{"type":"file-history-snapshot","messageId":"snap-1","snapshot":{"messageId":"snap-1","trackedFileBackups":{},"timestamp":"2026-01-01T00:00:00Z"},"isSnapshotUpdate":false}
{"type":"user","message":{"role":"user","content":"Hello"},"timestamp":"2026-01-01T00:00:01Z","cwd":"/tmp/project"}`
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		meta, err := readSessionFileMeta(path)
		if err != nil {
			t.Fatalf("readSessionFileMeta error: %v", err)
		}
		if meta.SnapshotOnly {
			t.Fatalf("expected snapshot-only to be false for mixed content")
		}
	})

	t.Run("readSessionFileSessionID empty file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "sess.jsonl")
		if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		id, err := readSessionFileSessionID(path)
		if err != nil {
			t.Fatalf("readSessionFileSessionID error: %v", err)
		}
		if id != "" {
			t.Fatalf("expected empty session id, got %q", id)
		}
	})

	t.Run("resolveSessionIDFromFile prefers embedded id", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "fallback.jsonl")
		content := `{"sessionId":"sess-embedded"}`
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		id, err := resolveSessionIDFromFile(path)
		if err != nil {
			t.Fatalf("resolveSessionIDFromFile error: %v", err)
		}
		if id != "sess-embedded" {
			t.Fatalf("expected embedded session id, got %q", id)
		}
	})

	t.Run("resolveSessionIDFromFile falls back to filename", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "fallback-id.jsonl")
		content := `{"type":"user"}`
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		id, err := resolveSessionIDFromFile(path)
		if err != nil {
			t.Fatalf("resolveSessionIDFromFile error: %v", err)
		}
		if id != "fallback-id" {
			t.Fatalf("expected filename session id, got %q", id)
		}
	})

	t.Run("selectProjectPath resolves ties by name", func(t *testing.T) {
		sessions := []Session{
			{ProjectPath: "/b"},
			{ProjectPath: "/A"},
		}
		if got := selectProjectPath(sessions); got != "/A" {
			t.Fatalf("expected /A to win tie, got %q", got)
		}
	})

	t.Run("selectProjectPathExisting filters missing", func(t *testing.T) {
		dir := t.TempDir()
		sessions := []Session{
			{ProjectPath: filepath.Join(dir, "missing")},
			{ProjectPath: dir},
			{ProjectPath: dir},
		}
		if got := selectProjectPathExisting(sessions); got != dir {
			t.Fatalf("expected existing dir %q, got %q", dir, got)
		}
	})

	t.Run("resolveProjectPath prefers existing and falls back", func(t *testing.T) {
		dir := t.TempDir()
		sessions := []Session{{ProjectPath: dir}}
		if got := resolveProjectPath(dir, sessions); got != dir {
			t.Fatalf("expected preferred existing path, got %q", got)
		}

		preferred := filepath.Join(t.TempDir(), "missing")
		if got := resolveProjectPath(preferred, sessions); got != dir {
			t.Fatalf("expected existing session path fallback, got %q", got)
		}

		fallbackSessions := []Session{{ProjectPath: "/tmp/project"}}
		if got := resolveProjectPath(preferred, fallbackSessions); got != "/tmp/project" {
			t.Fatalf("expected session path when preferred missing, got %q", got)
		}

		if got := resolveProjectPath(preferred, nil); got != preferred {
			t.Fatalf("expected preferred to be returned when no existing paths, got %q", got)
		}
	})
}
