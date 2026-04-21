package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

var (
	version = "0.0.65"
	commit  = ""
	date    = ""
)

type rootOptions struct {
	configPath string
	exePatch   exePatchOptions
}

func Execute() int {
	cmd := newRootCmd()
	return mapExecuteError(cmd.Execute(), os.Stderr)
}

// mapExecuteError converts a cobra Execute() error into a process exit code.
//
// Unwrapped *exec.ExitError means the child process exited with a non-zero
// code but clp itself didn't fail — pass the code through silently, don't
// print our own error line. This uses a type assertion (not errors.As) on
// purpose: clp's own failures that wrap an *exec.ExitError with %w must
// still print an "Error: ..." line and exit 1.
func mapExecuteError(err error, stderr io.Writer) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if code := exitErr.ExitCode(); code >= 0 {
			return code
		}
	}
	fmt.Fprintln(stderr, "Error:", err.Error())
	return 1
}

func newRootCmd() *cobra.Command {
	opts := &rootOptions{}

	cmd := &cobra.Command{
		Use:           "claude-proxy [profile]",
		Short:         "Browse Claude Code history in a terminal UI",
		SilenceErrors: true,
		SilenceUsage:  true,
		Version:       buildVersion(),
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			return runDefaultTui(cmd, opts)
		},
	}

	cmd.PersistentFlags().StringVar(&opts.configPath, "config", "", "Override config file path (default: OS user config dir)")
	cmd.PersistentFlags().BoolVar(&opts.exePatch.enabledFlag, "exe-patch-enabled", exePatchEnabledDefault(), "Enable executable binary patching (default on; env: CLAUDE_PROXY_EXE_PATCH)")
	cmd.PersistentFlags().StringVar(&opts.exePatch.regex1, "exe-patch-regex-1", "", "Stage 1 regex to locate candidate code blocks in the target executable")
	cmd.PersistentFlags().StringArrayVar(&opts.exePatch.regex2, "exe-patch-regex-2", nil, "Stage 2 regex to confirm a stage 1 block should be patched (repeatable)")
	cmd.PersistentFlags().StringArrayVar(&opts.exePatch.regex3, "exe-patch-regex-3", nil, "Stage 3 regex to apply inside the stage 1 block (repeatable)")
	cmd.PersistentFlags().StringArrayVar(&opts.exePatch.replace, "exe-patch-replace", nil, "Replacement for stage 3 regex (repeatable, supports $1-style expansion)")
	cmd.PersistentFlags().BoolVar(&opts.exePatch.preview, "exe-patch-preview", false, "Print before/after matches when patching")
	cmd.PersistentFlags().BoolVar(&opts.exePatch.policySettings, "exe-patch-policy-settings", true, "Apply built-in policySettings patches (requires --exe-patch-enabled)")
	cmd.PersistentFlags().BoolVar(&opts.exePatch.glibcCompat, "exe-patch-glibc-compat", exePatchGlibcCompatDefault(), "Apply Linux glibc compat patch via patchelf when Claude fails with missing GLIBC symbols (requires --exe-patch-enabled)")
	cmd.PersistentFlags().StringVar(&opts.exePatch.glibcCompatRoot, "exe-patch-glibc-root", exePatchGlibcCompatRootDefault(), "Optional path to extracted glibc compat runtime root; when unset clp auto-downloads from GitHub release assets (env: CLAUDE_PROXY_GLIBC_COMPAT_ROOT)")
	cmd.PersistentFlags().BoolVar(&opts.exePatch.dryRun, "exe-patch-dry-run", false, "Run exe patch in memory without writing or launching the command (requires --exe-patch-enabled)")

	cmd.AddCommand(
		newInitCmd(opts),
		newRunCmd(opts),
		newRunJSONCmd(opts),
		newTuiCmd(opts),
		newProxyCmd(opts),
		newUpgradeCmd(opts),
		newUpgradeClaudeCmd(opts),
		newHistoryCmd(opts),
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
