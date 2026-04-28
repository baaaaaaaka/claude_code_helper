package cli

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/baaaaaaaka/claude_code_helper/internal/claudehistory"
	"github.com/baaaaaaaka/claude_code_helper/internal/config"
	"github.com/baaaaaaaka/claude_code_helper/internal/tui"
)

func TestRootDefaultClpRunsTuiWithoutArgs(t *testing.T) {
	store := newTempStore(t)
	disabled := false
	if err := store.Save(config.Config{Version: config.CurrentVersion, ProxyEnabled: &disabled}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	prevSelect := selectSession
	prevRequireTTY := historyRequireTTYFn
	t.Cleanup(func() {
		selectSession = prevSelect
		historyRequireTTYFn = prevRequireTTY
	})
	historyRequireTTYFn = func() error { return nil }

	called := false
	selectSession = func(ctx context.Context, opts tui.Options) (*tui.Selection, error) {
		called = true
		return nil, nil
	}

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--config", store.Path()})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !called {
		t.Fatalf("expected default clp path to run history TUI")
	}
}

func TestRootDefaultClpForwardsSingleProfile(t *testing.T) {
	store := newTempStore(t)
	enabled := true
	cfg := config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &enabled,
		Profiles: []config.Profile{
			{ID: "p1", Name: "one", Host: "host1", Port: 22, User: "user"},
			{ID: "p2", Name: "two", Host: "host2", Port: 22, User: "user"},
		},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	projectDir := t.TempDir()
	prevSelect := selectSession
	prevRunNew := runClaudeNewSessionFn
	prevRun := runClaudeSessionFunc
	prevRequireTTY := historyRequireTTYFn
	t.Cleanup(func() {
		selectSession = prevSelect
		runClaudeNewSessionFn = prevRunNew
		runClaudeSessionFunc = prevRun
		historyRequireTTYFn = prevRequireTTY
	})
	historyRequireTTYFn = func() error { return nil }

	selectSession = func(ctx context.Context, opts tui.Options) (*tui.Selection, error) {
		return &tui.Selection{Cwd: projectDir, UseProxy: opts.ProxyEnabled}, nil
	}
	runClaudeNewSessionFn = func(ctx context.Context, root *rootOptions, store *config.Store, profile *config.Profile, instances []config.Instance, cwd string, path string, dir string, useProxy bool, yoloMode config.YoloMode, log io.Writer) error {
		if profile == nil || profile.ID != "p2" {
			t.Fatalf("expected profile p2, got %#v", profile)
		}
		if !useProxy {
			t.Fatalf("expected useProxy=true")
		}
		if cwd != projectDir {
			t.Fatalf("expected cwd %q, got %q", projectDir, cwd)
		}
		if path != "" || dir != "" {
			t.Fatalf("expected empty claude path/dir, got path=%q dir=%q", path, dir)
		}
		return nil
	}
	runClaudeSessionFunc = func(ctx context.Context, root *rootOptions, store *config.Store, profile *config.Profile, instances []config.Instance, session claudehistory.Session, project claudehistory.Project, path string, dir string, useProxy bool, yoloMode config.YoloMode, log io.Writer) error {
		t.Fatalf("unexpected runClaudeSession call")
		return nil
	}

	cmd := &cobra.Command{}
	if err := cmd.Flags().Parse([]string{"two"}); err != nil {
		t.Fatalf("failed to parse args: %v", err)
	}
	cmd.SetContext(context.Background())
	if err := runDefaultTui(cmd, &rootOptions{configPath: store.Path()}); err != nil {
		t.Fatalf("runDefaultTui error: %v", err)
	}
}

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
