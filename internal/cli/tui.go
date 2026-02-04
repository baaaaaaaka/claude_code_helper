package cli

import (
	"time"

	"github.com/spf13/cobra"
)

func newTuiCmd(root *rootOptions) *cobra.Command {
	var claudeDir string
	var claudePath string
	var profileRef string
	var refreshInterval time.Duration

	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Browse Claude Code history in a terminal UI",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runHistoryTui(cmd, root, profileRef, claudeDir, claudePath, refreshInterval)
		},
	}

	cmd.Flags().StringVar(&claudeDir, "claude-dir", "", "Override Claude Code data dir (default: ~/.claude)")
	cmd.Flags().StringVar(&claudePath, "claude-path", "", "Override Claude CLI path (default: search PATH)")
	cmd.Flags().StringVar(&profileRef, "profile", "", "Proxy profile id or name")
	cmd.Flags().DurationVar(&refreshInterval, "refresh-interval", defaultRefreshInterval, "Auto-refresh interval (0 to disable)")
	return cmd
}
