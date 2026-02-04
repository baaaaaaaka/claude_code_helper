package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func runDefaultTui(cmd *cobra.Command, root *rootOptions) error {
	all := cmd.Flags().Args()
	dash := cmd.Flags().ArgsLenAtDash()

	before := all
	after := []string{}
	if dash >= 0 {
		before = all[:dash]
		after = all[dash:]
	}

	if len(before) > 1 {
		return fmt.Errorf("unexpected args before -- (only profile is allowed)")
	}
	if len(after) > 0 {
		return fmt.Errorf("unexpected args after -- (use `claude-proxy run` to run commands)")
	}

	profileRef := ""
	if len(before) == 1 {
		profileRef = before[0]
	}

	return runHistoryTui(cmd, root, profileRef, "", "", defaultRefreshInterval)
}
