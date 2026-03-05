package cli

import (
	"fmt"
	"os"
	"os/exec"

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

	// Remove stale exe-patch backup and history so the patch system treats
	// the freshly-installed binary as new rather than re-patching the old
	// backup over the top.
	invalidateExePatchState("claude", root.configPath)

	_, _ = fmt.Fprintln(out, "Claude Code upgrade complete.")
	return nil
}

// invalidateExePatchState removes the exe-patch backup file and patch history
// entry for the given command so the next run re-patches from the new binary.
func invalidateExePatchState(cmdName string, configPath string) {
	exePath, err := exec.LookPath(cmdName)
	if err != nil {
		return
	}
	resolved, err := resolveExecutablePath(exePath)
	if err != nil {
		return
	}

	backup := originalBackupPath(resolved)
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		return
	}

	historyStore, err := config.NewPatchHistoryStore(configPath)
	if err != nil {
		return
	}
	_ = historyStore.Update(func(h *config.PatchHistory) error {
		for i := 0; i < len(h.Entries); i++ {
			if h.Entries[i].Path == resolved {
				h.Entries = append(h.Entries[:i], h.Entries[i+1:]...)
				i--
			}
		}
		return nil
	})
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
