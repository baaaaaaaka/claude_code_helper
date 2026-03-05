package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

func newUpgradeClaudeCmd(root *rootOptions) *cobra.Command {
	var profile string

	cmd := &cobra.Command{
		Use:   "upgrade-claude",
		Short: "Upgrade (or install) Claude Code using the official installer",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpgradeClaude(cmd, root, profile)
		},
	}

	cmd.Flags().StringVar(&profile, "profile", "", "SSH profile to use for proxy")

	return cmd
}

func runUpgradeClaude(cmd *cobra.Command, root *rootOptions, profileRef string) error {
	store, err := config.NewStore(root.configPath)
	if err != nil {
		return err
	}

	if _, err := os.Stat(store.Path()); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("claude-proxy has not been initialized; run 'claude-proxy init' first")
		}
		return fmt.Errorf("cannot access config file: %w", err)
	}

	cfg, err := store.Load()
	if err != nil {
		return err
	}

	opts, err := upgradeClaudeInstallOpts(cfg, profileRef)
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	if err := runClaudeInstaller(ctx, out, opts); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(out, "Claude Code upgrade complete.")
	return nil
}

func upgradeClaudeInstallOpts(cfg config.Config, profileRef string) (installProxyOptions, error) {
	if cfg.ProxyEnabled != nil && !*cfg.ProxyEnabled {
		return installProxyOptions{UseProxy: false}, nil
	}

	useProxy := false
	if cfg.ProxyEnabled != nil && *cfg.ProxyEnabled {
		useProxy = true
	} else if cfg.ProxyEnabled == nil && len(cfg.Profiles) > 0 {
		useProxy = true
	}

	if !useProxy {
		return installProxyOptions{UseProxy: false}, nil
	}

	profile, err := selectProfile(cfg, profileRef)
	if err != nil {
		return installProxyOptions{}, err
	}

	return installProxyOptions{
		UseProxy:  true,
		Profile:   &profile,
		Instances: cfg.Instances,
	}, nil
}
