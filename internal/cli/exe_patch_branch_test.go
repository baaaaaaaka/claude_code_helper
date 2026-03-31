package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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
	prevGlibcHostEligible := glibcCompatHostEligibleFn
	prevRestore := restoreExecutableFromBackupFn
	prevCleanup := cleanupPatchHistoryFn
	prevRecordFailure := recordPatchFailureFn
	prevGOOS := runtimeGOOS
	prevReadinessPolicy := patchReadinessPolicyFn
	prevMaybePatchCtx := maybePatchExecutableCtxFn
	prevWaitReady := waitPatchedExecutableReadyFn
	prevRunWithProfileOptions := runWithProfileOptionsFn
	prevRunTargetWithFallback := runTargetWithFallbackWithOptionsFn
	prevReleasePatchPrepMemory := releasePatchPrepMemoryFn
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
		glibcCompatHostEligibleFn = prevGlibcHostEligible
		restoreExecutableFromBackupFn = prevRestore
		cleanupPatchHistoryFn = prevCleanup
		recordPatchFailureFn = prevRecordFailure
		runtimeGOOS = prevGOOS
		patchReadinessPolicyFn = prevReadinessPolicy
		maybePatchExecutableCtxFn = prevMaybePatchCtx
		waitPatchedExecutableReadyFn = prevWaitReady
		runWithProfileOptionsFn = prevRunWithProfileOptions
		runTargetWithFallbackWithOptionsFn = prevRunTargetWithFallback
		releasePatchPrepMemoryFn = prevReleasePatchPrepMemory
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

