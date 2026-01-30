package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRunDefaultTuiRejectsExtraArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "args before dash",
			args: []string{"profile-a", "profile-b"},
			want: "unexpected args before --",
		},
		{
			name: "args after dash",
			args: []string{"profile-a", "--", "echo"},
			want: "unexpected args after --",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			if err := cmd.Flags().Parse(tc.args); err != nil {
				t.Fatalf("failed to parse args: %v", err)
			}
			err := runDefaultTui(cmd, &rootOptions{})
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error to contain %q, got %q", tc.want, err.Error())
			}
		})
	}
}
