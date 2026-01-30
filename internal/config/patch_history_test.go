package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPatchHistoryPath(t *testing.T) {
	requireExePatchEnabled(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	path, err := PatchHistoryPath(configPath)
	if err != nil {
		t.Fatalf("PatchHistoryPath error: %v", err)
	}
	want := filepath.Join(dir, "patch_history.json")
	if path != want {
		t.Fatalf("expected %q, got %q", want, path)
	}
}

func TestPatchHistoryStoreLoadSave(t *testing.T) {
	requireExePatchEnabled(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	store, err := NewPatchHistoryStore(configPath)
	if err != nil {
		t.Fatalf("NewPatchHistoryStore error: %v", err)
	}

	entry := PatchHistoryEntry{
		Path:          filepath.Join(dir, "bin"),
		SpecsSHA256:   "spec",
		PatchedSHA256: "patched",
		ProxyVersion:  "v1",
	}
	if err := store.Update(func(h *PatchHistory) error {
		h.Upsert(entry)
		h.Upsert(PatchHistoryEntry{
			Path:          entry.Path,
			SpecsSHA256:   entry.SpecsSHA256,
			PatchedSHA256: "patched-2",
			ProxyVersion:  "v2",
		})
		return nil
	}); err != nil {
		t.Fatalf("Update error: %v", err)
	}

	history, err := store.Load()
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if len(history.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(history.Entries))
	}
	if history.IsPatched(entry.Path, entry.SpecsSHA256, entry.PatchedSHA256, "v1") {
		t.Fatalf("expected IsPatched to be false after overwrite")
	}
	if !history.IsPatched(entry.Path, entry.SpecsSHA256, "patched-2", "v2") {
		t.Fatalf("expected IsPatched to be true after overwrite")
	}
	if history.IsPatched(entry.Path, entry.SpecsSHA256, "patched-2", "v1") {
		t.Fatalf("expected IsPatched to be false for mismatched proxy version")
	}
	if history.IsPatched(entry.Path, entry.SpecsSHA256, "patched-2", "") {
		t.Fatalf("expected IsPatched to be false for empty proxy version")
	}
	if found, ok := history.Find(entry.Path, entry.SpecsSHA256); !ok {
		t.Fatalf("expected Find to return entry")
	} else if found.ProxyVersion != "v2" {
		t.Fatalf("expected Find to return latest proxy version, got %q", found.ProxyVersion)
	}

	if removed := history.Remove(entry.Path, entry.SpecsSHA256); !removed {
		t.Fatalf("expected Remove to succeed")
	}
	if history.IsPatched(entry.Path, entry.SpecsSHA256, entry.PatchedSHA256, "v1") {
		t.Fatalf("expected IsPatched to be false after Remove")
	}
	if _, ok := history.Find(entry.Path, entry.SpecsSHA256); ok {
		t.Fatalf("expected Find to be false after Remove")
	}
	if removed := history.Remove("missing", "missing"); removed {
		t.Fatalf("expected Remove to be false for missing entry")
	}
}

func TestPatchHistoryLoadInvalidJSON(t *testing.T) {
	requireExePatchEnabled(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "patch_history.json")
	if err := os.WriteFile(path, []byte("{invalid json"), 0o600); err != nil {
		t.Fatalf("write invalid json: %v", err)
	}

	store := &PatchHistoryStore{
		path: path,
		lock: nil,
	}
	if _, err := store.loadUnlocked(); err == nil {
		t.Fatalf("expected loadUnlocked to fail on invalid json")
	}
}

func TestPatchHistoryVersionMismatch(t *testing.T) {
	requireExePatchEnabled(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "patch_history.json")
	if err := os.WriteFile(path, []byte("{\"version\": 99, \"entries\": []}"), 0o600); err != nil {
		t.Fatalf("write version json: %v", err)
	}

	store := &PatchHistoryStore{
		path: path,
		lock: nil,
	}
	if _, err := store.loadUnlocked(); err == nil {
		t.Fatalf("expected version mismatch error")
	}
}

func TestPatchHistoryStoreErrorPaths(t *testing.T) {
	requireExePatchEnabled(t)

	t.Run("Load missing file returns default", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewPatchHistoryStore(filepath.Join(dir, "config.json"))
		if err != nil {
			t.Fatalf("NewPatchHistoryStore error: %v", err)
		}
		history, err := store.Load()
		if err != nil {
			t.Fatalf("Load error: %v", err)
		}
		if history.Version != PatchHistoryVersion || len(history.Entries) != 0 {
			t.Fatalf("unexpected history: %#v", history)
		}
	})

	t.Run("Load upgrades version zero", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "patch_history.json")
		if err := os.WriteFile(path, []byte(`{"version":0,"entries":[]}`), 0o600); err != nil {
			t.Fatalf("write patch history: %v", err)
		}
		store := &PatchHistoryStore{path: path}
		history, err := store.loadUnlocked()
		if err != nil {
			t.Fatalf("loadUnlocked error: %v", err)
		}
		if history.Version != PatchHistoryVersion {
			t.Fatalf("expected version upgrade, got %d", history.Version)
		}
	})

	t.Run("Update returns callback error", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewPatchHistoryStore(filepath.Join(dir, "config.json"))
		if err != nil {
			t.Fatalf("NewPatchHistoryStore error: %v", err)
		}
		if err := store.Update(func(h *PatchHistory) error {
			return fmt.Errorf("boom")
		}); err == nil {
			t.Fatalf("expected callback error")
		}
	})
}

func TestPatchHistoryPathDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path, err := PatchHistoryPath("")
	if err != nil {
		t.Fatalf("PatchHistoryPath error: %v", err)
	}
	want := filepath.Join(dir, "claude-proxy", "patch_history.json")
	if path != want {
		t.Fatalf("expected %q, got %q", want, path)
	}
}