func assertSameExistingPath(t *testing.T, got string, want string) {
	t.Helper()
	gotInfo, err := os.Stat(got)
	if err != nil {
		t.Fatalf("stat got path %q: %v", got, err)
	}
	wantInfo, err := os.Stat(want)
	if err != nil {
		t.Fatalf("stat want path %q: %v", want, err)
	}
	if os.SameFile(gotInfo, wantInfo) {
		return
	}
	gotEval := got
	if resolved, err := filepath.EvalSymlinks(got); err == nil {
		gotEval = resolved
	}
	wantEval := want
	if resolved, err := filepath.EvalSymlinks(want); err == nil {
		wantEval = resolved
	}
	if sameFilePath(gotEval, wantEval) {
		return
	}
	t.Fatalf("expected path %q, got %q", want, got)
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

func TestMaybePatchExecutableRetriesKnownFailure(t *testing.T) {
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
	if outcome == nil || !outcome.Applied {
		t.Fatalf("expected patch retry outcome, got %#v", outcome)
	}
	if !strings.Contains(log.String(), "previous failure recorded") {
		t.Fatalf("expected retry log, got %q", log.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read stub: %v", err)
	}
	if !strings.Contains(string(got), "Claude Code 9.9.9") {
		t.Fatalf("expected executable to be patched, got %q", string(got))
	}
}

func TestMaybePatchExecutableAlreadyPatchedSkipsVersionProbe(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)
	runtimeGOOS = "linux"

	dir := t.TempDir()
	path := writeClaudeVersionStub(t, dir, "Claude Code 1.2.3")
	setStubPath(t, dir)

	configPath := filepath.Join(dir, "config.json")
	if _, err := maybePatchExecutable(yoloClaudeArgs("claude"), patchOptionsForVersionReplacement(`echo "Claude Code 9.9.9"`), configPath, io.Discard); err != nil {
		t.Fatalf("initial patch error: %v", err)
	}

	resolveCalled := false
	resolveClaudeVersionFn = func(path string) string {
		resolveCalled = true
		return "1.2.3"
	}
	skipCalled := false
	shouldSkipPatchFailureFn = func(configPath string, proxyVersion string, claudeVersion string, claudeSHA string) (bool, error) {
		skipCalled = true
		return false, nil
	}

	outcome, err := maybePatchExecutable(yoloClaudeArgs("claude"), patchOptionsForVersionReplacement(`echo "Claude Code 9.9.9"`), configPath, io.Discard)
	if err != nil {
		t.Fatalf("maybePatchExecutable error: %v", err)
	}
	if outcome == nil || !outcome.AlreadyPatched {
		t.Fatalf("expected already patched outcome, got %#v", outcome)
	}
	if resolveCalled {
		t.Fatalf("expected fast path to skip version probe")
	}
	if skipCalled {
		t.Fatalf("expected fast path to skip patch-failure lookup")
	}
	assertSameExistingPath(t, outcome.SourcePath, path)
	assertSameExistingPath(t, outcome.TargetPath, path)
}

func TestMaybePatchExecutableWindowsHistoricalVerificationPreservesTargetVersion(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	runtimeGOOS = "windows"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	_, patched := writeBuiltInPatchedClaudeBinary(t, path)

	execLookPathFn = func(file string) (string, error) { return path, nil }
	resolveExecutablePathFn = func(path string) (string, error) { return path, nil }

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
			ProxyVersion:  currentProxyVersion(),
		})
		return nil
	}); err != nil {
		t.Fatalf("seed history: %v", err)
	}

	versionCalls := 0
	resolveClaudeVersionFn = func(path string) string {
		versionCalls++
		return "2.1.3"
	}
	runClaudeTimedProbeFn = func(ctx context.Context, path string, arg string, timeout time.Duration) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}

	outcome, err := maybePatchExecutableWithContext(ctx, yoloClaudeArgs("claude"), exePatchOptions{
		enabledFlag:    true,
		policySettings: true,
	}, filepath.Join(dir, "config.json"), io.Discard)
	if err != nil {
		t.Fatalf("maybePatchExecutable error: %v", err)
	}
	defer func() {
		cancel()
		if outcome != nil && outcome.readiness != nil {
			<-outcome.readiness.done
		}
	}()
	if outcome == nil {
		t.Fatalf("expected non-nil outcome")
	}
	if !outcome.AlreadyPatched || !outcome.NeedsVerification {
		t.Fatalf("expected historical verification outcome, got %#v", outcome)
	}
	if outcome.TargetVersion != "2.1.3" {
		t.Fatalf("expected target version to be preserved, got %q", outcome.TargetVersion)
	}
	if versionCalls != 1 {
		t.Fatalf("expected one version probe for windows historical verification, got %d", versionCalls)
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

func TestMaybePatchExecutableAllowsBuiltInClaudePatchWithoutBypassWhenForced(t *testing.T) {
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
		enabledFlag:               true,
		policySettings:            true,
		allowBuiltInWithoutBypass: true,
	}, filepath.Join(dir, "config.json"), io.Discard)
	if err != nil {
		t.Fatalf("maybePatchExecutable error: %v", err)
	}
	if outcome == nil {
		t.Fatalf("expected non-nil outcome after forced built-in patch preparation")
	}
	if !patchCalled {
		t.Fatalf("expected built-in patcher to run when forced without bypass flag")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read stub: %v", err)
	}
	if !strings.Contains(string(got), "Claude Code 1.2.3") {
		t.Fatalf("expected executable to remain unchanged with stub patcher")
	}
}

func TestMaybePatchExecutableRestoresClaudeWhenRulesModeCannotApplyBuiltins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("restore semantics are covered on non-windows targets")
	}
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	cases := []struct {
		name string
		opts exePatchOptions
	}{
		{
			name: "exe patch disabled",
			opts: exePatchOptions{
				allowBuiltInWithoutBypass: true,
			},
		},
		{
			name: "policy patch disabled",
			opts: exePatchOptions{
				enabledFlag:               true,
				allowBuiltInWithoutBypass: true,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			setStubPath(t, dir)

			path := filepath.Join(dir, "claude")
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
			outcome, err := maybePatchExecutable([]string{"claude"}, tc.opts, configPath, &log)
			if err != nil {
				t.Fatalf("maybePatchExecutable error: %v", err)
			}
			if outcome != nil {
				t.Fatalf("expected nil outcome when built-in rules patch is unavailable, got %#v", outcome)
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
		})
	}
}

