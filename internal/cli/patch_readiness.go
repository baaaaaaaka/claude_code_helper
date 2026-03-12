package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
	"golang.org/x/term"
)

var (
	runtimeGOOS                   = runtime.GOOS
	runClaudeTimedProbeFn         = runClaudeProbeWithContext
	patchReadinessPolicyFn        = defaultPatchReadinessPolicy
	maybePatchExecutableCtxFn     = maybePatchExecutableWithContext
	waitPatchedExecutableReadyFn  = waitPatchedExecutableReady
	errPatchReadinessTimeout      = errors.New("timed out waiting for patched Claude to become ready")
	errPatchReadinessStillPending = errors.New("Windows is still preparing Claude to start")
)

type patchReadinessPolicy struct {
	InitialProbeTimeout time.Duration
	RetryProbeTimeout   time.Duration
	RetryInterval       time.Duration
	TotalBudget         time.Duration
	QuietDelay          time.Duration
}

type patchReadiness struct {
	startedAt time.Time
	deadline  time.Time
	policy    patchReadinessPolicy
	done      chan struct{}
	cancel    context.CancelFunc
	err       error
}

func defaultPatchReadinessPolicy() patchReadinessPolicy {
	return patchReadinessPolicy{
		InitialProbeTimeout: 60 * time.Second,
		RetryProbeTimeout:   5 * time.Second,
		RetryInterval:       2 * time.Second,
		TotalBudget:         75 * time.Second,
		QuietDelay:          2 * time.Second,
	}
}

func (o *patchOutcome) hasPatchedBinary() bool {
	return o != nil && (o.Applied || o.AlreadyPatched || o.Verified || o.NeedsVerification)
}

func startPatchedExecutableReadiness(ctx context.Context, outcome *patchOutcome, opts exePatchOptions) {
	if outcome == nil || !outcome.IsClaude || outcome.Verified || !outcome.NeedsVerification || runtimeGOOS != "windows" {
		return
	}
	policy := patchReadinessPolicyFn()
	if policy.TotalBudget <= 0 {
		policy = defaultPatchReadinessPolicy()
	}
	startedAt := time.Now()
	runCtx, cancel := context.WithCancel(ctx)
	readiness := &patchReadiness{
		startedAt: startedAt,
		deadline:  startedAt.Add(policy.TotalBudget),
		policy:    policy,
		done:      make(chan struct{}),
		cancel:    cancel,
	}
	outcome.readiness = readiness

	go func() {
		defer close(readiness.done)

		out, err := verifyWindowsPatchReadiness(runCtx, outcome.TargetPath, policy)
		switch {
		case err == nil:
			if markErr := markPatchedExecutableVerified(outcome, time.Now()); markErr != nil {
				_, _ = fmt.Fprintf(patchOutcomeLogWriter(outcome), "exe-patch: failed to persist patch verification: %v\n", markErr)
			}
			outcome.Verified = true
			outcome.NeedsVerification = false
		case errors.Is(err, context.Canceled):
			readiness.err = err
		case errors.Is(err, errPatchReadinessTimeout):
			readiness.err = fmt.Errorf("%w: Windows is still preparing Claude to start; please retry in a few moments", errPatchReadinessStillPending)
		default:
			if failureErr := handlePatchedExecutableFailure(outcome, err, out); failureErr != nil {
				readiness.err = failureErr
				return
			}
			// The patched binary has already been restored to the original
			// executable, so callers should continue with the recovered Claude.
			readiness.err = nil
		}
	}()
}

func verifyWindowsPatchReadiness(ctx context.Context, path string, policy patchReadinessPolicy) (string, error) {
	initialTimeout := policy.InitialProbeTimeout
	if initialTimeout <= 0 {
		initialTimeout = 60 * time.Second
	}
	totalBudget := policy.TotalBudget
	if totalBudget <= 0 {
		totalBudget = initialTimeout
	}
	if initialTimeout > totalBudget {
		initialTimeout = totalBudget
	}
	deadline := time.Now().Add(totalBudget)

	out, err := runClaudeTimedProbeFn(ctx, path, "--version", initialTimeout)
	if err == nil {
		return out, nil
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		return out, err
	}

	retryTimeout := policy.RetryProbeTimeout
	if retryTimeout <= 0 {
		retryTimeout = 5 * time.Second
	}
	retryInterval := policy.RetryInterval
	if retryInterval <= 0 {
		retryInterval = 2 * time.Second
	}

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return out, errPatchReadinessTimeout
		}

		wait := retryInterval
		if wait > remaining {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return out, ctx.Err()
		case <-timer.C:
		}

		remaining = time.Until(deadline)
		if remaining <= 0 {
			return out, errPatchReadinessTimeout
		}
		timeout := retryTimeout
		if timeout > remaining {
			timeout = remaining
		}

		out, err = runClaudeTimedProbeFn(ctx, path, "--version", timeout)
		if err == nil {
			return out, nil
		}
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			return out, err
		}
	}
}

