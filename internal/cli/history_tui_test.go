package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

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

	prevRequireTTY := historyRequireTTYFn
	historyRequireTTYFn = func() error { return nil }
	t.Cleanup(func() { historyRequireTTYFn = prevRequireTTY })

	if err := runHistoryTui(cmd, root, "", "", "", 0); err == nil {
		t.Fatalf("expected error when config dir is read-only")
	}
}

func TestRunHistoryTuiDoesNotInstallClaudeUntilLaunch(t *testing.T) {
	store := newTempStore(t)
	disabled := false
	if err := store.Save(config.Config{Version: config.CurrentVersion, ProxyEnabled: &disabled}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	prevSelect := selectSession
	prevInstaller := runClaudeInstallerWithEnvFn
	prevRequireTTY := historyRequireTTYFn
	t.Cleanup(func() {
		selectSession = prevSelect
		runClaudeInstallerWithEnvFn = prevInstaller
		historyRequireTTYFn = prevRequireTTY
	})
	historyRequireTTYFn = func() error { return nil }

	installerCalled := false
	runClaudeInstallerWithEnvFn = func(ctx context.Context, out io.Writer, opts installProxyOptions, extraEnv []string) error {
		installerCalled = true
		return errors.New("unexpected installer call")
	}
	selectSession = func(ctx context.Context, opts tui.Options) (*tui.Selection, error) {
		return nil, nil
	}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	if err := runHistoryTui(cmd, &rootOptions{configPath: store.Path()}, "", "", "", 0); err != nil {
		t.Fatalf("runHistoryTui error: %v", err)
	}
	if installerCalled {
		t.Fatalf("expected TUI startup to avoid eager Claude installation")
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
	prevRequireTTY := historyRequireTTYFn
	t.Cleanup(func() {
		selectSession = prevSelect
		runClaudeNewSessionFn = prevRunNew
		runClaudeSessionFunc = prevRun
		historyRequireTTYFn = prevRequireTTY
	})
	historyRequireTTYFn = func() error { return nil }

	calledNew := false
	selectSession = func(ctx context.Context, opts tui.Options) (*tui.Selection, error) {
		return &tui.Selection{Cwd: t.TempDir()}, nil
	}
	runClaudeNewSessionFn = func(ctx context.Context, root *rootOptions, store *config.Store, profile *config.Profile, instances []config.Instance, cwd string, path string, dir string, useProxy bool, yoloMode config.YoloMode, log io.Writer) error {
		calledNew = true
		return nil
	}
	runClaudeSessionFunc = func(ctx context.Context, root *rootOptions, store *config.Store, profile *config.Profile, instances []config.Instance, session claudehistory.Session, project claudehistory.Project, path string, dir string, useProxy bool, yoloMode config.YoloMode, log io.Writer) error {
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

func TestTuiCmdPassesClaudeLaunchFlags(t *testing.T) {
	store := newTempStore(t)
	disabled := false
	if err := store.Save(config.Config{Version: config.CurrentVersion, ProxyEnabled: &disabled}); err != nil {
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
		return &tui.Selection{Cwd: projectDir}, nil
	}
	runClaudeNewSessionFn = func(ctx context.Context, root *rootOptions, store *config.Store, profile *config.Profile, instances []config.Instance, cwd string, path string, dir string, useProxy bool, yoloMode config.YoloMode, log io.Writer) error {
		if root.claudeLaunch.Model != "sonnet" || root.claudeLaunch.Effort != "high" {
			t.Fatalf("unexpected Claude launch options: %#v", root.claudeLaunch)
		}
		return nil
	}
	runClaudeSessionFunc = func(ctx context.Context, root *rootOptions, store *config.Store, profile *config.Profile, instances []config.Instance, session claudehistory.Session, project claudehistory.Project, path string, dir string, useProxy bool, yoloMode config.YoloMode, log io.Writer) error {
		t.Fatalf("unexpected runClaudeSession call")
		return nil
	}

	root := &rootOptions{configPath: store.Path()}
	cmd := newTuiCmd(root)
	cmd.SetArgs([]string{"--model", "sonnet", "--effort", "high"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
	}
}

func TestHistoryTuiCmdPassesClaudeLaunchFlags(t *testing.T) {
	store := newTempStore(t)
	disabled := false
	if err := store.Save(config.Config{Version: config.CurrentVersion, ProxyEnabled: &disabled}); err != nil {
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
		return &tui.Selection{Cwd: projectDir}, nil
	}
	runClaudeNewSessionFn = func(ctx context.Context, root *rootOptions, store *config.Store, profile *config.Profile, instances []config.Instance, cwd string, path string, dir string, useProxy bool, yoloMode config.YoloMode, log io.Writer) error {
		if root.claudeLaunch.Model != "opus" || root.claudeLaunch.Effort != "max" {
			t.Fatalf("unexpected Claude launch options: %#v", root.claudeLaunch)
		}
		return nil
	}
	runClaudeSessionFunc = func(ctx context.Context, root *rootOptions, store *config.Store, profile *config.Profile, instances []config.Instance, session claudehistory.Session, project claudehistory.Project, path string, dir string, useProxy bool, yoloMode config.YoloMode, log io.Writer) error {
		t.Fatalf("unexpected runClaudeSession call")
		return nil
	}

	claudeDir := ""
	claudePath := ""
	profileRef := ""
	root := &rootOptions{configPath: store.Path()}
	cmd := newHistoryTuiCmd(root, &claudeDir, &claudePath, &profileRef)
	cmd.SetArgs([]string{"--model", "opus", "--effort", "max"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
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
	prevRequireTTY := historyRequireTTYFn
	t.Cleanup(func() {
		selectSession = prevSelect
		runClaudeNewSessionFn = prevRunNew
		runClaudeSessionFunc = prevRun
		historyRequireTTYFn = prevRequireTTY
	})
	historyRequireTTYFn = func() error { return nil }

	called := false
	selectSession = func(ctx context.Context, opts tui.Options) (*tui.Selection, error) {
		return &tui.Selection{
			Session: claudehistory.Session{SessionID: "sess-1"},
			Project: claudehistory.Project{Path: t.TempDir()},
		}, nil
	}
	runClaudeNewSessionFn = func(ctx context.Context, root *rootOptions, store *config.Store, profile *config.Profile, instances []config.Instance, cwd string, path string, dir string, useProxy bool, yoloMode config.YoloMode, log io.Writer) error {
		t.Fatalf("unexpected runClaudeNewSession call")
		return nil
	}
	runClaudeSessionFunc = func(ctx context.Context, root *rootOptions, store *config.Store, profile *config.Profile, instances []config.Instance, session claudehistory.Session, project claudehistory.Project, path string, dir string, useProxy bool, yoloMode config.YoloMode, log io.Writer) error {
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
	prevRequireTTY := historyRequireTTYFn
	t.Cleanup(func() {
		selectSession = prevSelect
		performUpdate = prevUpdate
		historyRequireTTYFn = prevRequireTTY
	})
	historyRequireTTYFn = func() error { return nil }

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
	prevRequireTTY := historyRequireTTYFn
	t.Cleanup(func() {
		selectSession = prevSelect
		historyRequireTTYFn = prevRequireTTY
	})
	historyRequireTTYFn = func() error { return nil }

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

func TestRunHistoryTuiForwardsYoloOptions(t *testing.T) {
	store := newTempStore(t)
	disabled := false
	mode := string(config.YoloModeRules)
	if err := store.Save(config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &disabled,
		YoloMode:     &mode,
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	prevSelect := selectSession
	prevRequireTTY := historyRequireTTYFn
	t.Cleanup(func() {
		selectSession = prevSelect
		historyRequireTTYFn = prevRequireTTY
	})
	historyRequireTTYFn = func() error { return nil }

	refreshInterval := 42 * time.Millisecond
	called := false
	selectSession = func(ctx context.Context, opts tui.Options) (*tui.Selection, error) {
		called = true
		if !opts.YoloVisible {
			t.Fatalf("expected yolo controls to be visible")
		}
		if opts.YoloMode != config.YoloModeRules {
			t.Fatalf("expected rules yolo mode, got %q", opts.YoloMode)
		}
		if opts.RefreshInterval != refreshInterval {
			t.Fatalf("expected refresh interval %s, got %s", refreshInterval, opts.RefreshInterval)
		}
		if opts.PersistYolo == nil {
			t.Fatalf("expected PersistYolo callback")
		}
		if err := opts.PersistYolo(config.YoloModeBypass); err != nil {
			t.Fatalf("PersistYolo error: %v", err)
		}
		return nil, nil
	}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	if err := runHistoryTui(cmd, &rootOptions{configPath: store.Path()}, "", "", "", refreshInterval); err != nil {
		t.Fatalf("runHistoryTui error: %v", err)
	}
	if !called {
		t.Fatalf("expected selectSession to be called")
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if loaded.YoloMode == nil || *loaded.YoloMode != string(config.YoloModeBypass) {
		t.Fatalf("expected yolo mode to persist bypass, got %#v", loaded.YoloMode)
	}
	if loaded.YoloEnabled == nil || !*loaded.YoloEnabled {
		t.Fatalf("expected legacy yolo enabled compatibility flag, got %#v", loaded.YoloEnabled)
	}
}

func TestRunHistoryTuiRejectsNonTTY(t *testing.T) {
	prevRequireTTY := historyRequireTTYFn
	prevSelect := selectSession
	t.Cleanup(func() {
		historyRequireTTYFn = prevRequireTTY
		selectSession = prevSelect
	})

	wantErr := errors.New("not a tty")
	historyRequireTTYFn = func() error { return wantErr }
	selectSession = func(ctx context.Context, opts tui.Options) (*tui.Selection, error) {
		t.Fatalf("selectSession must not be called when TTY check fails")
		return nil, nil
	}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	err := runHistoryTui(cmd, &rootOptions{}, "", "", "", 0)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
}