func TestMaybePatchExecutableTracksBuiltInClaudePatchState(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	t.Run("active after built-in patch", func(t *testing.T) {
		dir := t.TempDir()
		setStubPath(t, dir)

		path := filepath.Join(dir, "claude")
		original := []byte("function FI(H){if(H===\"policySettings\"){let L=sqA();if(L&&Object.keys(L).length>0)return L}let $=L4(H);if(!$)return null;let{settings:A}=DmA($);return A}")
		if err := os.WriteFile(path, original, 0o700); err != nil {
			t.Fatalf("write original claude: %v", err)
		}
		runClaudeProbeFn = func(path string, arg string) (string, error) {
			return "Claude Code 1.2.3", nil
		}

			outcome, err := maybePatchExecutable(yoloClaudeArgs(path), exePatchOptions{
			enabledFlag:    true,
			policySettings: true,
		}, filepath.Join(dir, "config.json"), io.Discard)
		if err != nil {
			t.Fatalf("maybePatchExecutable error: %v", err)
		}
		if outcome == nil || !outcome.BuiltInClaudePatchActive {
			t.Fatalf("expected built-in Claude patch to be active, got %#v", outcome)
		}
	})

	t.Run("inactive when built-in patch misses", func(t *testing.T) {
		dir := t.TempDir()
		setStubPath(t, dir)

		path := filepath.Join(dir, "claude")
		if err := os.WriteFile(path, []byte("#!/bin/sh\necho not-claude\n"), 0o700); err != nil {
			t.Fatalf("write unmatched claude: %v", err)
		}

			outcome, err := maybePatchExecutable([]string{path}, exePatchOptions{
			enabledFlag:               true,
			policySettings:            true,
			allowBuiltInWithoutBypass: true,
		}, filepath.Join(dir, "config.json"), io.Discard)
		if err != nil {
			t.Fatalf("maybePatchExecutable error: %v", err)
		}
		if outcome == nil {
			t.Fatalf("expected non-nil outcome")
		}
		if outcome.BuiltInClaudePatchActive {
			t.Fatalf("expected built-in Claude patch to remain inactive, got %#v", outcome)
		}
	})
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
	if runtime.GOOS == "windows" {
		t.Skip("restore semantics are covered on non-windows targets")
	}
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
	glibcCompatHostEligibleFn = func() bool { return true }

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

func TestMaybePatchExecutableReturnsCompatPreparationError(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)
	glibcCompatHostEligibleFn = func() bool { return true }

	dir := t.TempDir()
	path := writeClaudeVersionStub(t, dir, "Claude Code 1.2.3")
	setStubPath(t, dir)
	wantErr := io.EOF
	applyClaudeGlibcCompatPatchFn = func(path string, opts exePatchOptions, log io.Writer, dryRun bool, outcome *patchOutcome) (*patchOutcome, bool, error) {
		return outcome, false, wantErr
	}

	outcome, err := maybePatchExecutable([]string{"claude"}, exePatchOptions{
		enabledFlag: true,
		glibcCompat: true,
	}, filepath.Join(dir, "config.json"), io.Discard)
	if err == nil || !errors.Is(err, wantErr) {
		t.Fatalf("expected compat preparation error %v, got outcome=%#v err=%v", wantErr, outcome, err)
	}
	resolvedPath, resolveErr := resolveExecutablePath(path)
	if resolveErr != nil {
		t.Fatalf("resolveExecutablePath error: %v", resolveErr)
	}
	if outcome != nil && outcome.TargetPath == resolvedPath {
		t.Fatalf("expected nil outcome on compat preparation failure, got %#v", outcome)
	}
}

func TestMaybePatchExecutableKnownFailureWithoutCompatOutcomeUsesResolvedPath(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	dir := t.TempDir()
	path := writeClaudeVersionStub(t, dir, "Claude Code 1.2.3")
	setStubPath(t, dir)
	shouldSkipPatchFailureFn = func(configPath string, proxyVersion string, claudeVersion string, claudeSHA string) (bool, error) {
		return true, nil
	}

	outcome, err := maybePatchExecutable([]string{"claude"}, patchOptionsForVersionReplacement("Claude Code 9.9.9"), filepath.Join(dir, "config.json"), io.Discard)
	if err != nil {
		t.Fatalf("maybePatchExecutable error: %v", err)
	}
	if outcome == nil {
		t.Fatalf("expected non-nil outcome")
	}
	if !outcome.Applied {
		t.Fatalf("expected retry to still apply patch, got %#v", outcome)
	}
	assertSameExistingPath(t, outcome.SourcePath, path)
	assertSameExistingPath(t, outcome.TargetPath, path)
	if len(outcome.LaunchArgsPrefix) != 1 {
		t.Fatalf("unexpected launch prefix: %#v", outcome.LaunchArgsPrefix)
	}
	assertSameExistingPath(t, outcome.LaunchArgsPrefix[0], path)
	if outcome.SourceSHA256 != "" {
		t.Fatalf("expected version-based skip to leave SourceSHA256 empty, got %q", outcome.SourceSHA256)
	}
}

