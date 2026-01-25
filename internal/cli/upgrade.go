package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/update"
)

func newUpgradeCmd(_ *rootOptions) *cobra.Command {
	var repo string
	var versionOverride string
	var installPath string

	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade claude-proxy from GitHub releases",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			requested := update.ResolveVersion(versionOverride)
			if strings.EqualFold(requested, "latest") {
				status := update.CheckForUpdate(ctx, update.CheckOptions{
					Repo:             repo,
					InstalledVersion: version,
					Timeout:          8 * time.Second,
				})
				if status.Supported && !status.UpdateAvailable {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Already up to date.")
					return nil
				}
			}

			res, err := update.PerformUpdate(ctx, update.UpdateOptions{
				Repo:        repo,
				Version:     versionOverride,
				InstallPath: installPath,
				Timeout:     120 * time.Second,
			})
			if err != nil {
				return err
			}

			if res.RestartRequired {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Update scheduled for v%s. Please restart `claude-proxy`.\n", res.Version)
				return nil
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Updated to v%s.\n", res.Version)
			return nil
		},
	}

	cmd.Flags().StringVar(&repo, "repo", "", "Override GitHub repo (owner/name)")
	cmd.Flags().StringVar(&versionOverride, "version", "", "Install a specific version (default: latest)")
	cmd.Flags().StringVar(&installPath, "install-path", "", "Override install path (file or directory)")

	return cmd
}
