package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

func TestVerifyWindowsPatchReadinessRetriesUntilSuccess(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	policy := patchReadinessPolicy{
		InitialProbeTimeout: time.Millisecond,
		RetryProbeTimeout:   time.Millisecond,
		RetryInterval:       time.Millisecond,
		TotalBudget:         10 * time.Millisecond,
	}

	calls := 0
	runClaudeTimedProbeFn = func(ctx context.Context, path string, arg string, timeout time.Duration) (string, error) {
		calls++
		if calls == 1 {
			return "", context.DeadlineExceeded
		}
		return "Claude Code 1.2.3", nil
	}

	out, err := verifyWindowsPatchReadiness(context.Background(), "claude", policy)
	if err != nil {
		t.Fatalf("verifyWindowsPatchReadiness error: %v", err)
	}
	if out != "Claude Code 1.2.3" {
		t.Fatalf("unexpected output %q", out)
	}
	if calls != 2 {
		t.Fatalf("expected 2 probe calls, got %d", calls)
	}
}

func TestVerifyWindowsPatchReadinessTimesOut(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	policy := patchReadinessPolicy{
		InitialProbeTimeout: time.Millisecond,
		RetryProbeTimeout:   time.Millisecond,
		RetryInterval:       time.Millisecond,
		TotalBudget:         5 * time.Millisecond,
	}

	runClaudeTimedProbeFn = func(ctx context.Context, path string, arg string, timeout time.Duration) (string, error) {
		return "", context.DeadlineExceeded
	}

	if _, err := verifyWindowsPatchReadiness(context.Background(), "claude", policy); !errors.Is(err, errPatchReadinessTimeout) {
		t.Fatalf("expected readiness timeout, got %v", err)
	}
}

func TestMaybePatchExecutableWindowsReadinessMarksVerified(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	runtimeGOOS = "windows"
	patchReadinessPolicyFn = func() patchReadinessPolicy {
		return patchReadinessPolicy{
			InitialProbeTimeout: time.Millisecond,
			RetryProbeTimeout:   time.Millisecond,
			RetryInterval:       time.Millisecond,
			TotalBudget:         10 * time.Millisecond,
			QuietDelay:          time.Hour,
		}
	}

	dir := t.TempDir()
	writeClaudeVersionStub(t, dir, "Claude Code 1.2.3")
	setStubPath(t, dir)
	configPath := filepath.Join(dir, "config.json")

	calls := 0
	runClaudeTimedProbeFn = func(ctx context.Context, path string, arg string, timeout time.Duration) (string, error) {
		calls++
		if calls == 1 {
			return "", context.DeadlineExceeded
		}
		return "Claude Code 1.2.3", nil
	}

	outcome, err := maybePatchExecutableWithContext(context.Background(), []string{"claude"}, patchOptionsForVersionReplacement(`echo "Claude Code 9.9.9"`), configPath, io.Discard)
	if err != nil {
		t.Fatalf("maybePatchExecutableWithContext error: %v", err)
	}
	if outcome == nil || !outcome.hasPatchedBinary() {
		t.Fatalf("expected patched outcome, got %#v", outcome)
	}
	if err := waitPatchedExecutableReady(context.Background(), outcome); err != nil {
		t.Fatalf("waitPatchedExecutableReady error: %v", err)
	}
	if !outcome.Verified || outcome.NeedsVerification {
		t.Fatalf("expected verified outcome after wait, got %#v", outcome)
	}
	if calls < 2 {
		t.Fatalf("expected at least 2 timed probe calls, got %d", calls)
	}

	store, err := config.NewPatchHistoryStore(configPath)
	if err != nil {
		t.Fatalf("new patch history store: %v", err)
	}
	history, err := store.Load()
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(history.Entries) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history.Entries))
	}
	if history.Entries[0].VerifiedAt.IsZero() {
		t.Fatalf("expected verified timestamp to be persisted")
	}
}

func TestMaybePatchExecutableWindowsReadinessHardFailureRestoresAndContinues(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	runtimeGOOS = "windows"
	patchReadinessPolicyFn = func() patchReadinessPolicy {
		return patchReadinessPolicy{
			InitialProbeTimeout: time.Millisecond,
			RetryProbeTimeout:   time.Millisecond,
			RetryInterval:       time.Millisecond,
			TotalBudget:         10 * time.Millisecond,
			QuietDelay:          time.Hour,
		}
	}

	dir := t.TempDir()
	path := writeClaudeVersionStub(t, dir, "Claude Code 1.2.3")
	setStubPath(t, dir)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read original stub: %v", err)
	}

	configPath := filepath.Join(dir, "config.json")
	runClaudeTimedProbeFn = func(ctx context.Context, path string, arg string, timeout time.Duration) (string, error) {
		return "synthetic startup failure", os.ErrInvalid
	}

	var log bytes.Buffer
	outcome, err := maybePatchExecutableWithContext(context.Background(), []string{"claude"}, patchOptionsForVersionReplacement(`echo "Claude Code 9.9.9"`), configPath, &log)
	if err != nil {
		t.Fatalf("maybePatchExecutableWithContext error: %v", err)
	}
	if outcome == nil {
		t.Fatalf("expected outcome for readiness worker path")
	}

	if err := waitPatchedExecutableReady(context.Background(), outcome); err != nil {
		t.Fatalf("expected readiness wait to continue after restore, got %v", err)
	}
	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read restored stub: %v", err)
	}
	if string(restored) != string(original) {
		t.Fatalf("expected executable to be restored")
	}
	if !strings.Contains(log.String(), "detected startup failure; restoring backup") {
		t.Fatalf("expected restore log, got %q", log.String())
	}
	if outcome.RollbackOnStartupFailure {
		t.Fatalf("expected rollback-on-startup-failure to be cleared after restore")
	}
	if outcome.NeedsVerification {
		t.Fatalf("expected readiness requirement to be cleared after restore")
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
		t.Fatalf("expected one patch failure, got %d", len(cfg.PatchFailures))
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
		t.Fatalf("expected patch history to be cleaned up, got %d entries", len(history.Entries))
	}
}

