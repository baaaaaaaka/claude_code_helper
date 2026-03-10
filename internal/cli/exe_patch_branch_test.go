package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

func withExePatchTestHooks(t *testing.T) {
	t.Helper()
	prevLookPath := execLookPathFn
	prevResolvePath := resolveExecutablePathFn
	prevProxyVersion := currentProxyVersionFn
	prevResolveClaudeVersion := resolveClaudeVersionFn
	prevHash := hashFileSHA256Fn
	prevShouldSkip := shouldSkipPatchFailureFn
	prevHistoryStore := newPatchHistoryStoreFn
	prevPatch := patchExecutableFn
	prevCodesign := adhocCodesignFn
	prevProbe := runClaudeProbeFn
	prevGlibcPatch := applyClaudeGlibcCompatPatchFn
	prevRestore := restoreExecutableFromBackupFn
	prevCleanup := cleanupPatchHistoryFn
	prevRecordFailure := recordPatchFailureFn
	t.Cleanup(func() {
		execLookPathFn = prevLookPath
		resolveExecutablePathFn = prevResolvePath
		currentProxyVersionFn = prevProxyVersion
		resolveClaudeVersionFn = prevResolveClaudeVersion
		hashFileSHA256Fn = prevHash
		shouldSkipPatchFailureFn = prevShouldSkip
		newPatchHistoryStoreFn = prevHistoryStore
		patchExecutableFn = prevPatch
		adhocCodesignFn = prevCodesign
		runClaudeProbeFn = prevProbe
		applyClaudeGlibcCompatPatchFn = prevGlibcPatch
		restoreExecutableFromBackupFn = prevRestore
		cleanupPatchHistoryFn = prevCleanup
		recordPatchFailureFn = prevRecordFailure
	})
}

func writeClaudeVersionStub(t *testing.T, dir, versionText string) string {
	t.Helper()
	var path string
	var body string
	if runtime.GOOS == "windows" {
		path = filepath.Join(dir, "claude.cmd")
		body = "@echo off\r\nif \"%~1\"==\"--version\" (\r\n  echo " + versionText + "\r\n  exit /b 0\r\n)\r\nexit /b 0\r\n"
	} else {
		path = filepath.Join(dir, "claude")
		body = "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo \"" + versionText + "\"\n  exit 0\nfi\nexit 0\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write claude stub: %v", err)
	}
	return path
}

func patchOptionsForVersionReplacement(replace string) exePatchOptions {
	return exePatchOptions{
		enabledFlag: true,
		regex1:      `echo "?Claude Code 1\.2\.3"?`,
		regex2:      []string{`Claude Code 1\.2\.3`},
		regex3:      []string{`echo "?Claude Code 1\.2\.3"?`},
		replace:     []string{replace},
	}
}

func failingVersionReplacement() string {
	if runtime.GOOS == "windows" {
		return "exit /b 42"
	}
	return "exit 42"
}

func TestMaybePatchExecutableSkipsKnownFailure(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	dir := t.TempDir()
	path := writeClaudeVersionStub(t, dir, "Claude Code 1.2.3")
	setStubPath(t, dir)

	configPath := filepath.Join(dir, "config.json")
	prevVersion := version
	version = "v1.2.3"
	t.Cleanup(func() { version = prevVersion })

	if err := recordPatchFailure(configPath, &patchOutcome{
		IsClaude:      true,
		TargetPath:    path,
		TargetVersion: "1.2.3",
	}, "previous boom"); err != nil {
		t.Fatalf("recordPatchFailure error: %v", err)
	}

	var log bytes.Buffer
	outcome, err := maybePatchExecutable([]string{"claude"}, patchOptionsForVersionReplacement(`echo "Claude Code 9.9.9"`), configPath, &log)
	if err != nil {
		t.Fatalf("maybePatchExecutable error: %v", err)
	}
	if outcome == nil || outcome.Applied {
		t.Fatalf("expected skip outcome without applying patch, got %#v", outcome)
	}
	if !strings.Contains(log.String(), "skip (previous failure)") {
		t.Fatalf("expected skip log, got %q", log.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read stub: %v", err)
	}
	if !strings.Contains(string(got), "Claude Code 1.2.3") {
		t.Fatalf("expected executable to remain unchanged")
	}
}

func TestMaybePatchExecutableRestoresAfterProbeFailure(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	dir := t.TempDir()
	path := writeClaudeVersionStub(t, dir, "Claude Code 1.2.3")
	setStubPath(t, dir)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read original stub: %v", err)
	}

	configPath := filepath.Join(dir, "config.json")
	var log bytes.Buffer
	outcome, err := maybePatchExecutable([]string{"claude"}, patchOptionsForVersionReplacement(failingVersionReplacement()), configPath, &log)
	if err != nil {
		t.Fatalf("maybePatchExecutable error: %v", err)
	}
	if outcome != nil {
		t.Fatalf("expected probe failure path to return nil outcome, got %#v", outcome)
	}
	if !strings.Contains(log.String(), "detected startup failure; restoring backup") {
		t.Fatalf("expected restore log, got %q", log.String())
	}

	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read restored stub: %v", err)
	}
	if string(restored) != string(original) {
		t.Fatalf("expected executable to be restored")
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
		t.Fatalf("expected one recorded patch failure, got %d", len(cfg.PatchFailures))
	}
}

