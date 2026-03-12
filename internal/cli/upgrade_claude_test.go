package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

func TestUpgradeClaudeInstallOptsNoProxy(t *testing.T) {
	disabled := false
	cfg := config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &disabled,
	}
	opts, err := upgradeClaudeInstallOpts(cfg, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.UseProxy {
		t.Fatalf("expected UseProxy=false when ProxyEnabled=false")
	}
}

func TestUpgradeClaudeInstallOptsWithProxy(t *testing.T) {
	enabled := true
	profile := config.Profile{ID: "p1", Name: "p1"}
	instances := []config.Instance{{ID: "inst-1", ProfileID: "p1"}}
	cfg := config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &enabled,
		Profiles:     []config.Profile{profile},
		Instances:    instances,
	}
	opts, err := upgradeClaudeInstallOpts(cfg, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !opts.UseProxy {
		t.Fatalf("expected UseProxy=true")
	}
	if opts.Profile == nil || opts.Profile.ID != "p1" {
		t.Fatalf("expected profile p1, got %v", opts.Profile)
	}
	if len(opts.Instances) != 1 || opts.Instances[0].ID != "inst-1" {
		t.Fatalf("expected instances to be passed through")
	}
}

func TestUpgradeClaudeInstallOptsImpliedProxy(t *testing.T) {
	cfg := config.Config{
		Version:  config.CurrentVersion,
		Profiles: []config.Profile{{ID: "p1", Name: "p1"}},
	}
	opts, err := upgradeClaudeInstallOpts(cfg, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !opts.UseProxy {
		t.Fatalf("expected UseProxy=true when ProxyEnabled=nil and profiles exist")
	}
	if opts.Profile == nil || opts.Profile.ID != "p1" {
		t.Fatalf("expected profile p1")
	}
}

func TestUpgradeClaudeInstallOptsEmptyConfig(t *testing.T) {
	cfg := config.Config{Version: config.CurrentVersion}
	opts, err := upgradeClaudeInstallOpts(cfg, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.UseProxy {
		t.Fatalf("expected UseProxy=false when no profiles and no proxy preference")
	}
}

func TestUpgradeClaudeInstallOptsMultipleProfilesNoRef(t *testing.T) {
	enabled := true
	cfg := config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &enabled,
		Profiles: []config.Profile{
			{ID: "p1", Name: "p1"},
			{ID: "p2", Name: "p2"},
		},
	}
	_, err := upgradeClaudeInstallOpts(cfg, "")
	if err == nil {
		t.Fatalf("expected error when multiple profiles and no --profile")
	}
	if !strings.Contains(err.Error(), "multiple profiles") {
		t.Fatalf("expected 'multiple profiles' error, got: %v", err)
	}
}

func TestUpgradeClaudeInstallOptsMultipleProfilesWithRef(t *testing.T) {
	enabled := true
	cfg := config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &enabled,
		Profiles: []config.Profile{
			{ID: "p1", Name: "alpha"},
			{ID: "p2", Name: "beta"},
		},
	}
	opts, err := upgradeClaudeInstallOpts(cfg, "beta")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Profile == nil || opts.Profile.ID != "p2" {
		t.Fatalf("expected profile p2, got %v", opts.Profile)
	}
}

func TestUpgradeClaudeInstallOptsProfileNotFound(t *testing.T) {
	enabled := true
	cfg := config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &enabled,
		Profiles:     []config.Profile{{ID: "p1", Name: "p1"}},
	}
	_, err := upgradeClaudeInstallOpts(cfg, "unknown")
	if err == nil {
		t.Fatalf("expected error for unknown profile")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' error, got: %v", err)
	}
}

func TestRunUpgradeClaudeUninitializedConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "nonexistent", "config.json")

	root := &rootOptions{configPath: configPath}
	cmd := newUpgradeClaudeCmd(root)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error for uninitialized config")
	}
	if !strings.Contains(err.Error(), "not been initialized") {
		t.Fatalf("expected 'not been initialized' error, got: %v", err)
	}
}

func TestRunUpgradeClaudeNoProxyDirect(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}

	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("https_proxy", "")

	store := newTempStore(t)
	disabled := false
	if err := store.Save(config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &disabled,
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	envFile := filepath.Join(dir, "env.txt")
	script := filepath.Join(dir, "bash")
	scriptBody := fmt.Sprintf("#!/bin/sh\nprintf ok > %q\nprintf '%%s\\n%%s\\n' \"$HTTP_PROXY\" \"$HTTPS_PROXY\" > %q\nexit 0\n", marker, envFile)
	if err := os.WriteFile(script, []byte(scriptBody), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	root := &rootOptions{configPath: store.Path()}
	cmd := newUpgradeClaudeCmd(root)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("upgrade-claude error: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("expected installer to run: %v", err)
	}

	content, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			t.Fatalf("expected empty HTTP_PROXY/HTTPS_PROXY, got %q", string(content))
		}
	}
}