func TestVerifyWindowsPatchReadinessImmediateSuccess(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	policy := patchReadinessPolicy{
		InitialProbeTimeout: time.Millisecond,
		RetryProbeTimeout:   time.Millisecond,
		RetryInterval:       time.Millisecond,
		TotalBudget:         10 * time.Millisecond,
	}

	calls := 0
	runClaudeTimedProbeFn = func(ctx context.Context, path string, arg string, timeout time.Duration) (string, error) {
		calls++
		return "Claude Code 1.2.3", nil
	}

	out, err := verifyWindowsPatchReadiness(context.Background(), "claude", policy)
	if err != nil {
		t.Fatalf("verifyWindowsPatchReadiness error: %v", err)
	}
	if out != "Claude Code 1.2.3" {
		t.Fatalf("unexpected output %q", out)
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 probe call (no retries), got %d", calls)
	}
}

func TestVerifyWindowsPatchReadinessInitialTimeoutCappedByBudget(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	policy := patchReadinessPolicy{
		InitialProbeTimeout: 10 * time.Second, // much larger than budget
		RetryProbeTimeout:   time.Millisecond,
		RetryInterval:       time.Millisecond,
		TotalBudget:         5 * time.Millisecond,
	}

	var initialTimeout time.Duration
	runClaudeTimedProbeFn = func(ctx context.Context, path string, arg string, timeout time.Duration) (string, error) {
		initialTimeout = timeout
		return "Claude Code 1.2.3", nil
	}

	if _, err := verifyWindowsPatchReadiness(context.Background(), "claude", policy); err != nil {
		t.Fatalf("verifyWindowsPatchReadiness error: %v", err)
	}
	// InitialProbeTimeout (10s) should be capped to TotalBudget (5ms).
	if initialTimeout > policy.TotalBudget {
		t.Fatalf("expected initial timeout to be capped to budget (%v), got %v", policy.TotalBudget, initialTimeout)
	}
}

func TestVerifyWindowsPatchReadinessHardErrorNotRetried(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	policy := patchReadinessPolicy{
		InitialProbeTimeout: time.Millisecond,
		RetryProbeTimeout:   time.Millisecond,
		RetryInterval:       time.Millisecond,
		TotalBudget:         50 * time.Millisecond,
	}

	calls := 0
	runClaudeTimedProbeFn = func(ctx context.Context, path string, arg string, timeout time.Duration) (string, error) {
		calls++
		return "crash output", os.ErrInvalid // hard error, not DeadlineExceeded
	}

	_, err := verifyWindowsPatchReadiness(context.Background(), "claude", policy)
	if !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("expected hard error to be returned, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected hard error to stop retries after 1 call, got %d", calls)
	}
}

func TestStartPatchedExecutableReadinessVerificationFailureLogsButContinues(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	runtimeGOOS = "windows"
	patchReadinessPolicyFn = func() patchReadinessPolicy {
		return patchReadinessPolicy{
			InitialProbeTimeout: time.Millisecond,
			RetryProbeTimeout:   time.Millisecond,
			RetryInterval:       time.Millisecond,
			TotalBudget:         10 * time.Millisecond,
			QuietDelay:          time.Hour,
		}
	}

	runClaudeTimedProbeFn = func(ctx context.Context, path string, arg string, timeout time.Duration) (string, error) {
		return "Claude Code 1.2.3", nil
	}

	var log bytes.Buffer
	outcome := &patchOutcome{
		Applied:           true,
		IsClaude:          true,
		NeedsVerification: true,
		TargetPath:        "/fake/claude",
		BackupPath:        "/fake/claude.bak",
		SpecsHash:         "test-spec",
		HistoryStore:      nil, // nil store will cause markPatchedExecutableVerified to short-circuit
		LogWriter:         &log,
	}

	startPatchedExecutableReadiness(context.Background(), outcome, exePatchOptions{})

	err := waitPatchedExecutableReady(context.Background(), outcome)
	if err != nil {
		t.Fatalf("expected no error even when verification persistence fails, got %v", err)
	}
	// Verification should still be marked on the outcome even if persistence failed.
	if !outcome.Verified {
		t.Fatalf("expected Verified=true after successful probe")
	}
	if outcome.NeedsVerification {
		t.Fatalf("expected NeedsVerification=false after successful probe")
	}
}

