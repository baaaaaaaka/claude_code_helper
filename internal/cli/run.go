package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
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

			store, err := config.NewStore(root.configPath)
			if err != nil {
				return err
			}
			cfg, err := store.Load()
			if err != nil {
				return err
			}

			profile, err := selectProfile(cfg, profileRef)
			if err != nil {
				return err
			}

			hc := manager.HealthClient{Timeout: 1 * time.Second}
			if inst := manager.FindReusableInstance(cfg.Instances, profile.ID, hc); inst != nil {
				return runWithExistingInstance(ctx, hc, *inst, after)
			}

			return runWithNewStack(ctx, store, profile, after)
		},
	}
	return cmd
}

func selectProfile(cfg config.Config, ref string) (config.Profile, error) {
	if ref != "" {
		if p, ok := cfg.FindProfile(ref); ok {
			return p, nil
		}
		return config.Profile{}, fmt.Errorf("profile %q not found", ref)
	}
	if len(cfg.Profiles) == 0 {
		return config.Profile{}, fmt.Errorf("no profiles found; run `claude-proxy init` first")
	}
	if len(cfg.Profiles) == 1 {
		return cfg.Profiles[0], nil
	}
	return config.Profile{}, fmt.Errorf("multiple profiles exist; specify one: `claude-proxy run <profile> -- ...`")
}

func runWithExistingInstance(ctx context.Context, hc manager.HealthClient, inst config.Instance, cmdArgs []string) error {
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", inst.HTTPPort)
	return runTargetSupervised(ctx, cmdArgs, proxyURL, func() error {
		return hc.CheckHTTPProxy(inst.HTTPPort, inst.ID)
	})
}

func runWithNewStack(ctx context.Context, store *config.Store, profile config.Profile, cmdArgs []string) error {
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

	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Env = env.WithProxy(os.Environ(), proxyURL)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	hc := manager.HealthClient{Timeout: 1 * time.Second}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	failures := 0
	for {
		select {
		case err := <-done:
			return err
		case err := <-st.Fatal():
			_ = terminateProcess(cmd.Process, 2*time.Second)
			<-done
			return fmt.Errorf("proxy stack failed; terminated target: %w", err)
		case <-ctx.Done():
			_ = terminateProcess(cmd.Process, 2*time.Second)
			<-done
			return ctx.Err()
		case <-ticker.C:
			if err := hc.CheckHTTPProxy(st.HTTPPort, instanceID); err != nil {
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

func runTargetSupervised(ctx context.Context, cmdArgs []string, proxyURL string, healthCheck func() error) error {
	if len(cmdArgs) == 0 {
		return fmt.Errorf("missing command")
	}

	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Env = env.WithProxy(os.Environ(), proxyURL)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

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
