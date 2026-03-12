package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
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
		VerifiedAt:    time.Unix(1700000000, 0).UTC(),
	}
	if err := store.Update(func(h *PatchHistory) error {
		h.Upsert(entry)
		h.Upsert(PatchHistoryEntry{
			Path:          entry.Path,
			SpecsSHA256:   entry.SpecsSHA256,
			PatchedSHA256: "patched-2",
			ProxyVersion:  "v2",
			VerifiedAt:    time.Time{},
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
	if history.IsVerified(entry.Path, entry.SpecsSHA256, "patched-2", "v2") {
		t.Fatalf("expected IsVerified to be false for unverified entry")
	}
	if found, ok := history.Find(entry.Path, entry.SpecsSHA256); !ok {
		t.Fatalf("expected Find to return entry")
	} else if found.ProxyVersion != "v2" {
		t.Fatalf("expected Find to return latest proxy version, got %q", found.ProxyVersion)
	}
	if !history.MarkVerified(entry.Path, entry.SpecsSHA256, time.Unix(1700000010, 0).UTC()) {
		t.Fatalf("expected MarkVerified to succeed")
	}
	if !history.IsVerified(entry.Path, entry.SpecsSHA256, "patched-2", "v2") {
		t.Fatalf("expected IsVerified to be true after MarkVerified")
	}
	if history.MarkVerified("missing", "missing", time.Now()) {
		t.Fatalf("expected MarkVerified to be false for missing entry")
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

	t.Run("Load migrates version one entries to verified", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "patch_history.json")
		payload := `{"version":1,"entries":[{"path":"/tmp/claude","specsSha256":"spec","patchedSha256":"hash","proxyVersion":"v1","patchedAt":"2024-01-02T03:04:05Z"}]}`
		if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
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
		if len(history.Entries) != 1 {
			t.Fatalf("expected 1 history entry, got %d", len(history.Entries))
		}
		if history.Entries[0].VerifiedAt.IsZero() {
			t.Fatalf("expected version one entry to be migrated as verified")
		}
		if !history.IsVerified("/tmp/claude", "spec", "hash", "v1") {
			t.Fatalf("expected migrated entry to be verified")
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

func TestPathsEqual(t *testing.T) {
	// Same path should always match.
	if !PathsEqual("/usr/bin/claude", "/usr/bin/claude") {
		t.Fatal("expected identical paths to be equal")
	}

	if runtime.GOOS == "windows" {
		// Case-insensitive on Windows.
		if !PathsEqual(`C:\Users\FOO\bin\claude.exe`, `c:\users\foo\bin\claude.exe`) {
			t.Fatal("expected case-insensitive match on Windows")
		}
	} else {
		// Case-sensitive on other platforms.
		if PathsEqual("/usr/bin/Claude", "/usr/bin/claude") {
			t.Fatal("expected case-sensitive mismatch on non-Windows")
		}
	}

	// Different paths should never match.
	if PathsEqual("/usr/bin/claude", "/usr/local/bin/claude") {
		t.Fatal("expected different paths to not be equal")
	}
}

func TestPatchHistoryPathsEqualIntegration(t *testing.T) {
	// Verify that IsPatched, Find, Remove, and Upsert all use PathsEqual
	// by confirming behavior with mixed-case paths on non-Windows.
	if runtime.GOOS == "windows" {
		t.Skip("case-sensitivity test only meaningful on non-Windows")
	}

	h := PatchHistory{Version: 1}
	h.Upsert(PatchHistoryEntry{
		Path:          "/usr/bin/Claude",
		SpecsSHA256:   "spec1",
		PatchedSHA256: "hash1",
		ProxyVersion:  "v1",
	})

	// Different case should NOT match on non-Windows.
	if h.IsPatched("/usr/bin/claude", "spec1", "hash1", "v1") {
		t.Fatal("should not match different case on non-Windows")
	}
	if _, ok := h.Find("/usr/bin/claude", "spec1"); ok {
		t.Fatal("Find should not match different case on non-Windows")
	}
	if h.Remove("/usr/bin/claude", "spec1") {
		t.Fatal("Remove should not match different case on non-Windows")
	}

	// Exact case should match.
	if !h.IsPatched("/usr/bin/Claude", "spec1", "hash1", "v1") {
		t.Fatal("should match exact case")
	}
}

func TestPatchHistoryPathDefault(t *testing.T) {
	dir := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", dir)
	case "darwin":
		t.Setenv("HOME", dir)
	default:
		t.Setenv("XDG_CONFIG_HOME", dir)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir error: %v", err)
	}
	path, err := PatchHistoryPath("")
	if err != nil {
		t.Fatalf("PatchHistoryPath error: %v", err)
	}
	want := filepath.Join(base, "claude-proxy", "patch_history.json")
	if path != want {
		t.Fatalf("expected %q, got %q", want, path)
	}
}
