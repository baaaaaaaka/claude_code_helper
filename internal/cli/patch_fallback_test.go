package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

func writeBuiltInPatchedClaudeBinary(t *testing.T, path string) ([]byte, []byte) {
	t.Helper()
	original := []byte("function FI(H){if(H===\"policySettings\"){let L=sqA();if(L&&Object.keys(L).length>0)return L}let $=L4(H);if(!$)return null;let{settings:A}=DmA($);return A}")
	specs, err := policySettingsSpecs()
	if err != nil {
		t.Fatalf("policySettingsSpecs error: %v", err)
	}
	patched, _, err := applyExePatches(original, specs, io.Discard, false)
	if err != nil {
		t.Fatalf("applyExePatches error: %v", err)
	}
	if err := os.WriteFile(path, patched, 0o700); err != nil {
		t.Fatalf("write patched claude: %v", err)
	}
	if err := os.WriteFile(originalBackupPath(path), original, 0o700); err != nil {
		t.Fatalf("write backup: %v", err)
	}
	return original, patched
}

func TestBackupAndRestoreExecutable(t *testing.T) {
	requireExePatchEnabled(t)
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

	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("expected backup to remain: %v", err)
	}
}

func TestShouldRetryWindowsRestore(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	runtimeGOOS = "windows"
	shareErr := &os.PathError{Op: "open", Path: `C:\claude.exe`, Err: syscall.Errno(32)}
	if !shouldRetryWindowsRestore(shareErr) {
		t.Fatalf("expected sharing violation to retry")
	}

	otherErr := &os.PathError{Op: "open", Path: `C:\claude.exe`, Err: syscall.Errno(2)}
	if shouldRetryWindowsRestore(otherErr) {
		t.Fatalf("did not expect unrelated error to retry")
	}

	runtimeGOOS = "linux"
	if shouldRetryWindowsRestore(shareErr) {
		t.Fatalf("did not expect retry outside windows")
	}
}