func TestMaybePatchExecutableRestoresAfterCodesignFailure(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	dir := t.TempDir()
	path := writeClaudeVersionStub(t, dir, "Claude Code 1.2.3")
	setStubPath(t, dir)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read original stub: %v", err)
	}

	adhocCodesignFn = func(string, io.Writer) error {
		return os.ErrPermission
	}

	configPath := filepath.Join(dir, "config.json")
	var log bytes.Buffer
	outcome, err := maybePatchExecutable([]string{"claude"}, patchOptionsForVersionReplacement(`echo "Claude Code 1.2.4"`), configPath, &log)
	if err != nil {
		t.Fatalf("maybePatchExecutable error: %v", err)
	}
	if outcome != nil {
		t.Fatalf("expected codesign failure path to return nil outcome, got %#v", outcome)
	}
	if !strings.Contains(log.String(), "codesign failed; restoring backup") {
		t.Fatalf("expected codesign restore log, got %q", log.String())
	}

	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read restored stub: %v", err)
	}
	if string(restored) != string(original) {
		t.Fatalf("expected executable to be restored")
	}
}

func TestMaybePatchExecutableAppliesGlibcCompatAfterProbeFailure(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	dir := t.TempDir()
	path := writeClaudeVersionStub(t, dir, "Claude Code 1.2.3")
	setStubPath(t, dir)

	probeCalls := 0
	runClaudeProbeFn = func(path string, arg string) (string, error) {
		probeCalls++
		if probeCalls == 1 {
			return path + ": /lib64/libc.so.6: version `GLIBC_2.25' not found", os.ErrInvalid
		}
		return "Claude Code 1.2.3", nil
	}

	applied := false
	applyClaudeGlibcCompatPatchFn = func(path string, opts exePatchOptions, log io.Writer, dryRun bool, outcome *patchOutcome) (*patchOutcome, bool, error) {
		applied = true
		if path == "" || !opts.glibcCompat {
			t.Fatalf("unexpected glibc patch input: path=%q opts=%+v", path, opts)
		}
		if outcome == nil {
			outcome = &patchOutcome{TargetPath: path}
		}
		outcome.Applied = true
		return outcome, true, nil
	}

	outcome, err := maybePatchExecutable([]string{"claude"}, exePatchOptions{
		enabledFlag: true,
		glibcCompat: true,
	}, filepath.Join(dir, "config.json"), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("maybePatchExecutable error: %v", err)
	}
	resolvedPath, err := resolveExecutablePath(path)
	if err != nil {
		t.Fatalf("resolveExecutablePath error: %v", err)
	}
	if !applied {
		t.Fatalf("expected glibc compat patch to be attempted")
	}
	if outcome == nil || !outcome.Applied || outcome.TargetPath != resolvedPath {
		t.Fatalf("unexpected outcome: %#v", outcome)
	}
	if probeCalls != 2 {
		t.Fatalf("expected two probe calls, got %d", probeCalls)
	}
}