func TestMaybePatchExecutableProbeCompatRescueBackfillsLaunchPrefix(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	dir := t.TempDir()
	writeClaudeVersionStub(t, dir, "Claude Code 1.2.3")
	setStubPath(t, dir)

	probeCalls := 0
	mirrorPath := filepath.Join(dir, "mirror", "claude")
	if runtime.GOOS == "windows" {
		mirrorPath += ".cmd"
	}
	if err := os.MkdirAll(filepath.Dir(mirrorPath), 0o755); err != nil {
		t.Fatalf("mkdir mirror: %v", err)
	}
	if err := os.WriteFile(mirrorPath, []byte("mirror"), 0o700); err != nil {
		t.Fatalf("write mirror: %v", err)
	}

	runClaudeProbeFn = func(path string, arg string) (string, error) {
		probeCalls++
		if probeCalls == 1 {
			return path + ": /lib64/libc.so.6: version `GLIBC_2.25' not found", os.ErrInvalid
		}
		if path != mirrorPath {
			t.Fatalf("expected compat rescue to reprobe mirror path %q, got %q", mirrorPath, path)
		}
		return "Claude Code 1.2.3", nil
	}
	applyClaudeGlibcCompatPatchFn = func(path string, opts exePatchOptions, log io.Writer, dryRun bool, outcome *patchOutcome) (*patchOutcome, bool, error) {
		if outcome == nil {
			outcome = &patchOutcome{}
		}
		outcome.SourcePath = path
		outcome.TargetPath = mirrorPath
		outcome.LaunchArgsPrefix = nil
		outcome.Applied = false
		return outcome, true, nil
	}

	outcome, err := maybePatchExecutable([]string{"claude"}, exePatchOptions{
		enabledFlag: true,
		glibcCompat: true,
	}, filepath.Join(dir, "config.json"), io.Discard)
	if err != nil {
		t.Fatalf("maybePatchExecutable error: %v", err)
	}
	if outcome == nil {
		t.Fatalf("expected non-nil outcome")
	}
	if len(outcome.LaunchArgsPrefix) != 1 {
		t.Fatalf("expected compat rescue to backfill launch prefix, got %#v", outcome.LaunchArgsPrefix)
	}
	assertSameExistingPath(t, outcome.LaunchArgsPrefix[0], mirrorPath)
	if probeCalls != 2 {
		t.Fatalf("expected 2 probe calls, got %d", probeCalls)
	}
}

