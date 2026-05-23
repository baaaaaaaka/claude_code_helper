package cli

import "testing"

func TestClaudeLaunchOptionsArgs(t *testing.T) {
	opts := claudeLaunchOptions{Model: " opus ", Effort: " xhigh "}
	want := []string{"--model", "opus", "--effort", "xhigh"}
	requireArgsEqual(t, opts.args(), want)
}

func TestInsertClaudeLaunchArgsBeforeResume(t *testing.T) {
	args := []string{"--permission-mode", "bypassPermissions", "--resume", "sess-1"}
	opts := claudeLaunchOptions{Model: "sonnet", Effort: "high"}

	want := []string{"--permission-mode", "bypassPermissions", "--model", "sonnet", "--effort", "high", "--resume", "sess-1"}
	requireArgsEqual(t, insertClaudeLaunchArgsBeforeResume(args, opts), want)
}

func TestMergeClaudeLaunchOptionsPrefersPrimaryValues(t *testing.T) {
	got := mergeClaudeLaunchOptions(
		claudeLaunchOptions{Model: "opus"},
		claudeLaunchOptions{Model: "sonnet", Effort: "high"},
	)
	if got.Model != "opus" || got.Effort != "high" {
		t.Fatalf("unexpected merged options: %#v", got)
	}
}

func TestValidateClaudeLaunchArgConflicts(t *testing.T) {
	cases := []struct {
		name string
		opts claudeLaunchOptions
		args []string
	}{
		{
			name: "split model",
			opts: claudeLaunchOptions{Model: "opus"},
			args: []string{"--model", "sonnet"},
		},
		{
			name: "inline model",
			opts: claudeLaunchOptions{Model: "opus"},
			args: []string{"--model=sonnet"},
		},
		{
			name: "split effort",
			opts: claudeLaunchOptions{Effort: "high"},
			args: []string{"--effort", "low"},
		},
		{
			name: "inline effort",
			opts: claudeLaunchOptions{Effort: "high"},
			args: []string{"--effort=low"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateClaudeLaunchArgConflicts("test", tc.opts, tc.args); err == nil {
				t.Fatalf("expected conflict")
			}
		})
	}
}
