package cli

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

func TestRunTargetWithFallbackRestoresAndReruns(t *testing.T) {
	requireExePatchEnabled(t)
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target.bin")
	backup := filepath.Join(dir, "target.bin.bak")

	if err := os.WriteFile(target, []byte("patched"), 0o700); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.WriteFile(backup, []byte("original"), 0o700); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	script := filepath.Join(dir, "run.sh")
	scriptBody := []byte("#!/bin/sh\n" +
		"if [ \"$(cat \"" + target + "\")\" = \"original\" ]; then exit 0; fi\n" +
		"echo \"error: Module not found '/ @bun @bytecode @b'\" 1>&2\n" +
		"exit 1\n")
	if err := os.WriteFile(script, scriptBody, 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}

	store, err := config.NewPatchHistoryStore(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("NewPatchHistoryStore error: %v", err)
	}
	if err := store.Update(func(h *config.PatchHistory) error {
		h.Upsert(config.PatchHistoryEntry{
			Path:          target,
			SpecsSHA256:   "spec-hash",
			PatchedSHA256: "patched-hash",
		})
		return nil
	}); err != nil {
		t.Fatalf("seed history error: %v", err)
	}

	outcome := &patchOutcome{
		Applied:      true,
		TargetPath:   target,
		BackupPath:   backup,
		SpecsHash:    "spec-hash",
		HistoryStore: store,
	}

	if err := runTargetWithFallback(context.Background(), []string{script}, "", nil, outcome, nil); err != nil {
		t.Fatalf("runTargetWithFallback error: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read restored target: %v", err)
	}
	if string(data) != "original" {
		t.Fatalf("expected target to be restored, got %q", string(data))
	}

	history, err := store.Load()
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(history.Entries) != 0 {
		t.Fatalf("expected history to be cleared, got %d entries", len(history.Entries))
	}

	if _, err := os.Stat(backup); err == nil {
		t.Fatalf("expected backup to be removed")
	}
}