func TestMaybePatchExecutableFallsBackToCompatWrapperWhenMirrorProbeFails(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	dir := t.TempDir()
	writeClaudeVersionStub(t, dir, "Claude Code 1.2.3")
	setStubPath(t, dir)
	glibcCompatHostEligibleFn = func() bool { return true }
	resolveClaudeVersionFn = func(path string) string { return "1.2.3" }
	shouldSkipPatchFailureFn = func(configPath string, proxyVersion string, claudeVersion string, claudeSHA string) (bool, error) {
		return false, nil
	}

	mirrorPath := filepath.Join(dir, "mirror", "claude")
	wrapperPath := filepath.Join(dir, "wrapper", "claude")
	if runtime.GOOS == "windows" {
		mirrorPath += ".cmd"
		wrapperPath += ".cmd"
	}
	for _, path := range []string{mirrorPath, wrapperPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir compat path: %v", err)
		}
		if err := os.WriteFile(path, []byte("compat"), 0o700); err != nil {
			t.Fatalf("write compat path: %v", err)
		}
	}

	applyCalls := 0
	runClaudeProbeFn = func(path string, arg string) (string, error) {
		switch path {
		case mirrorPath:
			return "Error: signal: segmentation fault (core dumped)", os.ErrInvalid
		case wrapperPath:
			return "Claude Code 1.2.3", nil
		default:
			return "Claude Code 1.2.3", nil
		}
	}
	applyClaudeGlibcCompatPatchFn = func(path string, opts exePatchOptions, log io.Writer, dryRun bool, outcome *patchOutcome) (*patchOutcome, bool, error) {
		applyCalls++
		if outcome == nil {
			outcome = &patchOutcome{}
		}
		outcome.SourcePath = path
		if opts.glibcCompatPreferWrapper {
			outcome.TargetPath = wrapperPath
			outcome.LaunchArgsPrefix = []string{wrapperPath}
			return outcome, true, nil
		}
		outcome.TargetPath = mirrorPath
		outcome.LaunchArgsPrefix = []string{mirrorPath}
		return outcome, true, nil
	}

	outcome, err := maybePatchExecutable([]string{"claude"}, exePatchOptions{
		enabledFlag: true,
		glibcCompat: true,
	}, filepath.Join(dir, "config.json"), io.Discard)
	if err != nil {
		t.Fatalf("maybePatchExecutable error: %v", err)
	}
	if outcome == nil {
		t.Fatalf("expected non-nil outcome")
	}
	assertSameExistingPath(t, outcome.TargetPath, wrapperPath)
	if len(outcome.LaunchArgsPrefix) != 1 {
		t.Fatalf("unexpected launch prefix: %#v", outcome.LaunchArgsPrefix)
	}
	assertSameExistingPath(t, outcome.LaunchArgsPrefix[0], wrapperPath)
	if applyCalls != 2 {
		t.Fatalf("expected mirror prepare plus wrapper fallback, got %d apply calls", applyCalls)
	}
}

func TestMaybePatchExecutableRestoresSourceBeforePreparingEL7Mirror(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip glibc compat flow on windows")
	}
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	dir := t.TempDir()
	glibcCompatHostEligibleFn = func() bool { return true }

	sourcePath := filepath.Join(dir, "claude")
	original, _ := writeBuiltInPatchedClaudeBinary(t, sourcePath)
	mirrorPath := filepath.Join(dir, "mirror", filepath.Base(sourcePath))

	execLookPathFn = func(file string) (string, error) { return sourcePath, nil }
	resolveExecutablePathFn = func(path string) (string, error) { return sourcePath, nil }
	resolveClaudeVersionFn = func(path string) string { return "2.1.3" }
	shouldSkipPatchFailureFn = func(configPath string, proxyVersion string, claudeVersion string, claudeSHA string) (bool, error) {
		return false, nil
	}
	runClaudeProbeFn = func(path string, arg string) (string, error) {
		return "Claude Code 2.1.3", nil
	}
	applyClaudeGlibcCompatPatchFn = func(path string, opts exePatchOptions, log io.Writer, dryRun bool, outcome *patchOutcome) (*patchOutcome, bool, error) {
		gotSource, err := os.ReadFile(sourcePath)
		if err != nil {
			t.Fatalf("read source during compat prep: %v", err)
		}
		if string(gotSource) != string(original) {
			t.Fatalf("expected source to be restored before compat prep")
		}
		if err := os.MkdirAll(filepath.Dir(mirrorPath), 0o755); err != nil {
			t.Fatalf("mkdir mirror dir: %v", err)
		}
		if err := os.WriteFile(mirrorPath, gotSource, 0o700); err != nil {
			t.Fatalf("write mirror: %v", err)
		}
		if outcome == nil {
			outcome = &patchOutcome{}
		}
		outcome.SourcePath = path
		outcome.TargetPath = mirrorPath
		outcome.LaunchArgsPrefix = []string{mirrorPath}
		outcome.Applied = false
		return outcome, true, nil
	}

	outcome, err := maybePatchExecutable([]string{"claude"}, exePatchOptions{
		enabledFlag: true,
		glibcCompat: true,
	}, filepath.Join(dir, "config.json"), io.Discard)
	if err != nil {
		t.Fatalf("maybePatchExecutable error: %v", err)
	}
	if outcome == nil {
		t.Fatalf("expected mirror outcome, got %#v", outcome)
	}
	assertSameExistingPath(t, outcome.TargetPath, mirrorPath)
	gotSource, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read source after compat prep: %v", err)
	}
	if string(gotSource) != string(original) {
		t.Fatalf("expected source to remain restored after compat prep")
	}
	gotMirror, err := os.ReadFile(mirrorPath)
	if err != nil {
		t.Fatalf("read mirror after compat prep: %v", err)
	}
	if string(gotMirror) != string(original) {
		t.Fatalf("expected mirror to be copied from restored source")
	}
	if _, err := os.Stat(originalBackupPath(sourcePath)); !os.IsNotExist(err) {
		t.Fatalf("expected source backup to be removed after restore, got err=%v", err)
	}
}

