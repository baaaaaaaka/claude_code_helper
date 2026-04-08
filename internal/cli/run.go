package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
	"github.com/baaaaaaaka/claude_code_helper/internal/env"
	"github.com/baaaaaaaka/claude_code_helper/internal/ids"
	"github.com/baaaaaaaka/claude_code_helper/internal/manager"
	"github.com/baaaaaaaka/claude_code_helper/internal/proc"
	"github.com/baaaaaaaka/claude_code_helper/internal/stack"
)

var (
	stackStart               = stack.Start
	releasePatchPrepMemoryFn = releasePatchPrepMemory
)

func newRunCmd(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [profile] -- [cmd args...]",
		Short: "Run a command using direct mode or an SSH-backed local proxy",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Keep `run` working, but also auto-init when no profiles exist.
			return runLike(cmd, root, true)
		},
	}
	return cmd
}

func runLike(cmd *cobra.Command, root *rootOptions, autoInit bool) error {
	all := cmd.Flags().Args()
	dash := cmd.Flags().ArgsLenAtDash()

	before := all
	after := []string{}
	if dash >= 0 {
		before = all[:dash]
		after = all[dash:]
	}

	var profileRef string
	if len(before) > 0 {
		profileRef = before[0]
	}
	if len(before) > 1 {
		return fmt.Errorf("unexpected args before -- (only profile is allowed)")
	}
	if len(after) == 0 {
		after = []string{"claude"}
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	patchOutcome, err := maybePatchExecutableCtxFn(ctx, after, root.exePatch, root.configPath, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	releasePatchPrepMemoryFn(after, root.exePatch, patchOutcome)
	if root.exePatch.dryRun && root.exePatch.enabled() {
		return nil
	}

	store, err := config.NewStore(root.configPath)
	if err != nil {
		return err
	}

	// An explicit positional profile keeps the historical "force proxy" behavior.
	// Without a profile, `run` follows the saved direct/proxy preference just
	// like the TUI and history commands.
	if profileRef != "" {
		profile, cfg, err := ensureProfile(ctx, store, profileRef, autoInit, cmd.OutOrStdout())
		if err != nil {
			return err
		}
		return runWithProfile(ctx, store, profile, cfg.Instances, after, patchOutcome)
	}

	pref, err := ensureProxyPreference(ctx, store, "", cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	useProxy, cfg := pref.Enabled, pref.Cfg

	if useProxy {
		profile, cfgWithProfile, err := ensureProfile(ctx, store, "", autoInit, cmd.OutOrStdout())
		if err != nil {
			return err
		}
		cfg = cfgWithProfile
		if pref.NeedsPersist {
			if err := persistProxyPreference(store, true); err != nil {
				return err
			}
		}
		return runWithProfile(ctx, store, profile, cfg.Instances, after, patchOutcome)
	}

	if pref.NeedsPersist {
		if err := persistProxyPreference(store, false); err != nil {
			return err
		}
	}

	opts := defaultRunTargetOptions()
	opts.UseProxy = false
	return runTargetWithFallbackWithOptions(ctx, after, "", nil, patchOutcome, nil, opts)
}

func releasePatchPrepMemory(cmdArgs []string, opts exePatchOptions, outcome *patchOutcome) {
	if runtimeGOOS == "windows" || !opts.enabled() {
		return
	}

	targetPath := ""
	if outcome != nil {
		targetPath = firstNonEmpty(outcome.SourcePath, outcome.TargetPath)
	}
	if targetPath == "" && len(cmdArgs) > 0 {
		exePath, err := execLookPathFn(cmdArgs[0])
		if err != nil {
			return
		}
		resolvedPath, err := resolveExecutablePathFn(exePath)
		if err != nil {
			return
		}
		targetPath = resolvedPath
	}
	if targetPath == "" {
		return
	}

	info, err := os.Stat(targetPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 64<<20 {
		return
	}

	runtime.GC()
	debug.FreeOSMemory()
}

func selectProfile(cfg config.Config, ref string) (config.Profile, error) {
	if ref != "" {
		if p, ok := cfg.FindProfile(ref); ok {
			return p, nil
		}
		return config.Profile{}, fmt.Errorf("profile %q not found", ref)
	}
	if len(cfg.Profiles) == 0 {
		return config.Profile{}, fmt.Errorf("no profiles found; run `claude-proxy init` (or run `claude-proxy` to create one)")
	}
	if len(cfg.Profiles) == 1 {
		return cfg.Profiles[0], nil
	}
	return config.Profile{}, fmt.Errorf("multiple profiles exist; specify one: `claude-proxy <profile>` or `claude-proxy run <profile> -- ...`")
}

func runWithExistingInstance(ctx context.Context, hc manager.HealthClient, inst config.Instance, cmdArgs []string, patchOutcome *patchOutcome) error {
	return runWithExistingInstanceOptions(ctx, hc, inst, cmdArgs, patchOutcome, defaultRunTargetOptions())
}

func runWithExistingInstanceOptions(
	ctx context.Context,
	hc manager.HealthClient,
	inst config.Instance,
	cmdArgs []string,
	patchOutcome *patchOutcome,
	opts runTargetOptions,
) error {
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", inst.HTTPPort)
	return runTargetSupervisedWithOptions(ctx, cmdArgs, proxyURL, func() error {
		return hc.CheckHTTPProxy(inst.HTTPPort, inst.ID)
	}, patchOutcome, nil, opts)
}

func runWithNewStack(ctx context.Context, store *config.Store, profile config.Profile, cmdArgs []string, patchOutcome *patchOutcome) error {
	return runWithNewStackOptions(ctx, store, profile, cmdArgs, patchOutcome, defaultRunTargetOptions())
}

func runWithNewStackOptions(
	ctx context.Context,
	store *config.Store,
	profile config.Profile,
	cmdArgs []string,
	patchOutcome *patchOutcome,
	opts runTargetOptions,
) error {
	instanceID, err := ids.New()
	if err != nil {
		return err
	}

	st, err := stackStart(profile, instanceID, stack.Options{})
	if err != nil {
		return err
	}
	defer func() { _ = st.Close(context.Background()) }()

	proxyURL := st.HTTPProxyURL()

	if len(cmdArgs) == 0 {
		return fmt.Errorf("missing command")
	}

	hc := manager.HealthClient{Timeout: 1 * time.Second}
	return runTargetSupervisedWithOptions(ctx, cmdArgs, proxyURL, func() error {
		return hc.CheckHTTPProxy(st.HTTPPort, instanceID)
	}, patchOutcome, st.Fatal(), opts)
}

func runWithProfile(
	ctx context.Context,
	store *config.Store,
	profile config.Profile,
	instances []config.Instance,
	cmdArgs []string,
	patchOutcome *patchOutcome,
) error {
	return runWithProfileOptions(ctx, store, profile, instances, cmdArgs, patchOutcome, defaultRunTargetOptions())
}

func runWithProfileOptions(
	ctx context.Context,
	store *config.Store,
	profile config.Profile,
	instances []config.Instance,
	cmdArgs []string,
	patchOutcome *patchOutcome,
	opts runTargetOptions,
) error {
	hc := manager.HealthClient{Timeout: 1 * time.Second}
	if inst := manager.FindReusableInstance(instances, profile.ID, hc); inst != nil {
		return runWithExistingInstanceOptions(ctx, hc, *inst, cmdArgs, patchOutcome, opts)
	}
	return runWithNewStackOptions(ctx, store, profile, cmdArgs, patchOutcome, opts)
}

type runTargetOptions struct {
	Cwd      string
	ExtraEnv []string
	UseProxy bool
	// PreserveTTY keeps stdout/stderr attached to the terminal for interactive CLIs.
	PreserveTTY        bool
	CaptureTTYOutput   bool
	YoloEnabled        bool
	OnYoloFallback     func() error
	OnYoloRetryPrepare func([]string) (*patchOutcome, error)
}

func defaultRunTargetOptions() runTargetOptions {
	return runTargetOptions{UseProxy: true}
}

func runTargetSupervised(
	ctx context.Context,
	cmdArgs []string,
	proxyURL string,
	healthCheck func() error,
	patchOutcome *patchOutcome,
	fatalCh <-chan error,
) error {
	return runTargetSupervisedWithOptions(ctx, cmdArgs, proxyURL, healthCheck, patchOutcome, fatalCh, defaultRunTargetOptions())
}

func runTargetSupervisedWithOptions(
	ctx context.Context,
	cmdArgs []string,
	proxyURL string,
	healthCheck func() error,
	patchOutcome *patchOutcome,
	fatalCh <-chan error,
	opts runTargetOptions,
) error {
	if len(cmdArgs) == 0 {
		return fmt.Errorf("missing command")
	}
	return runTargetWithFallbackWithOptions(ctx, cmdArgs, proxyURL, healthCheck, patchOutcome, fatalCh, opts)
}

func terminateProcess(p *os.Process, grace time.Duration) error {
	if p == nil {
		return nil
	}

	_ = p.Signal(os.Interrupt)

	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !proc.IsAlive(p.Pid) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	return p.Kill()
}

const maxOutputCaptureBytes = 64 * 1024

type limitedBuffer struct {
	buf []byte
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.max <= 0 || len(p) == 0 {
		return len(p), nil
	}
	if len(p) >= b.max {
		b.buf = append(b.buf[:0], p[len(p)-b.max:]...)
		return len(p), nil
	}
	if len(b.buf)+len(p) > b.max {
		overflow := len(b.buf) + len(p) - b.max
		b.buf = append(b.buf[overflow:], p...)
		return len(p), nil
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	return string(b.buf)
}

type synchronizedLimitedBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (b *synchronizedLimitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.max <= 0 || len(p) == 0 {
		return len(p), nil
	}
	if len(p) >= b.max {
		b.buf = append(b.buf[:0], p[len(p)-b.max:]...)
		return len(p), nil
	}
	if len(b.buf)+len(p) > b.max {
		overflow := len(b.buf) + len(p) - b.max
		b.buf = append(b.buf[overflow:], p...)
		return len(p), nil
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *synchronizedLimitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

func runTargetWithFallback(
	ctx context.Context,
	cmdArgs []string,
	proxyURL string,
	healthCheck func() error,
	patchOutcome *patchOutcome,
	fatalCh <-chan error,
) error {
	return runTargetWithFallbackWithOptions(ctx, cmdArgs, proxyURL, healthCheck, patchOutcome, fatalCh, defaultRunTargetOptions())
}

func runTargetWithFallbackWithOptions(
	ctx context.Context,
	cmdArgs []string,
	proxyURL string,
	healthCheck func() error,
	patchOutcome *patchOutcome,
	fatalCh <-chan error,
	opts runTargetOptions,
) error {
	if err := waitPatchedExecutableReadyFn(ctx, patchOutcome); err != nil {
		return err
	}
	attempt := 0
	yoloRetried := false
	patchChecked := false
	for {
		attempt++
		stdoutBuf := &synchronizedLimitedBuffer{max: maxOutputCaptureBytes}
		stderrBuf := &synchronizedLimitedBuffer{max: maxOutputCaptureBytes}
		launchArgs := commandArgsForOutcome(patchOutcome, cmdArgs)
		attemptOpts := opts
		if attemptOpts.PreserveTTY && (attemptOpts.YoloEnabled || (patchOutcome != nil && patchOutcome.RollbackOnStartupFailure)) {
			attemptOpts.CaptureTTYOutput = true
		}
		err := runTargetOnceWithOptions(ctx, launchArgs, proxyURL, healthCheck, fatalCh, stdoutBuf, stderrBuf, attemptOpts)
		if err == nil {
			return nil
		}
		out := stdoutBuf.String() + stderrBuf.String()
		if opts.YoloEnabled && !yoloRetried && isYoloFailure(err, out) {
			yoloRetried = true
			nextArgs := prepareYoloRetryArgs(cmdArgs, isYoloRuntimeFailure(err))
			if isYoloRuntimeFailure(err) {
				_, _ = fmt.Fprintln(os.Stderr, "yolo: bypass permission mode failed at runtime; retrying without bypass")
			}
			if opts.OnYoloFallback != nil {
				_ = opts.OnYoloFallback()
			}
			if opts.OnYoloRetryPrepare != nil {
				patchOutcome, err = opts.OnYoloRetryPrepare(nextArgs)
				if err != nil {
					return err
				}
				if err := waitPatchedExecutableReadyFn(ctx, patchOutcome); err != nil {
					return err
				}
				patchChecked = false
			}
			cmdArgs = nextArgs
			opts.YoloEnabled = false
			continue
		}
		if patchOutcome != nil && patchOutcome.RollbackOnStartupFailure && !patchChecked {
			patchChecked = true
			if isPatchedBinaryStartupFailure(err, out) {
				_, _ = fmt.Fprintln(os.Stderr, "exe-patch: detected startup failure; restoring backup")
				if restoreErr := restoreExecutableFromBackup(patchOutcome); restoreErr != nil {
					return fmt.Errorf("restore patched executable: %w", restoreErr)
				}
				if historyErr := cleanupPatchHistory(patchOutcome); historyErr != nil {
					return fmt.Errorf("cleanup patch history: %w", historyErr)
				}
				if recordErr := recordPatchFailure(patchOutcome.ConfigPath, patchOutcome, formatFailureReason(err, out)); recordErr != nil {
					_, _ = fmt.Fprintf(os.Stderr, "exe-patch: failed to record patch failure: %v\n", recordErr)
				}
				patchOutcome = nil
				continue
			}
		}
		return err
	}
}

func runTargetOnce(
	ctx context.Context,
	cmdArgs []string,
	proxyURL string,
	healthCheck func() error,
	fatalCh <-chan error,
	stdoutBuf io.Writer,
	stderrBuf io.Writer,
) error {
	return runTargetOnceWithOptions(ctx, cmdArgs, proxyURL, healthCheck, fatalCh, stdoutBuf, stderrBuf, defaultRunTargetOptions())
}

func runTargetOnceWithOptions(
	ctx context.Context,
	cmdArgs []string,
	proxyURL string,
	healthCheck func() error,
	fatalCh <-chan error,
	stdoutBuf io.Writer,
	stderrBuf io.Writer,
	opts runTargetOptions,
) error {
	envVars := os.Environ()
	if opts.UseProxy {
		envVars = env.WithProxy(envVars, proxyURL)
	}
	if len(opts.ExtraEnv) > 0 {
		envVars = append(envVars, opts.ExtraEnv...)
	}

	if opts.PreserveTTY && opts.CaptureTTYOutput {
		err := runTargetOnceWithCapturedTTYOutput(ctx, cmdArgs, envVars, healthCheck, fatalCh, stdoutBuf, stderrBuf, opts)
		if err == nil || !errors.Is(err, errTTYCaptureUnavailable) {
			return err
		}
	}

	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	cmd.Env = envVars
	cmd.Stdin = os.Stdin
	if opts.PreserveTTY {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		if stdoutBuf != nil {
			cmd.Stdout = io.MultiWriter(os.Stdout, stdoutBuf)
		} else {
			cmd.Stdout = os.Stdout
		}
		if stderrBuf != nil {
			cmd.Stderr = io.MultiWriter(os.Stderr, stderrBuf)
		} else {
			cmd.Stderr = os.Stderr
		}
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	failures := 0
	for {
		select {
		case err := <-done:
			return err
		case err := <-fatalCh:
			_ = terminateProcess(cmd.Process, 2*time.Second)
			<-done
			return fmt.Errorf("proxy stack failed; terminated target: %w", err)
		case <-ctx.Done():
			_ = terminateProcess(cmd.Process, 2*time.Second)
			<-done
			return ctx.Err()
		case <-ticker.C:
			if healthCheck == nil {
				continue
			}
			if err := healthCheck(); err != nil {
				failures++
				if failures >= 3 {
					_ = terminateProcess(cmd.Process, 2*time.Second)
					<-done
					return fmt.Errorf("proxy unhealthy; terminated target: %w", err)
				}
				continue
			}
			failures = 0
		}
	}
}

func prepareYoloRetryArgs(cmdArgs []string, runtimeFailure bool) []string {
	nextArgs := stripYoloArgs(cmdArgs)
	if !runtimeFailure {
		return nextArgs
	}
	if len(nextArgs) != 1 {
		return nextArgs
	}
	if !isClaudeExecutable(nextArgs[0], nextArgs[0]) {
		return nextArgs
	}
	return append(nextArgs, "--continue")
}

func isPatchedBinaryFailure(err error, output string) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(output)
	if strings.Contains(lower, "@bun @bytecode") || strings.Contains(lower, "@bun/@bytecode") {
		return true
	}
	if strings.Contains(lower, "module not found") && strings.Contains(lower, "bun") {
		return true
	}
	return false
}
