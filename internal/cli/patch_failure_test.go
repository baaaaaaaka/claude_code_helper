package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
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
	requireExePatchEnabled(t)
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

func TestIsClaudeExecutableUsesCmdArgWhenResolvedDiffers(t *testing.T) {
	if !isClaudeExecutable("claude", "/Users/mocha/.local/share/claude/versions/2.1.22") {
		t.Fatalf("expected cmd arg to identify claude binary")
	}
	if !isClaudeExecutable("claude.exe", "C:\\Users\\mocha\\AppData\\Local\\claude\\2.1.22") {
		t.Fatalf("expected cmd arg to identify claude.exe binary")
	}
	if isClaudeExecutable("not-claude", "/tmp/2.1.22") {
		t.Fatalf("expected non-claude to be rejected")
	}
}

func TestIsYoloFailure(t *testing.T) {
	cases := []struct {
		output string
		want   bool
	}{
		{"", false},
		{"unknown flag: --permission-mode", true},
		{"permission-mode unknown", true},
		{"permission-mode not supported", true},
		{"permission-mode invalid", true},
		{"permission-mode flag provided but not defined", true},
		{"unrelated error", false},
	}
	for _, tc := range cases {
		got := isYoloFailure(os.ErrInvalid, tc.output)
		if got != tc.want {
			t.Fatalf("output %q: expected %v, got %v", tc.output, tc.want, got)
		}
	}
	if isYoloFailure(nil, "permission-mode unknown") {
		t.Fatalf("expected nil error to return false")
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

func TestExtractVersion(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"Claude Code 1.2.3", "1.2.3"},
		{"v2.0", "2.0"},
		{"version", "version"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := extractVersion(tc.input); got != tc.want {
			t.Fatalf("extractVersion(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestResolveClaudeVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	if runtime.GOOS == "windows" {
		path += ".cmd"
	}
	writeStub(t, dir, "claude", "#!/bin/sh\necho \"Claude Code 1.2.3\"\n", "@echo off\r\necho Claude Code 1.2.3\r\n")
	if got := resolveClaudeVersion(path); got != "1.2.3" {
		t.Fatalf("expected version 1.2.3, got %q", got)
	}
}

func TestHashFileSHA256(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	got, err := hashFileSHA256(path)
	if err != nil {
		t.Fatalf("hashFileSHA256 error: %v", err)
	}
	want := sha256.Sum256([]byte("hello"))
	if got != hex.EncodeToString(want[:]) {
		t.Fatalf("unexpected hash %q", got)
	}
}

func TestIsPatchedBinaryStartupFailure(t *testing.T) {
	if isPatchedBinaryStartupFailure(nil, "") {
		t.Fatalf("expected nil error to be false")
	}
	if !isPatchedBinaryStartupFailure(os.ErrInvalid, "@bun @bytecode") {
		t.Fatalf("expected bun bytecode output to be treated as failure")
	}
	if !isPatchedBinaryStartupFailure(&os.PathError{Op: "open", Path: "/nope", Err: os.ErrNotExist}, "") {
		t.Fatalf("expected path error to be treated as failure")
	}
	if !isPatchedBinaryStartupFailure(&exec.Error{Name: "missing", Err: exec.ErrNotFound}, "") {
		t.Fatalf("expected exec error to be treated as failure")
	}
	cmdName := "sh"
	cmdArgs := []string{"-c", "exit 1"}
	if runtime.GOOS == "windows" {
		cmdName = "cmd"
		cmdArgs = []string{"/C", "exit 1"}
	}
	cmd := exec.Command(cmdName, cmdArgs...)
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected exit error")
	}
	if isPatchedBinaryStartupFailure(err, "") {
		t.Fatalf("expected exit error to be treated as non-startup failure")
	}
}

func TestPatchFailureHelpers(t *testing.T) {
	t.Run("formatFailureReason truncates", func(t *testing.T) {
		long := strings.Repeat("x", 300)
		got := formatFailureReason(nil, long)
		if len(got) != 243 {
			t.Fatalf("expected truncated length 243, got %d", len(got))
		}
		if !strings.HasSuffix(got, "...") {
			t.Fatalf("expected truncation suffix, got %q", got)
		}
	})

	t.Run("recordPatchFailure merges concurrent entries", func(t *testing.T) {
		requireExePatchEnabled(t)
		dir := t.TempDir()
		configPath := filepath.Join(dir, "config.json")
		outcome := &patchOutcome{
			IsClaude:      true,
			TargetVersion: "2.2.0",
			TargetSHA256:  "sha-1",
			TargetPath:    "/tmp/claude",
		}

		const workers = 5
		errCh := make(chan error, workers)
		for i := 0; i < workers; i++ {
			go func() {
				errCh <- recordPatchFailure(configPath, outcome, "boom")
			}()
		}
		for i := 0; i < workers; i++ {
			if err := <-errCh; err != nil {
				t.Fatalf("recordPatchFailure error: %v", err)
			}
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
			t.Fatalf("expected 1 patch failure entry, got %d", len(cfg.PatchFailures))
		}
	})
}

func writeProbeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}