func TestStartPatchedExecutableReadinessSkipsAlreadyVerified(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	runtimeGOOS = "windows"

	outcome := &patchOutcome{
		Applied:           true,
		IsClaude:          true,
		Verified:          true,
		NeedsVerification: false,
		TargetPath:        "/fake/claude",
		LogWriter:         io.Discard,
	}

	startPatchedExecutableReadiness(context.Background(), outcome, exePatchOptions{})

	// readiness should not have been started
	if outcome.readiness != nil {
		t.Fatalf("expected readiness to be nil for already-verified outcome")
	}

	// waitPatchedExecutableReady should be a no-op
	if err := waitPatchedExecutableReady(context.Background(), outcome); err != nil {
		t.Fatalf("expected nil error for nil readiness, got %v", err)
	}
}

func TestStartPatchedExecutableReadinessSkipsNonWindows(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	runtimeGOOS = "linux"

	outcome := &patchOutcome{
		Applied:           true,
		IsClaude:          true,
		NeedsVerification: true,
		TargetPath:        "/fake/claude",
		LogWriter:         io.Discard,
	}

	startPatchedExecutableReadiness(context.Background(), outcome, exePatchOptions{})

	if outcome.readiness != nil {
		t.Fatalf("expected readiness to be nil on non-Windows")
	}
}

func TestStartPatchedExecutableReadinessTimeoutReturnsStillPending(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	runtimeGOOS = "windows"
	patchReadinessPolicyFn = func() patchReadinessPolicy {
		return patchReadinessPolicy{
			InitialProbeTimeout: time.Millisecond,
			RetryProbeTimeout:   time.Millisecond,
			RetryInterval:       time.Millisecond,
			TotalBudget:         5 * time.Millisecond,
			QuietDelay:          time.Hour,
		}
	}

	runClaudeTimedProbeFn = func(ctx context.Context, path string, arg string, timeout time.Duration) (string, error) {
		return "", context.DeadlineExceeded
	}

	outcome := &patchOutcome{
		Applied:           true,
		IsClaude:          true,
		NeedsVerification: true,
		TargetPath:        "/fake/claude",
		BackupPath:        "/fake/claude.bak",
		LogWriter:         io.Discard,
	}

	startPatchedExecutableReadiness(context.Background(), outcome, exePatchOptions{})

	err := waitPatchedExecutableReady(context.Background(), outcome)
	if !errors.Is(err, errPatchReadinessStillPending) {
		t.Fatalf("expected errPatchReadinessStillPending, got %v", err)
	}
}

func TestHasPatchedBinary(t *testing.T) {
	requireExePatchEnabled(t)

	if (&patchOutcome{}).hasPatchedBinary() {
		t.Fatalf("expected zero-value outcome to return false")
	}
	var nilOutcome *patchOutcome
	if nilOutcome.hasPatchedBinary() {
		t.Fatalf("expected nil outcome to return false")
	}
	if !(&patchOutcome{Applied: true}).hasPatchedBinary() {
		t.Fatalf("expected Applied=true to return true")
	}
	if !(&patchOutcome{AlreadyPatched: true}).hasPatchedBinary() {
		t.Fatalf("expected AlreadyPatched=true to return true")
	}
	if !(&patchOutcome{Verified: true}).hasPatchedBinary() {
		t.Fatalf("expected Verified=true to return true")
	}
	if !(&patchOutcome{NeedsVerification: true}).hasPatchedBinary() {
		t.Fatalf("expected NeedsVerification=true to return true")
	}
}

func TestWaitPatchedExecutableReadyContextCancellation(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	runtimeGOOS = "windows"
	patchReadinessPolicyFn = func() patchReadinessPolicy {
		return patchReadinessPolicy{
			InitialProbeTimeout: time.Millisecond,
			RetryProbeTimeout:   time.Millisecond,
			RetryInterval:       time.Millisecond,
			TotalBudget:         10 * time.Second, // long budget so we cancel before it expires
			QuietDelay:          time.Hour,
		}
	}

	// Probe blocks until its context is cancelled.
	runClaudeTimedProbeFn = func(ctx context.Context, path string, arg string, timeout time.Duration) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}

	outcome := &patchOutcome{
		Applied:           true,
		IsClaude:          true,
		NeedsVerification: true,
		TargetPath:        "/fake/claude",
		BackupPath:        "/fake/claude.bak",
		LogWriter:         io.Discard,
	}

	ctx, cancel := context.WithCancel(context.Background())
	startPatchedExecutableReadiness(ctx, outcome, exePatchOptions{})

	// Cancel the context; wait should return promptly with context error.
	cancel()

	err := waitPatchedExecutableReady(ctx, outcome)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	// The goroutine must have finished (no data race): verify we can
	// safely read outcome fields after wait returns.
	_ = outcome.Verified
	_ = outcome.NeedsVerification
	_ = outcome.RollbackOnStartupFailure
}
