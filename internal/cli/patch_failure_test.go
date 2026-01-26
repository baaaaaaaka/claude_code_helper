package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/config"
)

func TestSupportsYoloFlag(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	dir := t.TempDir()

	pathWithFlag := writeProbeScript(t, dir, "has-flag", "#!/bin/sh\necho \"--permission-mode\"; exit 0\n")
	if !supportsYoloFlag(pathWithFlag) {
		t.Fatalf("expected support when flag appears in help output")
	}

	pathWithUsage := writeProbeScript(t, dir, "usage", "#!/bin/sh\necho \"usage: claude\"; exit 0\n")
	if supportsYoloFlag(pathWithUsage) {
		t.Fatalf("expected no support for plain usage output")
	}

	pathWithError := writeProbeScript(t, dir, "error", "#!/bin/sh\nexit 1\n")
	if !supportsYoloFlag(pathWithError) {
		t.Fatalf("expected support when help command fails")
	}
}

func TestRecordPatchFailureAndSkip(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	origVersion := version
	version = "v9.9.9"
	t.Cleanup(func() { version = origVersion })

	outcome := &patchOutcome{
		IsClaude:      true,
		TargetVersion: "2.1.19",
		TargetSHA256:  "abc123",
		TargetPath:    "/tmp/claude",
	}
	if err := recordPatchFailure(configPath, outcome, "boom"); err != nil {
		t.Fatalf("recordPatchFailure error: %v", err)
	}
	store, err := config.NewStore(configPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.PatchFailures) != 1 {
		t.Fatalf("expected 1 patch failure, got %d", len(cfg.PatchFailures))
	}
	entry := cfg.PatchFailures[0]
	if entry.ProxyVersion != "v9.9.9" || entry.ClaudeVersion != "2.1.19" || entry.ClaudeSHA256 != "abc123" {
		t.Fatalf("unexpected patch failure entry: %#v", entry)
	}
	if entry.Reason == "" {
		t.Fatalf("expected failure reason to be recorded")
	}

	if skip, err := shouldSkipPatchFailure(configPath, "v9.9.9", "2.1.19", ""); err != nil || !skip {
		t.Fatalf("expected skip by version, got skip=%v err=%v", skip, err)
	}
	if skip, err := shouldSkipPatchFailure(configPath, "v9.9.9", "", "abc123"); err != nil || !skip {
		t.Fatalf("expected skip by sha, got skip=%v err=%v", skip, err)
	}
	if skip, err := shouldSkipPatchFailure(configPath, "v9.9.8", "2.1.19", ""); err != nil || skip {
		t.Fatalf("expected no skip for different proxy version, got skip=%v err=%v", skip, err)
	}
}

func TestIsYoloFailure(t *testing.T) {
	if !isYoloFailure(os.ErrInvalid, "unknown flag: --permission-mode") {
		t.Fatalf("expected yolo failure on unknown flag")
	}
	if isYoloFailure(os.ErrInvalid, "unrelated error") {
		t.Fatalf("unexpected yolo failure for unrelated error")
	}
}

func TestStripYoloArgs(t *testing.T) {
	in := []string{"claude", "--permission-mode", "bypassPermissions", "--resume", "abc"}
	out := stripYoloArgs(in)
	want := []string{"claude", "--resume", "abc"}
	if len(out) != len(want) {
		t.Fatalf("expected %v, got %v", want, out)
	}
	for i := range want {
		if out[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, out)
		}
	}
}

func writeProbeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}
