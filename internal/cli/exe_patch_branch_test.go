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
	prevTimedProbe := runClaudeTimedProbeFn
	prevGlibcPatch := applyClaudeGlibcCompatPatchFn
	prevRestore := restoreExecutableFromBackupFn
	prevCleanup := cleanupPatchHistoryFn
	prevRecordFailure := recordPatchFailureFn
	prevGOOS := runtimeGOOS
	prevReadinessPolicy := patchReadinessPolicyFn
	prevMaybePatchCtx := maybePatchExecutableCtxFn
	prevWaitReady := waitPatchedExecutableReadyFn
	prevRunWithProfileOptions := runWithProfileOptionsFn
	prevRunTargetWithFallback := runTargetWithFallbackWithOptionsFn
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
		runClaudeTimedProbeFn = prevTimedProbe
		applyClaudeGlibcCompatPatchFn = prevGlibcPatch
		restoreExecutableFromBackupFn = prevRestore
		cleanupPatchHistoryFn = prevCleanup
		recordPatchFailureFn = prevRecordFailure
		runtimeGOOS = prevGOOS
		patchReadinessPolicyFn = prevReadinessPolicy
		maybePatchExecutableCtxFn = prevMaybePatchCtx
		waitPatchedExecutableReadyFn = prevWaitReady
		runWithProfileOptionsFn = prevRunWithProfileOptions
		runTargetWithFallbackWithOptionsFn = prevRunTargetWithFallback
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

func yoloClaudeArgs(cmd string) []string {
	return []string{cmd, "--permission-mode", "bypassPermissions"}
}

func TestExePatchOptionsCompileSuccess(t *testing.T) {
	specs, err := (exePatchOptions{
		enabledFlag:    true,
		policySettings: true,
		regex1:         "outer",
		regex2:         []string{"guard"},
		regex3:         []string{"patch"},
		replace:        []string{"replace"},
	}).compile()
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	if len(specs) != 5 {
		t.Fatalf("expected 5 specs, got %d", len(specs))
	}
}

func TestExePatchOptionsCompileValidatesCustomRules(t *testing.T) {
	_, err := (exePatchOptions{
		enabledFlag:    true,
		policySettings: true,
		regex2:         []string{"guard"},
	}).compile()
	if err == nil {
		t.Fatalf("expected compile validation error")
	}
}

func TestExePatchOptionsCompileCustomSpecsValidatesCustomRules(t *testing.T) {
	_, err := (exePatchOptions{
		enabledFlag: true,
		regex2:      []string{"guard"},
	}).compileCustomSpecs()
	if err == nil {
		t.Fatalf("expected custom spec validation error")
	}
}

func TestMaybePatchExecutablePropagatesCompileBuiltinError(t *testing.T) {
	requireExePatchEnabled(t)

	_, err := maybePatchExecutable([]string{"claude"}, exePatchOptions{
		enabledFlag:    true,
		policySettings: true,
		regex2:         []string{"guard"},
	}, "", io.Discard)
	if err == nil {
		t.Fatalf("expected compileBuiltinSpecs error")
	}
}