func TestRunUpgradeClaudePrewarmsPatchedClaude(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	store := newTempStore(t)
	disabled := false
	if err := store.Save(config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &disabled,
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	prepareCalled := false
	waitCalled := false
	prevInstaller := runClaudeInstallerFn
	runClaudeInstallerFn = func(ctx context.Context, out io.Writer, opts installProxyOptions) error {
		return nil
	}
	t.Cleanup(func() {
		runClaudeInstallerFn = prevInstaller
	})
	maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
		prepareCalled = true
		if len(cmdArgs) != 1 || cmdArgs[0] != "claude" {
			t.Fatalf("unexpected patch prep args: %v", cmdArgs)
		}
		return &patchOutcome{}, nil
	}
	waitPatchedExecutableReadyFn = func(ctx context.Context, outcome *patchOutcome) error {
		waitCalled = true
		return nil
	}

	root := &rootOptions{
		configPath: store.Path(),
		exePatch: exePatchOptions{
			enabledFlag:    true,
			policySettings: true,
		},
	}
	cmd := newUpgradeClaudeCmd(root)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("upgrade-claude error: %v", err)
	}
	if !prepareCalled {
		t.Fatalf("expected upgrade-claude to prewarm patched claude")
	}
	if !waitCalled {
		t.Fatalf("expected upgrade-claude to wait for readiness")
	}
}

func TestRunUpgradeClaudeWithProxyUsesProxyEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	instanceID := "inst-1"
	mux := http.NewServeMux()
	mux.HandleFunc("/_claude_proxy/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":         true,
			"instanceId": instanceID,
		})
	})
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(ln) }()
	t.Cleanup(func() { _ = server.Close() })

	store := newTempStore(t)
	enabled := true
	profile := config.Profile{ID: "profile-1", Name: "profile-1"}
	if err := store.Save(config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &enabled,
		Profiles:     []config.Profile{profile},
		Instances: []config.Instance{{
			ID:        instanceID,
			ProfileID: profile.ID,
			HTTPPort:  port,
			DaemonPID: os.Getpid(),
		}},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := t.TempDir()
	outFile := filepath.Join(dir, "env.txt")
	script := filepath.Join(dir, "bash")
	scriptBody := "#!/bin/sh\nprintf \"%s\\n%s\\n\" \"$HTTP_PROXY\" \"$HTTPS_PROXY\" > \"$OUT_FILE\"\nexit 0\n"
	if err := os.WriteFile(script, []byte(scriptBody), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}

	t.Setenv("OUT_FILE", outFile)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	root := &rootOptions{configPath: store.Path()}
	cmd := newUpgradeClaudeCmd(root)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("upgrade-claude error: %v", err)
	}

	content, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), string(content))
	}
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if lines[0] != proxyURL || lines[1] != proxyURL {
		t.Fatalf("expected proxy env %q, got %q", proxyURL, strings.Join(lines, ","))
	}
}

func TestNewUpgradeClaudeCmdExists(t *testing.T) {
	root := &rootOptions{}
	cmd := newUpgradeClaudeCmd(root)
	if cmd.Use != "upgrade-claude" {
		t.Fatalf("expected Use='upgrade-claude', got %q", cmd.Use)
	}
	f := cmd.Flags().Lookup("profile")
	if f == nil {
		t.Fatalf("expected --profile flag")
	}
	if f.DefValue != "" {
		t.Fatalf("expected empty default for --profile, got %q", f.DefValue)
	}
}

func TestUpgradeClaudeCmdRegistered(t *testing.T) {
	root := newRootCmd()
	found := false
	for _, sub := range root.Commands() {
		if sub.Use == "upgrade-claude" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("upgrade-claude subcommand not registered on root command")
	}
}

func TestRunUpgradeClaudeStatError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip permission test on windows")
	}
	if os.Getuid() == 0 {
		t.Skip("skip permission test when running as root")
	}

	dir := t.TempDir()
	subDir := filepath.Join(dir, "restricted")
	if err := os.Mkdir(subDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	configPath := filepath.Join(subDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.Chmod(subDir, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(subDir, 0o700) })

	root := &rootOptions{configPath: configPath}
	cmd := newUpgradeClaudeCmd(root)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error for inaccessible config")
	}
	if !strings.Contains(err.Error(), "cannot access") {
		t.Fatalf("expected 'cannot access' error, got: %v", err)
	}
}

func TestRunUpgradeClaudeCorruptConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{{{`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	root := &rootOptions{configPath: configPath}
	cmd := newUpgradeClaudeCmd(root)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error for corrupt config")
	}
	if strings.Contains(err.Error(), "not been initialized") {
		t.Fatalf("should not report 'not been initialized' for corrupt config, got: %v", err)
	}
}

func TestUpgradeClaudeInstallOptsProxyEnabledNoProfiles(t *testing.T) {
	enabled := true
	cfg := config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &enabled,
		Profiles:     []config.Profile{},
	}
	_, err := upgradeClaudeInstallOpts(cfg, "")
	if err == nil {
		t.Fatalf("expected error when proxy enabled but no profiles")
	}
	if !strings.Contains(err.Error(), "no profiles found") {
		t.Fatalf("expected 'no profiles found' error, got: %v", err)
	}
}

func TestRunUpgradeClaudeWithProfileFlag(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	instanceID := "inst-1"
	mux := http.NewServeMux()
	mux.HandleFunc("/_claude_proxy/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":         true,
			"instanceId": instanceID,
		})
	})
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(ln) }()
	t.Cleanup(func() { _ = server.Close() })

	store := newTempStore(t)
	enabled := true
	if err := store.Save(config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &enabled,
		Profiles: []config.Profile{
			{ID: "p1", Name: "alpha"},
			{ID: "p2", Name: "beta"},
		},
		Instances: []config.Instance{{
			ID:        instanceID,
			ProfileID: "p2",
			HTTPPort:  port,
			DaemonPID: os.Getpid(),
		}},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := t.TempDir()
	outFile := filepath.Join(dir, "env.txt")
	script := filepath.Join(dir, "bash")
	scriptBody := "#!/bin/sh\nprintf \"%s\\n%s\\n\" \"$HTTP_PROXY\" \"$HTTPS_PROXY\" > \"$OUT_FILE\"\nexit 0\n"
	if err := os.WriteFile(script, []byte(scriptBody), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}

	t.Setenv("OUT_FILE", outFile)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	root := &rootOptions{configPath: store.Path()}
	cmd := newUpgradeClaudeCmd(root)
	cmd.SetArgs([]string{"--profile", "beta"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("upgrade-claude --profile beta error: %v", err)
	}

	content, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), string(content))
	}
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if lines[0] != proxyURL || lines[1] != proxyURL {
		t.Fatalf("expected proxy env %q, got %q", proxyURL, strings.Join(lines, ","))
	}
}

func TestInvalidateExePatchState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip on windows")
	}

	// Create a fake "claude" binary.
	binDir := t.TempDir()
	fakeClaude := filepath.Join(binDir, "claude")
	if err := os.WriteFile(fakeClaude, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Resolve the path the same way invalidateExePatchState will (follows symlinks).
	resolvedClaude, err := resolveExecutablePath(fakeClaude)
	if err != nil {
		t.Fatalf("resolve path: %v", err)
	}

	// Create a stale backup file alongside the binary (at resolved path).
	backupPath := resolvedClaude + ".claude-proxy.bak"
	if err := os.WriteFile(backupPath, []byte("old-backup"), 0o600); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	// Create a patch history with an entry for this binary (at resolved path).
	store := newTempStore(t)
	historyStore, err2 := config.NewPatchHistoryStore(store.Path())
	if err2 != nil {
		t.Fatalf("new patch history store: %v", err2)
	}
	if err := historyStore.Update(func(h *config.PatchHistory) error {
		h.Upsert(config.PatchHistoryEntry{
			Path:          resolvedClaude,
			SpecsSHA256:   "specs-hash",
			PatchedSHA256: "patched-hash",
			ProxyVersion:  "0.0.38",
		})
		return nil
	}); err != nil {
		t.Fatalf("update history: %v", err)
	}

	// Run invalidation.
	invalidateExePatchState("claude", store.Path())

	// Backup should be removed.
	if _, err := os.Stat(backupPath); !os.IsNotExist(err) {
		t.Fatalf("expected backup to be removed, got err=%v", err)
	}

	// Patch history entry should be removed.
	history, err := historyStore.Load()
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	for _, entry := range history.Entries {
		if entry.Path == resolvedClaude {
			t.Fatalf("expected patch history entry to be removed, found: %+v", entry)
		}
	}
}