func TestCleanupPatchHistory(t *testing.T) {
	requireExePatchEnabled(t)
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
	requireExePatchEnabled(t)
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

func TestDisableClaudeBytePatchSkipsWhenCurrentMatchesBackup(t *testing.T) {
	requireExePatchEnabled(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	backupPath := originalBackupPath(path)
	original := []byte("original-bytes")

	if err := os.WriteFile(path, original, 0o700); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.WriteFile(backupPath, original, 0o700); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	if err := disableClaudeBytePatch(path, filepath.Join(dir, "config.json"), io.Discard, false); err != nil {
		t.Fatalf("disableClaudeBytePatch error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(data) != string(original) {
		t.Fatalf("expected target to remain unchanged, got %q", string(data))
	}
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("expected backup to remain: %v", err)
	}
}

func TestDisableClaudeBytePatchSkipsWhenCurrentIsNotBuiltInPatch(t *testing.T) {
	requireExePatchEnabled(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	backupPath := originalBackupPath(path)
	current := []byte("custom-patched")
	backup := []byte("original-bytes")

	if err := os.WriteFile(path, current, 0o700); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.WriteFile(backupPath, backup, 0o700); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	if err := disableClaudeBytePatch(path, filepath.Join(dir, "config.json"), io.Discard, false); err != nil {
		t.Fatalf("disableClaudeBytePatch error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(data) != string(current) {
		t.Fatalf("expected target to remain unchanged, got %q", string(data))
	}
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("expected backup to remain: %v", err)
	}
}

func TestDisableClaudeBytePatchRestoresModifiedPatchedBackupWithoutHistory(t *testing.T) {
	requireExePatchEnabled(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	original, patched := writeBuiltInPatchedClaudeBinary(t, path)
	current := append(append([]byte{}, patched...), []byte("\ncustom-tail")...)

	if err := os.WriteFile(path, current, 0o700); err != nil {
		t.Fatalf("write target: %v", err)
	}

	if err := disableClaudeBytePatch(path, filepath.Join(dir, "config.json"), io.Discard, false); err != nil {
		t.Fatalf("disableClaudeBytePatch error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(data) != string(original) {
		t.Fatalf("expected target to be restored, got %q", string(data))
	}
	if _, err := os.Stat(originalBackupPath(path)); !os.IsNotExist(err) {
		t.Fatalf("expected backup to be removed after restore, got err=%v", err)
	}
}

func TestDisableClaudeBytePatchRejectsNonRegularBackup(t *testing.T) {
	requireExePatchEnabled(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	backupPath := originalBackupPath(path)

	if err := os.WriteFile(path, []byte("target"), 0o700); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Mkdir(backupPath, 0o700); err != nil {
		t.Fatalf("mkdir backup path: %v", err)
	}

	err := disableClaudeBytePatch(path, filepath.Join(dir, "config.json"), io.Discard, false)
	if err == nil {
		t.Fatalf("expected non-regular backup error")
	}
}

func TestDisableClaudeBytePatchReturnsStatBackupError(t *testing.T) {
	requireExePatchEnabled(t)
	if os.PathSeparator == '\\' {
		t.Skip("permission-denied stat behavior is platform-specific")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	backupPath := originalBackupPath(path)

	if err := os.WriteFile(path, []byte("target"), 0o700); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.WriteFile(backupPath, []byte("backup"), 0o700); err != nil {
		t.Fatalf("write backup: %v", err)
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	err := disableClaudeBytePatch(path, filepath.Join(dir, "config.json"), io.Discard, false)
	if err == nil || !strings.Contains(err.Error(), "stat backup file") {
		t.Fatalf("expected stat backup error, got %v", err)
	}
}

func TestCleanupPatchHistoryForPathRemovesMatchingEntries(t *testing.T) {
	requireExePatchEnabled(t)

	dir := t.TempDir()
	target := filepath.Join(dir, "claude")
	other := filepath.Join(dir, "other")
	store, err := config.NewPatchHistoryStore(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("new patch history store: %v", err)
	}
	if err := cleanupPatchHistoryForPath(nil, ""); err != nil {
		t.Fatalf("expected noop cleanup for nil store: %v", err)
	}
	if err := cleanupPatchHistoryForPath(store, ""); err != nil {
		t.Fatalf("expected noop cleanup for empty path: %v", err)
	}
	if err := store.Update(func(h *config.PatchHistory) error {
		h.Upsert(config.PatchHistoryEntry{Path: target, SpecsSHA256: "spec-a", PatchedSHA256: "hash-a"})
		h.Upsert(config.PatchHistoryEntry{Path: target, SpecsSHA256: "spec-b", PatchedSHA256: "hash-b"})
		h.Upsert(config.PatchHistoryEntry{Path: other, SpecsSHA256: "spec-c", PatchedSHA256: "hash-c"})
		return nil
	}); err != nil {
		t.Fatalf("seed history: %v", err)
	}

	if err := cleanupPatchHistoryForPath(store, target); err != nil {
		t.Fatalf("cleanupPatchHistoryForPath error: %v", err)
	}

	history, err := store.Load()
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(history.Entries) != 1 {
		t.Fatalf("expected 1 remaining entry, got %d", len(history.Entries))
	}
	if history.Entries[0].Path != other {
		t.Fatalf("expected remaining entry for %s, got %s", other, history.Entries[0].Path)
	}
}

func TestRemovePatchBackupHandlesNoopAndErrors(t *testing.T) {
	requireExePatchEnabled(t)

	if err := removePatchBackup(""); err != nil {
		t.Fatalf("expected noop remove for empty path: %v", err)
	}

	dir := t.TempDir()
	backupDir := filepath.Join(dir, "claude.claude-proxy.bak")
	if err := os.Mkdir(backupDir, 0o700); err != nil {
		t.Fatalf("mkdir backup dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "child"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write child: %v", err)
	}

	if err := removePatchBackup(backupDir); err == nil {
		t.Fatalf("expected remove error for non-empty directory")
	}
}

func TestDisableClaudeBytePatchHashErrors(t *testing.T) {
	requireExePatchEnabled(t)

	t.Run("target hash", func(t *testing.T) {
		withExePatchTestHooks(t)

		dir := t.TempDir()
		path := filepath.Join(dir, "claude")
		backupPath := originalBackupPath(path)
		if err := os.WriteFile(path, []byte("target"), 0o700); err != nil {
			t.Fatalf("write target: %v", err)
		}
		if err := os.WriteFile(backupPath, []byte("backup"), 0o700); err != nil {
			t.Fatalf("write backup: %v", err)
		}

		hashFileSHA256Fn = func(gotPath string) (string, error) {
			if gotPath == path {
				return "", errors.New("target hash boom")
			}
			return "ok", nil
		}

		err := disableClaudeBytePatch(path, filepath.Join(dir, "config.json"), io.Discard, false)
		if err == nil || !strings.Contains(err.Error(), "hash target executable") {
			t.Fatalf("expected target hash error, got %v", err)
		}
	})

	t.Run("backup hash", func(t *testing.T) {
		withExePatchTestHooks(t)

		dir := t.TempDir()
		path := filepath.Join(dir, "claude")
		backupPath := originalBackupPath(path)
		if err := os.WriteFile(path, []byte("target"), 0o700); err != nil {
			t.Fatalf("write target: %v", err)
		}
		if err := os.WriteFile(backupPath, []byte("backup"), 0o700); err != nil {
			t.Fatalf("write backup: %v", err)
		}

		hashFileSHA256Fn = func(gotPath string) (string, error) {
			if gotPath == backupPath {
				return "", errors.New("backup hash boom")
			}
			return "ok", nil
		}

		err := disableClaudeBytePatch(path, filepath.Join(dir, "config.json"), io.Discard, false)
		if err == nil || !strings.Contains(err.Error(), "hash backup executable") {
			t.Fatalf("expected backup hash error, got %v", err)
		}
	})
}

func TestDisableClaudeBytePatchLogsStoreInitAndRemoveBackupFailure(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	_, _ = writeBuiltInPatchedClaudeBinary(t, path)

	newPatchHistoryStoreFn = func(configPath string) (*config.PatchHistoryStore, error) {
		return nil, errors.New("store init boom")
	}
	restoreExecutableFromBackupFn = func(outcome *patchOutcome) error {
		if err := restoreExecutableFromBackup(outcome); err != nil {
			return err
		}
		if err := os.Remove(outcome.BackupPath); err != nil {
			return err
		}
		if err := os.Mkdir(outcome.BackupPath, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(outcome.BackupPath, "child"), []byte("x"), 0o600); err != nil {
			return err
		}
		return nil
	}

	var log bytes.Buffer
	if err := disableClaudeBytePatch(path, filepath.Join(dir, "config.json"), &log, false); err != nil {
		t.Fatalf("disableClaudeBytePatch error: %v", err)
	}
	if !strings.Contains(log.String(), "failed to init patch history") {
		t.Fatalf("expected history init failure log, got %q", log.String())
	}
	if !strings.Contains(log.String(), "failed to remove backup") {
		t.Fatalf("expected backup removal failure log, got %q", log.String())
	}
}

func TestDisableClaudeBytePatchRestoreError(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	_, _ = writeBuiltInPatchedClaudeBinary(t, path)

	restoreExecutableFromBackupFn = func(outcome *patchOutcome) error {
		return errors.New("restore boom")
	}

	err := disableClaudeBytePatch(path, filepath.Join(dir, "config.json"), io.Discard, false)
	if err == nil || !strings.Contains(err.Error(), "restore patched executable") {
		t.Fatalf("expected restore error, got %v", err)
	}
}

func TestDisableClaudeBytePatchLogsCleanupHistoryFailure(t *testing.T) {
	requireExePatchEnabled(t)
	if os.PathSeparator == '\\' {
		t.Skip("cleanup history permission failure is platform-specific")
	}

	targetDir := t.TempDir()
	path := filepath.Join(targetDir, "claude")
	_, _ = writeBuiltInPatchedClaudeBinary(t, path)

	configDir := t.TempDir()
	if err := os.Chmod(configDir, 0o500); err != nil {
		t.Fatalf("chmod config dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(configDir, 0o700) })

	var log bytes.Buffer
	if err := disableClaudeBytePatch(path, filepath.Join(configDir, "config.json"), &log, false); err != nil {
		t.Fatalf("disableClaudeBytePatch error: %v", err)
	}
	if !strings.Contains(log.String(), "failed to cleanup patch history") {
		t.Fatalf("expected cleanup history failure log, got %q", log.String())
	}
}

func TestLooksLikeClaudeBuiltInBytePatchReadError(t *testing.T) {
	requireExePatchEnabled(t)

	_, err := looksLikeClaudeBuiltInBytePatch(filepath.Join(t.TempDir(), "missing"))
	if err == nil || !strings.Contains(err.Error(), "read target executable") {
		t.Fatalf("expected read error, got %v", err)
	}
}

func TestDisableClaudeBytePatchPropagatesLooksLikeError(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	backupPath := originalBackupPath(path)
	_, patched := writeBuiltInPatchedClaudeBinary(t, path)
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove target: %v", err)
	}

	specs, err := policySettingsSpecs()
	if err != nil {
		t.Fatalf("policySettingsSpecs error: %v", err)
	}
	store, err := config.NewPatchHistoryStore(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("new patch history store: %v", err)
	}
	if err := store.Update(func(h *config.PatchHistory) error {
		h.Upsert(config.PatchHistoryEntry{
			Path:          path,
			SpecsSHA256:   patchSpecsHash(specs),
			PatchedSHA256: hashBytes(patched),
		})
		return nil
	}); err != nil {
		t.Fatalf("seed history: %v", err)
	}

	hashFileSHA256Fn = func(gotPath string) (string, error) {
		if gotPath == path {
			return "modified-patched-hash", nil
		}
		if gotPath == backupPath {
			return hashBytes([]byte("unused-backup-hash-source")), nil
		}
		return "", errors.New("unexpected hash path")
	}

	err = disableClaudeBytePatch(path, filepath.Join(dir, "config.json"), io.Discard, false)
	if err == nil || !strings.Contains(err.Error(), "read target executable") {
		t.Fatalf("expected looksLike read error, got %v", err)
	}
}