func TestMaybePatchExecutableRestoresExistingEL7MirrorWhenYoloDisabled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip glibc compat flow on windows")
	}
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	dir := t.TempDir()
	glibcCompatHostEligibleFn = func() bool { return true }

	sourcePath := filepath.Join(dir, "claude")
	mirrorPath := filepath.Join(dir, "mirror", filepath.Base(sourcePath))
	if err := os.MkdirAll(filepath.Dir(mirrorPath), 0o755); err != nil {
		t.Fatalf("mkdir mirror dir: %v", err)
	}
	original, _ := writeBuiltInPatchedClaudeBinary(t, mirrorPath)
	if err := os.WriteFile(sourcePath, original, 0o700); err != nil {
		t.Fatalf("write source: %v", err)
	}

	execLookPathFn = func(file string) (string, error) { return sourcePath, nil }
	resolveExecutablePathFn = func(path string) (string, error) { return sourcePath, nil }
	resolveClaudeVersionFn = func(path string) string { return "2.1.3" }
	shouldSkipPatchFailureFn = func(configPath string, proxyVersion string, claudeVersion string, claudeSHA string) (bool, error) {
		return false, nil
	}
	runClaudeProbeFn = func(path string, arg string) (string, error) {
		return "Claude Code 2.1.3", nil
	}
	applyClaudeGlibcCompatPatchFn = func(path string, opts exePatchOptions, log io.Writer, dryRun bool, outcome *patchOutcome) (*patchOutcome, bool, error) {
		if outcome == nil {
			outcome = &patchOutcome{}
		}
		outcome.SourcePath = path
		outcome.TargetPath = mirrorPath
		outcome.LaunchArgsPrefix = []string{mirrorPath}
		outcome.Applied = false
		return outcome, true, nil
	}

	outcome, err := maybePatchExecutable([]string{"claude"}, exePatchOptions{
		enabledFlag: true,
		glibcCompat: true,
	}, filepath.Join(dir, "config.json"), io.Discard)
	if err != nil {
		t.Fatalf("maybePatchExecutable error: %v", err)
	}
	if outcome == nil {
		t.Fatalf("expected mirror outcome, got %#v", outcome)
	}
	assertSameExistingPath(t, outcome.TargetPath, mirrorPath)
	gotMirror, err := os.ReadFile(mirrorPath)
	if err != nil {
		t.Fatalf("read mirror after restore: %v", err)
	}
	if string(gotMirror) != string(original) {
		t.Fatalf("expected mirror to be restored on non-yolo launch")
	}
	if _, err := os.Stat(originalBackupPath(mirrorPath)); !os.IsNotExist(err) {
		t.Fatalf("expected mirror backup to be removed after restore, got err=%v", err)
	}
	gotSource, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read source after restore: %v", err)
	}
	if string(gotSource) != string(original) {
		t.Fatalf("expected source to remain unchanged")
	}
}

