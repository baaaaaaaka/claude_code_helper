package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/spf13/cobra"

	"github.com/baaaaaaaka/claude_code_helper/internal/claudehistory"
	"github.com/baaaaaaaka/claude_code_helper/internal/config"
	"github.com/baaaaaaaka/claude_code_helper/internal/tui"
	"github.com/baaaaaaaka/claude_code_helper/internal/update"
)

func TestRunHistoryTuiFailsWhenConfigDirReadOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip read-only directory test on windows")
	}
	base := t.TempDir()
	configDir := filepath.Join(base, "config")
	if err := os.MkdirAll(configDir, 0o500); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(configDir, 0o700) })

	root := &rootOptions{configPath: filepath.Join(configDir, "config.json")}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	if err := runHistoryTui(cmd, root, "", "", "", 0); err == nil {
		t.Fatalf("expected error when config dir is read-only")
	}
}

func TestRunHistoryTuiRunsNewSession(t *testing.T) {
	store := newTempStore(t)
	disabled := false
	cfg := config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &disabled,
		Profiles: []config.Profile{{
			ID:   "p1",
			Name: "profile",
			Host: "host",
			Port: 22,
			User: "user",
		}},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	claudePath := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write claude: %v", err)
	}

	prevSelect := selectSession
	prevRunNew := runClaudeNewSessionFn
	prevRun := runClaudeSessionFunc
	t.Cleanup(func() {
		selectSession = prevSelect
		runClaudeNewSessionFn = prevRunNew
		runClaudeSessionFunc = prevRun
	})

	calledNew := false
	selectSession = func(ctx context.Context, opts tui.Options) (*tui.Selection, error) {
		return &tui.Selection{Cwd: t.TempDir()}, nil
	}
	runClaudeNewSessionFn = func(ctx context.Context, root *rootOptions, store *config.Store, profile *config.Profile, instances []config.Instance, cwd string, path string, dir string, useProxy bool, useYolo bool, log io.Writer) error {
		calledNew = true
		return nil
	}
	runClaudeSessionFunc = func(ctx context.Context, root *rootOptions, store *config.Store, profile *config.Profile, instances []config.Instance, session claudehistory.Session, project claudehistory.Project, path string, dir string, useProxy bool, useYolo bool, log io.Writer) error {
		t.Fatalf("unexpected runClaudeSession call")
		return nil
	}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	if err := runHistoryTui(cmd, &rootOptions{configPath: store.Path()}, "", "", claudePath, 0); err != nil {
		t.Fatalf("runHistoryTui error: %v", err)
	}
	if !calledNew {
		t.Fatalf("expected runClaudeNewSession to be called")
	}
}

func TestRunHistoryTuiRunsSession(t *testing.T) {
	store := newTempStore(t)
	disabled := false
	cfg := config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &disabled,
		Profiles: []config.Profile{{
			ID:   "p1",
			Name: "profile",
			Host: "host",
			Port: 22,
			User: "user",
		}},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	claudePath := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write claude: %v", err)
	}

	prevSelect := selectSession
	prevRunNew := runClaudeNewSessionFn
	prevRun := runClaudeSessionFunc
	t.Cleanup(func() {
		selectSession = prevSelect
		runClaudeNewSessionFn = prevRunNew
		runClaudeSessionFunc = prevRun
	})

	called := false
	selectSession = func(ctx context.Context, opts tui.Options) (*tui.Selection, error) {
		return &tui.Selection{
			Session: claudehistory.Session{SessionID: "sess-1"},
			Project: claudehistory.Project{Path: t.TempDir()},
		}, nil
	}
	runClaudeNewSessionFn = func(ctx context.Context, root *rootOptions, store *config.Store, profile *config.Profile, instances []config.Instance, cwd string, path string, dir string, useProxy bool, useYolo bool, log io.Writer) error {
		t.Fatalf("unexpected runClaudeNewSession call")
		return nil
	}
	runClaudeSessionFunc = func(ctx context.Context, root *rootOptions, store *config.Store, profile *config.Profile, instances []config.Instance, session claudehistory.Session, project claudehistory.Project, path string, dir string, useProxy bool, useYolo bool, log io.Writer) error {
		called = true
		if session.SessionID != "sess-1" {
			t.Fatalf("unexpected session id %q", session.SessionID)
		}
		return nil
	}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	if err := runHistoryTui(cmd, &rootOptions{configPath: store.Path()}, "", "", claudePath, 0); err != nil {
		t.Fatalf("runHistoryTui error: %v", err)
	}
	if !called {
		t.Fatalf("expected runClaudeSession to be called")
	}
}

func TestRunHistoryTuiHandlesUpdateRequest(t *testing.T) {
	store := newTempStore(t)
	disabled := false
	if err := store.Save(config.Config{Version: config.CurrentVersion, ProxyEnabled: &disabled}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	claudePath := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write claude: %v", err)
	}

	prevSelect := selectSession
	prevUpdate := performUpdate
	t.Cleanup(func() {
		selectSession = prevSelect
		performUpdate = prevUpdate
	})

	selectSession = func(ctx context.Context, opts tui.Options) (*tui.Selection, error) {
		return nil, tui.UpdateRequested{}
	}
	performUpdate = func(ctx context.Context, opts update.UpdateOptions) (update.ApplyResult, error) {
		return update.ApplyResult{Version: "1.2.3", RestartRequired: true}, nil
	}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	if err := runHistoryTui(cmd, &rootOptions{configPath: store.Path()}, "", "", claudePath, 0); err != nil {
		t.Fatalf("runHistoryTui error: %v", err)
	}
}

func TestRunHistoryTuiHandlesProxyToggle(t *testing.T) {
	store := newTempStore(t)
	disabled := false
	if err := store.Save(config.Config{Version: config.CurrentVersion, ProxyEnabled: &disabled}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	claudePath := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write claude: %v", err)
	}

	prevSelect := selectSession
	t.Cleanup(func() { selectSession = prevSelect })

	calls := 0
	selectSession = func(ctx context.Context, opts tui.Options) (*tui.Selection, error) {
		calls++
		if calls == 1 {
			return nil, tui.ProxyToggleRequested{Enable: false, RequireConfig: false}
		}
		return nil, nil
	}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	if err := runHistoryTui(cmd, &rootOptions{configPath: store.Path()}, "", "", claudePath, 0); err != nil {
		t.Fatalf("runHistoryTui error: %v", err)
	}
}
