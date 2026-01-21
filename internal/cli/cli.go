package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = ""
	date    = ""
)

type rootOptions struct {
	configPath string
}

func Execute() int {
	cmd := newRootCmd()
	if err := cmd.Execute(); err != nil {
		return 1
	}
	return 0
}

func newRootCmd() *cobra.Command {
	opts := &rootOptions{}

	cmd := &cobra.Command{
		Use:           "claude-proxy",
		Short:         "Run a command through an SSH-backed local proxy",
		SilenceErrors: false,
		SilenceUsage:  true,
		Version:       buildVersion(),
	}

	cmd.PersistentFlags().StringVar(&opts.configPath, "config", "", "Override config file path (default: OS user config dir)")

	cmd.AddCommand(
		newInitCmd(opts),
		newRunCmd(opts),
		newProxyCmd(opts),
	)

	return cmd
}

func buildVersion() string {
	v := version
	if commit != "" {
		v += " (" + commit + ")"
	}
	if date != "" {
		v += " " + date
	}
	return v
}

func newNotImplementedCmd(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _ = fmt.Fprintln(os.Stderr, "not implemented yet")
			return fmt.Errorf("not implemented")
		},
	}
}
