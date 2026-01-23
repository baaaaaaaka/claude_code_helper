package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/config"
)

func TestBackupAndRestoreExecutable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	original := []byte("original-bytes")

	if err := os.WriteFile(path, original, 0o700); err != nil {
		t.Fatalf("write original: %v", err)
	}

	backupPath, err := backupExecutable(path, 0o700)
	if err != nil {
		t.Fatalf("backupExecutable error: %v", err)
	}

	if err := os.WriteFile(path, []byte("patched-bytes"), 0o700); err != nil {
		t.Fatalf("write patched: %v", err)
	}

	outcome := &patchOutcome{
		TargetPath: path,
		BackupPath: backupPath,
	}
	if err := restoreExecutableFromBackup(outcome); err != nil {
		t.Fatalf("restoreExecutableFromBackup error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read restored: %v", err)
	}
	if string(data) != string(original) {
		t.Fatalf("expected restored data %q, got %q", original, data)
	}

	if _, err := os.Stat(backupPath); err == nil {
		t.Fatalf("expected backup to be removed")
	}
}

func TestCleanupPatchHistory(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	store, err := config.NewPatchHistoryStore(configPath)
	if err != nil {
		t.Fatalf("NewPatchHistoryStore error: %v", err)
	}

	outcome := &patchOutcome{
		TargetPath:   filepath.Join(dir, "bin"),
		SpecsHash:    "spec-hash",
		HistoryStore: store,
	}
	if err := store.Update(func(h *config.PatchHistory) error {
		h.Upsert(config.PatchHistoryEntry{
			Path:          outcome.TargetPath,
			SpecsSHA256:   outcome.SpecsHash,
			PatchedSHA256: "patched-hash",
		})
		return nil
	}); err != nil {
		t.Fatalf("update history error: %v", err)
	}

	if err := cleanupPatchHistory(outcome); err != nil {
		t.Fatalf("cleanupPatchHistory error: %v", err)
	}

	history, err := store.Load()
	if err != nil {
		t.Fatalf("load history error: %v", err)
	}
	if len(history.Entries) != 0 {
		t.Fatalf("expected history to be empty, got %d entries", len(history.Entries))
	}
}

func TestIsPatchedBinaryFailure(t *testing.T) {
	err := errors.New("exit status 1")
	if !isPatchedBinaryFailure(err, "error: Module not found '/ @bun @bytecode @b'\nBun v1.3.6") {
		t.Fatalf("expected bun module error to be detected")
	}
	if isPatchedBinaryFailure(nil, "Bun v1.3.6") {
		t.Fatalf("expected nil error to return false")
	}
	if isPatchedBinaryFailure(err, "some other error") {
		t.Fatalf("expected unrelated error to return false")
	}
}