func TestMaybePatchExecutablePropagatesDisableClaudeBytePatchError(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	dir := t.TempDir()
	setStubPath(t, dir)

	path := filepath.Join(dir, "claude")
	if runtime.GOOS == "windows" {
		path += ".cmd"
	}
	if err := os.WriteFile(path, []byte("target"), 0o700); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Mkdir(originalBackupPath(path), 0o700); err != nil {
		t.Fatalf("mkdir backup path: %v", err)
	}

	_, err := maybePatchExecutable([]string{"claude"}, exePatchOptions{
		enabledFlag:    true,
		policySettings: true,
	}, filepath.Join(dir, "config.json"), io.Discard)
	if err == nil {
		t.Fatalf("expected disableClaudeBytePatch error")
	}
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
	outcome, err := maybePatchExecutable(yoloClaudeArgs("claude"), patchOptionsForVersionReplacement(`echo "Claude Code 9.9.9"`), configPath, &log)
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

func TestMaybePatchExecutableSkipsBuiltInClaudePatchWhenPermissionModeDisabled(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	dir := t.TempDir()
	path := writeClaudeVersionStub(t, dir, "Claude Code 1.2.3")
	setStubPath(t, dir)

	patchCalled := false
	patchExecutableFn = func(path string, specs []exePatchSpec, log io.Writer, preview bool, dryRun bool, historyStore *config.PatchHistoryStore, proxyVersion string) (*patchOutcome, error) {
		patchCalled = true
		return nil, nil
	}

	outcome, err := maybePatchExecutable([]string{"claude"}, exePatchOptions{
		enabledFlag:    true,
		policySettings: true,
	}, filepath.Join(dir, "config.json"), io.Discard)
	if err != nil {
		t.Fatalf("maybePatchExecutable error: %v", err)
	}
	if outcome != nil {
		t.Fatalf("expected nil outcome when built-in Claude patch is disabled, got %#v", outcome)
	}
	if patchCalled {
		t.Fatalf("expected built-in patcher not to run without permission-mode bypassPermissions")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read stub: %v", err)
	}
	if !strings.Contains(string(got), "Claude Code 1.2.3") {
		t.Fatalf("expected executable to remain unchanged")
	}
}

func TestMaybePatchExecutableAppliesCustomClaudePatchWhenPermissionModeDisabled(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	dir := t.TempDir()
	path := writeClaudeVersionStub(t, dir, "Claude Code 1.2.3")
	setStubPath(t, dir)

	outcome, err := maybePatchExecutable([]string{"claude"}, patchOptionsForVersionReplacement(`echo "Claude Code 9.9.9"`), filepath.Join(dir, "config.json"), io.Discard)
	if err != nil {
		t.Fatalf("maybePatchExecutable error: %v", err)
	}
	if outcome == nil || !outcome.Applied {
		t.Fatalf("expected custom patch to be applied, got %#v", outcome)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read stub: %v", err)
	}
	if !strings.Contains(string(got), "Claude Code 9.9.9") {
		t.Fatalf("expected custom patch to update executable, got %q", string(got))
	}
}

func TestMaybePatchExecutableRestoresClaudeWhenYoloDisabled(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	dir := t.TempDir()
	setStubPath(t, dir)

	path := filepath.Join(dir, "claude")
	if runtime.GOOS == "windows" {
		path += ".cmd"
	}
	original := []byte("function FI(H){if(H===\"policySettings\"){let L=sqA();if(L&&Object.keys(L).length>0)return L}let $=L4(H);if(!$)return null;let{settings:A}=DmA($);return A}")
	if err := os.WriteFile(path, original, 0o700); err != nil {
		t.Fatalf("write original claude: %v", err)
	}
	runClaudeProbeFn = func(path string, arg string) (string, error) {
		return "Claude Code 1.2.3", nil
	}

	configPath := filepath.Join(dir, "config.json")
	if _, err := maybePatchExecutable(yoloClaudeArgs("claude"), exePatchOptions{
		enabledFlag:    true,
		policySettings: true,
	}, configPath, io.Discard); err != nil {
		t.Fatalf("apply yolo patch: %v", err)
	}

	patched, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched claude: %v", err)
	}
	if string(patched) == string(original) {
		t.Fatalf("expected executable to be patched before restore")
	}

	var log bytes.Buffer
	outcome, err := maybePatchExecutable([]string{"claude"}, exePatchOptions{
		enabledFlag:    true,
		policySettings: true,
	}, configPath, &log)
	if err != nil {
		t.Fatalf("maybePatchExecutable error: %v", err)
	}
	if outcome != nil {
		t.Fatalf("expected nil outcome when yolo is disabled, got %#v", outcome)
	}
	if !strings.Contains(log.String(), "yolo disabled; restoring original Claude executable") {
		t.Fatalf("expected restore log, got %q", log.String())
	}

	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read restored claude: %v", err)
	}
	if string(restored) != string(original) {
		t.Fatalf("expected executable to be restored")
	}
	if _, err := os.Stat(originalBackupPath(path)); !os.IsNotExist(err) {
		t.Fatalf("expected backup to be removed, got err=%v", err)
	}

	historyStore, err := config.NewPatchHistoryStore(configPath)
	if err != nil {
		t.Fatalf("new patch history store: %v", err)
	}
	history, err := historyStore.Load()
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(history.Entries) != 0 {
		t.Fatalf("expected patch history to be cleared, got %d entries", len(history.Entries))
	}
}

func TestMaybePatchExecutableDoesNotRestoreClaudeWhenPermissionModeDisabledDryRun(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	dir := t.TempDir()
	setStubPath(t, dir)

	path := filepath.Join(dir, "claude")
	if runtime.GOOS == "windows" {
		path += ".cmd"
	}

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

	configPath := filepath.Join(dir, "config.json")
	resolvedPath, err := resolveExecutablePath(path)
	if err != nil {
		t.Fatalf("resolveExecutablePath error: %v", err)
	}
	historyStore, err := config.NewPatchHistoryStore(configPath)
	if err != nil {
		t.Fatalf("new patch history store: %v", err)
	}
	if err := historyStore.Update(func(h *config.PatchHistory) error {
		h.Upsert(config.PatchHistoryEntry{
			Path:          resolvedPath,
			SpecsSHA256:   patchSpecsHash(specs),
			PatchedSHA256: hashBytes(patched),
			ProxyVersion:  currentProxyVersion(),
		})
		return nil
	}); err != nil {
		t.Fatalf("seed history: %v", err)
	}

	var log bytes.Buffer
	outcome, err := maybePatchExecutable([]string{"claude"}, exePatchOptions{
		enabledFlag:    true,
		policySettings: true,
		dryRun:         true,
	}, configPath, &log)
	if err != nil {
		t.Fatalf("maybePatchExecutable error: %v", err)
	}
	if outcome != nil {
		t.Fatalf("expected nil outcome when only built-in patch is disabled in dry-run, got %#v", outcome)
	}
	if !strings.Contains(log.String(), "dry-run enabled; would restore original Claude executable") {
		t.Fatalf("expected dry-run restore log, got %q", log.String())
	}

	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read current claude: %v", err)
	}
	if string(current) != string(patched) {
		t.Fatalf("expected dry-run to leave executable unchanged")
	}
	if _, err := os.Stat(originalBackupPath(path)); err != nil {
		t.Fatalf("expected backup to remain in dry-run: %v", err)
	}
	history, err := historyStore.Load()
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(history.Entries) != 1 {
		t.Fatalf("expected patch history to remain in dry-run, got %d entries", len(history.Entries))
	}
}