func TestMaybePatchExecutablePatchesMirrorInsteadOfSharedSourceOnEL7(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	dir := t.TempDir()
	setStubPath(t, dir)
	glibcCompatHostEligibleFn = func() bool { return true }

	sourcePath := filepath.Join(dir, "claude")
	if runtime.GOOS == "windows" {
		sourcePath += ".cmd"
	}
	original := []byte("function FI(H){if(H===\"policySettings\"){let L=sqA();if(L&&Object.keys(L).length>0)return L}let $=L4(H);if(!$)return null;let{settings:A}=DmA($);return A}")
	if err := os.WriteFile(sourcePath, original, 0o700); err != nil {
		t.Fatalf("write source claude: %v", err)
	}
	mirrorDir := filepath.Join(dir, "mirror")
	if err := os.MkdirAll(mirrorDir, 0o755); err != nil {
		t.Fatalf("mkdir mirror dir: %v", err)
	}
	mirrorPath := filepath.Join(mirrorDir, filepath.Base(sourcePath))
	if err := os.WriteFile(mirrorPath, original, 0o700); err != nil {
		t.Fatalf("write mirror claude: %v", err)
	}

	runClaudeProbeFn = func(path string, arg string) (string, error) {
		return "Claude Code 1.2.3", nil
	}
	applyClaudeGlibcCompatPatchFn = func(path string, opts exePatchOptions, log io.Writer, dryRun bool, outcome *patchOutcome) (*patchOutcome, bool, error) {
		if outcome == nil {
			outcome = &patchOutcome{}
		}
		outcome.SourcePath = path
		outcome.TargetPath = mirrorPath
		outcome.LaunchArgsPrefix = []string{mirrorPath}
		return outcome, true, nil
	}

	outcome, err := maybePatchExecutable(yoloClaudeArgs("claude"), exePatchOptions{
		enabledFlag:    true,
		policySettings: true,
		glibcCompat:    true,
	}, filepath.Join(dir, "config.json"), io.Discard)
	if err != nil {
		t.Fatalf("maybePatchExecutable error: %v", err)
	}
	if outcome == nil {
		t.Fatalf("expected mirror patch outcome, got %#v", outcome)
	}
	assertSameExistingPath(t, outcome.TargetPath, mirrorPath)

	gotSource, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read source claude: %v", err)
	}
	if string(gotSource) != string(original) {
		t.Fatalf("expected shared source to remain unchanged")
	}
	gotMirror, err := os.ReadFile(mirrorPath)
	if err != nil {
		t.Fatalf("read mirror claude: %v", err)
	}
	if string(gotMirror) == string(original) {
		t.Fatalf("expected mirror executable to be patched")
	}
}

func TestMaybePatchExecutableKnownFailureUsesCompatLaunchPrefixOnEL7(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	dir := t.TempDir()
	setStubPath(t, dir)
	glibcCompatHostEligibleFn = func() bool { return true }
	shouldSkipPatchFailureFn = func(configPath string, proxyVersion string, claudeVersion string, claudeSHA string) (bool, error) {
		return true, nil
	}

	path := writeClaudeVersionStub(t, dir, "Claude Code 1.2.3")
	mirrorPath := filepath.Join(dir, "mirror", "claude")
	if runtime.GOOS == "windows" {
		mirrorPath += ".cmd"
	}
	if err := os.MkdirAll(filepath.Dir(mirrorPath), 0o755); err != nil {
		t.Fatalf("mkdir mirror: %v", err)
	}
	if err := os.WriteFile(mirrorPath, []byte("mirror"), 0o700); err != nil {
		t.Fatalf("write mirror: %v", err)
	}
	applyClaudeGlibcCompatPatchFn = func(path string, opts exePatchOptions, log io.Writer, dryRun bool, outcome *patchOutcome) (*patchOutcome, bool, error) {
		if outcome == nil {
			outcome = &patchOutcome{}
		}
		outcome.SourcePath = path
		outcome.TargetPath = mirrorPath
		outcome.LaunchArgsPrefix = []string{mirrorPath}
		return outcome, true, nil
	}

	outcome, err := maybePatchExecutable([]string{"claude"}, exePatchOptions{
		enabledFlag: true,
		glibcCompat: true,
	}, filepath.Join(dir, "config.json"), io.Discard)
	if err != nil {
		t.Fatalf("maybePatchExecutable error: %v", err)
	}
	if outcome == nil {
		t.Fatalf("expected non-nil outcome")
	}
	assertSameExistingPath(t, outcome.SourcePath, path)
	assertSameExistingPath(t, outcome.TargetPath, mirrorPath)
	if len(outcome.LaunchArgsPrefix) != 1 {
		t.Fatalf("unexpected launch prefix: %#v", outcome.LaunchArgsPrefix)
	}
	assertSameExistingPath(t, outcome.LaunchArgsPrefix[0], mirrorPath)
}
