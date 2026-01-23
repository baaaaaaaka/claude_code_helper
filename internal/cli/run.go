package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/config"
	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/env"
	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/ids"
	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/manager"
	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/proc"
	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/stack"
)

func newRunCmd(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [profile] -- [cmd args...]",
		Short: "Run a command through an SSH-backed local proxy",
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

	patchOutcome, err := maybePatchExecutable(after, root.exePatch, root.configPath, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	if root.exePatch.dryRun {
		return nil
	}

	store, err := config.NewStore(root.configPath)
	if err != nil {
		return err
	}

	cfg, err := store.Load()
	if err != nil {
		return err
	}

	var created *config.Profile
	if len(cfg.Profiles) == 0 && autoInit {
		p, err := initProfileInteractive(ctx, store)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Saved profile %q (%s)\n", p.Name, p.ID)
		created = &p

		// Reload to pick up any store defaults/migrations.
		cfg, err = store.Load()
		if err != nil {
			return err
		}
	}

	var profile config.Profile
	if created != nil && profileRef == "" {
		profile = *created
	} else {
		p, err := selectProfile(cfg, profileRef)
		if err != nil {
			if created != nil {
				// If the user passed a "profile" arg before init existed, don't fail; run using the newly created profile.
				profile = *created
			} else {
				return err
			}
		} else {
			profile = p
		}
	}

	hc := manager.HealthClient{Timeout: 1 * time.Second}
	if inst := manager.FindReusableInstance(cfg.Instances, profile.ID, hc); inst != nil {
		return runWithExistingInstance(ctx, hc, *inst, after, patchOutcome)
	}

	return runWithNewStack(ctx, store, profile, after, patchOutcome)
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
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", inst.HTTPPort)
	return runTargetSupervised(ctx, cmdArgs, proxyURL, func() error {
		return hc.CheckHTTPProxy(inst.HTTPPort, inst.ID)
	}, patchOutcome, nil)
}

func runWithNewStack(ctx context.Context, store *config.Store, profile config.Profile, cmdArgs []string, patchOutcome *patchOutcome) error {
	instanceID, err := ids.New()
	if err != nil {
		return err
	}

	st, err := stack.Start(profile, instanceID, stack.Options{})
	if err != nil {
		return err
	}
	defer func() { _ = st.Close(context.Background()) }()

	now := time.Now()
	inst := config.Instance{
		ID:         instanceID,
		ProfileID:  profile.ID,
		HTTPPort:   st.HTTPPort,
		SocksPort:  st.SocksPort,
		DaemonPID:  os.Getpid(),
		StartedAt:  now,
		LastSeenAt: now,
	}
	if err := manager.RecordInstance(store, inst); err != nil {
		return err
	}
	defer func() { _ = manager.RemoveInstance(store, instanceID) }()

	hbStop := make(chan struct{})
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-hbStop:
				return
			case <-t.C:
				_ = manager.Heartbeat(store, instanceID, time.Now())
			}
		}
	}()
	defer close(hbStop)

	proxyURL := st.HTTPProxyURL()

	if len(cmdArgs) == 0 {
		return fmt.Errorf("missing command")
	}

	hc := manager.HealthClient{Timeout: 1 * time.Second}
	return runTargetSupervised(ctx, cmdArgs, proxyURL, func() error {
		return hc.CheckHTTPProxy(st.HTTPPort, instanceID)
	}, patchOutcome, st.Fatal())
}

func runTargetSupervised(
	ctx context.Context,
	cmdArgs []string,
	proxyURL string,
	healthCheck func() error,
	patchOutcome *patchOutcome,
	fatalCh <-chan error,
) error {
	if len(cmdArgs) == 0 {
		return fmt.Errorf("missing command")
	}
	return runTargetWithFallback(ctx, cmdArgs, proxyURL, healthCheck, patchOutcome, fatalCh)
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

func runTargetWithFallback(
	ctx context.Context,
	cmdArgs []string,
	proxyURL string,
	healthCheck func() error,
	patchOutcome *patchOutcome,
	fatalCh <-chan error,
) error {
	attempt := 0
	for {
		attempt++
		stdoutBuf := &limitedBuffer{max: maxOutputCaptureBytes}
		stderrBuf := &limitedBuffer{max: maxOutputCaptureBytes}
		err := runTargetOnce(ctx, cmdArgs, proxyURL, healthCheck, fatalCh, stdoutBuf, stderrBuf)
		if err == nil {
			if patchOutcome != nil && patchOutcome.Applied {
				cleanupBackup(patchOutcome)
			}
			return nil
		}
		if patchOutcome != nil && patchOutcome.Applied && attempt == 1 {
			out := stdoutBuf.String() + stderrBuf.String()
			if isPatchedBinaryFailure(err, out) {
				_, _ = fmt.Fprintln(os.Stderr, "exe-patch: detected startup failure; restoring backup")
				if restoreErr := restoreExecutableFromBackup(patchOutcome); restoreErr != nil {
					return fmt.Errorf("restore patched executable: %w", restoreErr)
				}
				if historyErr := cleanupPatchHistory(patchOutcome); historyErr != nil {
					return fmt.Errorf("cleanup patch history: %w", historyErr)
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
	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Env = env.WithProxy(os.Environ(), proxyURL)
	cmd.Stdin = os.Stdin
	cmd.Stdout = io.MultiWriter(os.Stdout, stdoutBuf)
	cmd.Stderr = io.MultiWriter(os.Stderr, stderrBuf)

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
