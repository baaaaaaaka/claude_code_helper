package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

type claudeLaunchOptions struct {
	Model  string
	Effort string
}

func addClaudeLaunchFlags(cmd *cobra.Command, opts *claudeLaunchOptions) {
	cmd.Flags().StringVar(&opts.Model, "model", "", "Claude model for launched sessions (alias such as sonnet/opus, or full model name)")
	cmd.Flags().StringVar(&opts.Effort, "effort", "", "Claude effort level for launched sessions (low, medium, high, xhigh, max)")
}

func (o claudeLaunchOptions) normalized() claudeLaunchOptions {
	return claudeLaunchOptions{
		Model:  strings.TrimSpace(o.Model),
		Effort: strings.TrimSpace(o.Effort),
	}
}

func (o claudeLaunchOptions) args() []string {
	o = o.normalized()
	args := []string{}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	if o.Effort != "" {
		args = append(args, "--effort", o.Effort)
	}
	return args
}

func mergeClaudeLaunchOptions(primary claudeLaunchOptions, fallback claudeLaunchOptions) claudeLaunchOptions {
	primary = primary.normalized()
	fallback = fallback.normalized()
	if primary.Model == "" {
		primary.Model = fallback.Model
	}
	if primary.Effort == "" {
		primary.Effort = fallback.Effort
	}
	return primary
}

func appendClaudeLaunchArgs(args []string, opts claudeLaunchOptions) []string {
	launchArgs := opts.args()
	if len(launchArgs) == 0 {
		return args
	}
	return append(args, launchArgs...)
}

func insertClaudeLaunchArgsBeforeResume(args []string, opts claudeLaunchOptions) []string {
	launchArgs := opts.args()
	if len(launchArgs) == 0 {
		return args
	}
	for i, arg := range args {
		if arg == "--resume" || strings.HasPrefix(arg, "--resume=") {
			out := make([]string, 0, len(args)+len(launchArgs))
			out = append(out, args[:i]...)
			out = append(out, launchArgs...)
			out = append(out, args[i:]...)
			return out
		}
	}
	return append(args, launchArgs...)
}

func hasExplicitClaudeFlag(args []string, flag string) bool {
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == flag || strings.HasPrefix(arg, flag+"=") {
			return true
		}
	}
	return false
}

func validateClaudeLaunchArgConflicts(source string, opts claudeLaunchOptions, args []string) error {
	opts = opts.normalized()
	if opts.Model != "" && hasExplicitClaudeFlag(args, "--model") {
		return fmt.Errorf("%s model conflicts with args --model; choose one place to set the model", source)
	}
	if opts.Effort != "" && hasExplicitClaudeFlag(args, "--effort") {
		return fmt.Errorf("%s effort conflicts with args --effort; choose one place to set effort", source)
	}
	return nil
}
