package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultPath(t *testing.T) {
	path, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath error: %v", err)
	}
	if !strings.HasSuffix(path, filepath.Join("claude-proxy", "config.json")) {
		t.Fatalf("unexpected default path: %q", path)
	}
}

func TestStorePath(t *testing.T) {
	dir := t.TempDir()
	override := filepath.Join(dir, "custom.json")
	store, err := NewStore(override)
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	if store.Path() != override {
		t.Fatalf("expected store path %q, got %q", override, store.Path())
	}
}