func waitPatchedExecutableReady(ctx context.Context, outcome *patchOutcome) error {
	if outcome == nil || outcome.readiness == nil {
		return nil
	}
	return outcome.readiness.wait(ctx, patchOutcomeLogWriter(outcome))
}

func (r *patchReadiness) wait(ctx context.Context, w io.Writer) error {
	if r == nil {
		return nil
	}
	if w == nil {
		w = io.Discard
	}
	quietDelay := r.policy.QuietDelay
	quietElapsed := quietDelay <= 0

	var quiet <-chan time.Time
	if !quietElapsed {
		timer := time.NewTimer(quietDelay)
		defer timer.Stop()
		quiet = timer.C
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	printed := false
	isTTY := writerIsTerminal(w)
	lastNonTTYUpdate := time.Time{}

	printProgress := func(force bool) {
		if !force && !quietElapsed {
			return
		}
		msg := formatPatchReadinessMessage(r.deadline)
		if isTTY {
			_, _ = fmt.Fprintf(w, "\r%s", msg)
			printed = true
			return
		}

		now := time.Now()
		remaining := patchReadinessRemainingSeconds(r.deadline)
		interval := 5 * time.Second
		if remaining <= 10 {
			interval = time.Second
		}
		if printed && now.Sub(lastNonTTYUpdate) < interval {
			return
		}
		_, _ = fmt.Fprintln(w, msg)
		printed = true
		lastNonTTYUpdate = now
	}

	for {
		select {
		case <-r.done:
			if printed {
				_, _ = fmt.Fprintln(w)
			}
			return r.err
		case <-ctx.Done():
			if r.cancel != nil {
				r.cancel()
			}
			// Wait for the goroutine to finish so that all writes to
			// outcome fields complete before we return.  This avoids a
			// data race between the goroutine and the caller.
			<-r.done
			if printed {
				_, _ = fmt.Fprintln(w)
			}
			return ctx.Err()
		case <-quiet:
			quietElapsed = true
			printProgress(true)
			quiet = nil
		case <-ticker.C:
			printProgress(false)
		}
	}
}

func formatPatchReadinessMessage(deadline time.Time) string {
	return fmt.Sprintf(
		"Starting Claude. Windows is finishing a security check on the updated binary. About %d seconds remaining...",
		patchReadinessRemainingSeconds(deadline),
	)
}

func patchReadinessRemainingSeconds(deadline time.Time) int {
	remaining := int(math.Ceil(time.Until(deadline).Seconds()))
	if remaining < 0 {
		return 0
	}
	return remaining
}

func writerIsTerminal(w io.Writer) bool {
	type fdWriter interface {
		Fd() uintptr
	}
	f, ok := w.(fdWriter)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

func patchOutcomeLogWriter(outcome *patchOutcome) io.Writer {
	if outcome != nil && outcome.LogWriter != nil {
		return outcome.LogWriter
	}
	return os.Stderr
}

func markPatchedExecutableVerified(outcome *patchOutcome, verifiedAt time.Time) error {
	if outcome == nil {
		return nil
	}
	outcome.Verified = true
	outcome.NeedsVerification = false
	if outcome.HistoryStore == nil || strings.TrimSpace(outcome.SpecsHash) == "" {
		return nil
	}
	return outcome.HistoryStore.Update(func(history *config.PatchHistory) error {
		history.MarkVerified(outcome.TargetPath, outcome.SpecsHash, verifiedAt)
		return nil
	})
}

func handlePatchedExecutableFailure(outcome *patchOutcome, err error, output string) error {
	log := patchOutcomeLogWriter(outcome)
	_, _ = fmt.Fprintln(log, "exe-patch: detected startup failure; restoring backup")
	if restoreErr := restoreExecutableFromBackupFn(outcome); restoreErr != nil {
		return fmt.Errorf("restore patched executable: %w", restoreErr)
	}
	if historyErr := cleanupPatchHistoryFn(outcome); historyErr != nil {
		return fmt.Errorf("cleanup patch history: %w", historyErr)
	}
	if recordErr := recordPatchFailureFn(outcome.ConfigPath, outcome, formatFailureReason(err, output)); recordErr != nil {
		_, _ = fmt.Fprintf(log, "exe-patch: failed to record patch failure: %v\n", recordErr)
	}
	outcome.Verified = false
	outcome.NeedsVerification = false
	outcome.RollbackOnStartupFailure = false
	return nil
}