func TestMaybePatchExecutableRestoresModifiedPatchedClaudeWhenYoloDisabled(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	dir := t.TempDir()
	setStubPath(t, dir)

	path := filepath.Join(dir, "claude")
	if runtime.GOOS == "windows" {
		path += ".cmd"
	}

	original := []byte("function FI(H){if(H===\"policySettings\"){let L=sqA();if(L&&Object.keys(L).length>0)return L}let $=L4(H);if(!$)return null;let{settings:A}=DmA($);return A}")
	specs, err := policySettingsSpecs()
	if err != nil {
		t.Fatalf("policySettingsSpecs error: %v", err)
	}
	patched, _, err := applyExePatches(original, specs, io.Discard, false)
	if err != nil {
		t.Fatalf("applyExePatches error: %v", err)
	}
	current := append(append([]byte{}, patched...), []byte("\nextra-glibc-state")...)

	if err := os.WriteFile(path, current, 0o700); err != nil {
		t.Fatalf("write patched claude: %v", err)
	}
	if err := os.WriteFile(originalBackupPath(path), original, 0o700); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	configPath := filepath.Join(dir, "config.json")
	resolvedPath, err := resolveExecutablePath(path)
	if err != nil {
		t.Fatalf("resolveExecutablePath error: %v", err)
	}
	historyStore, err := config.NewPatchHistoryStore(configPath)
	if err != nil {
		t.Fatalf("new patch history store: %v", err)
	}
	if err := historyStore.Update(func(h *config.PatchHistory) error {
		h.Upsert(config.PatchHistoryEntry{
			Path:          resolvedPath,
			SpecsSHA256:   patchSpecsHash(specs),
			PatchedSHA256: hashBytes(patched),
			ProxyVersion:  currentProxyVersion(),
		})
		return nil
	}); err != nil {
		t.Fatalf("seed history: %v", err)
	}

	var log bytes.Buffer
	outcome, err := maybePatchExecutable([]string{"claude"}, exePatchOptions{
		enabledFlag:    true,
		policySettings: true,
	}, configPath, &log)
	if err != nil {
		t.Fatalf("maybePatchExecutable error: %v", err)
	}
	if outcome != nil {
		t.Fatalf("expected nil outcome when yolo is disabled, got %#v", outcome)
	}
	if !strings.Contains(log.String(), "yolo disabled; restoring original Claude executable") {
		t.Fatalf("expected restore log, got %q", log.String())
	}

	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read restored claude: %v", err)
	}
	if string(restored) != string(original) {
		t.Fatalf("expected executable to be restored")
	}
	if _, err := os.Stat(originalBackupPath(path)); !os.IsNotExist(err) {
		t.Fatalf("expected backup to be removed, got err=%v", err)
	}
	history, err := historyStore.Load()
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(history.Entries) != 0 {
		t.Fatalf("expected patch history to be cleared, got %d entries", len(history.Entries))
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
	runClaudeProbeFn = func(path string, arg string) (string, error) {
		return "synthetic startup failure", os.ErrInvalid
	}
	var log bytes.Buffer
	outcome, err := maybePatchExecutable(yoloClaudeArgs("claude"), patchOptionsForVersionReplacement(`echo "Claude Code 9.9.9"`), configPath, &log)
	if err != nil {
		t.Fatalf("maybePatchExecutable error: %v", err)
	}

	if runtime.GOOS == "windows" {
		// On Windows, probe failures are deferred to async readiness
		// verification — the synchronous probe is skipped entirely and
		// maybePatchExecutable returns a non-nil outcome with
		// NeedsVerification + RollbackOnStartupFailure set.
		if outcome == nil {
			t.Fatalf("expected non-nil outcome on Windows (async verification path)")
		}
		if !outcome.NeedsVerification {
			t.Fatalf("expected NeedsVerification=true on Windows")
		}
		if !outcome.RollbackOnStartupFailure {
			t.Fatalf("expected RollbackOnStartupFailure=true on Windows")
		}
	} else {
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
	outcome, err := maybePatchExecutable(yoloClaudeArgs("claude"), patchOptionsForVersionReplacement(`echo "Claude Code 1.2.4"`), configPath, &log)
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
