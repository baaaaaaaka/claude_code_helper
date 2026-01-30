package cli

import (
	"os"
	"testing"
)

func TestBuildVersion(t *testing.T) {
	prevVersion := version
	prevCommit := commit
	prevDate := date
	t.Cleanup(func() {
		version = prevVersion
		commit = prevCommit
		date = prevDate
	})

	version = "1.2.3"
	commit = "abc123"
	date = "2026-01-01"

	got := buildVersion()
	want := "1.2.3 (abc123) 2026-01-01"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestNewRootCmdFlags(t *testing.T) {
	cmd := newRootCmd()
	flag := cmd.PersistentFlags().Lookup("exe-patch-policy-settings")
	if flag == nil {
		t.Fatalf("expected exe-patch-policy-settings flag to exist")
	}
	if flag.DefValue != "true" {
		t.Fatalf("expected policySettings default true, got %q", flag.DefValue)
	}
}

func TestNewNotImplementedCmd(t *testing.T) {
	cmd := newNotImplementedCmd("foo", "bar")
	if cmd.Use != "foo" {
		t.Fatalf("expected Use to be %q, got %q", "foo", cmd.Use)
	}
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Fatalf("expected RunE to return error")
	}
}

func TestExecuteVersion(t *testing.T) {
	prevArgs := os.Args
	t.Cleanup(func() { os.Args = prevArgs })
	os.Args = []string{"claude-proxy", "--version"}
	if code := Execute(); code != 0 {
		t.Fatalf("expected Execute to return 0 for --version, got %d", code)
	}
}

func TestExecuteInvalidArgs(t *testing.T) {
	prevArgs := os.Args
	t.Cleanup(func() { os.Args = prevArgs })
	os.Args = []string{"claude-proxy", "--not-a-flag"}
	if code := Execute(); code != 1 {
		t.Fatalf("expected Execute to return 1 for invalid args, got %d", code)
	}
}

func TestNewRootCmdUnknownCommand(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{"definitely-not-a-command"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected unknown command to return error")
	}
}
