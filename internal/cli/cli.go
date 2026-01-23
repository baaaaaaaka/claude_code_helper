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
	exePatch   exePatchOptions
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
		Use:           "claude-proxy [profile] -- [cmd args...]",
		Short:         "Run a command through an SSH-backed local proxy",
		SilenceErrors: false,
		SilenceUsage:  true,
		Version:       buildVersion(),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Default behavior: equivalent to `claude-proxy run`.
			// If no profiles exist, start the init flow, then run the default command (`claude`).
			_ = args
			return runLike(cmd, opts, true)
		},
	}

	cmd.PersistentFlags().StringVar(&opts.configPath, "config", "", "Override config file path (default: OS user config dir)")
	cmd.PersistentFlags().StringVar(&opts.exePatch.regex1, "exe-patch-regex-1", "", "Stage 1 regex to locate candidate code blocks in the target executable")
	cmd.PersistentFlags().StringArrayVar(&opts.exePatch.regex2, "exe-patch-regex-2", nil, "Stage 2 regex to confirm a stage 1 block should be patched (repeatable)")
	cmd.PersistentFlags().StringArrayVar(&opts.exePatch.regex3, "exe-patch-regex-3", nil, "Stage 3 regex to apply inside the stage 1 block (repeatable)")
	cmd.PersistentFlags().StringArrayVar(&opts.exePatch.replace, "exe-patch-replace", nil, "Replacement for stage 3 regex (repeatable, supports $1-style expansion)")
	cmd.PersistentFlags().BoolVar(&opts.exePatch.preview, "exe-patch-preview", false, "Print before/after matches when patching")
	cmd.PersistentFlags().BoolVar(&opts.exePatch.policySettings, "exe-patch-policy-settings", true, "Apply built-in policySettings patches")
	cmd.PersistentFlags().BoolVar(&opts.exePatch.dryRun, "exe-patch-dry-run", false, "Run exe patch in memory without writing or launching the command")

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
