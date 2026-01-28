package config

import (
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
	}
	if err := store.Update(func(h *PatchHistory) error {
		h.Upsert(entry)
		h.Upsert(PatchHistoryEntry{
			Path:          entry.Path,
			SpecsSHA256:   entry.SpecsSHA256,
			PatchedSHA256: "patched-2",
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
	if history.IsPatched(entry.Path, entry.SpecsSHA256, entry.PatchedSHA256) {
		t.Fatalf("expected IsPatched to be false after overwrite")
	}
	if !history.IsPatched(entry.Path, entry.SpecsSHA256, "patched-2") {
		t.Fatalf("expected IsPatched to be true after overwrite")
	}

	if removed := history.Remove(entry.Path, entry.SpecsSHA256); !removed {
		t.Fatalf("expected Remove to succeed")
	}
	if history.IsPatched(entry.Path, entry.SpecsSHA256, entry.PatchedSHA256) {
		t.Fatalf("expected IsPatched to be false after Remove")
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
