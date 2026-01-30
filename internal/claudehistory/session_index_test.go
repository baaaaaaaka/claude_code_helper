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

		if got := resolveProjectPath(preferred, nil); got != preferred {
			t.Fatalf("expected preferred to be returned when no existing paths, got %q", got)
		}
	})
}
