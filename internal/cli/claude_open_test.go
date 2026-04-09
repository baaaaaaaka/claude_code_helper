package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/baaaaaaaka/claude_code_helper/internal/claudehistory"
	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

func writeClaudeHelpStub(t *testing.T) string {
	return writeClaudeHelpStubWithOutput(t, "--permission-mode")
}

func writeClaudeHelpStubWithOutput(t *testing.T, output string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script execution on windows")
	}
	path := filepath.Join(t.TempDir(), "claude")
	body := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo \"Claude Code 1.2.3\"\n  exit 0\nfi\nif [ \"$1\" = \"--help\" ]; then\n  printf '%s\\n' " + shellSingleQuote(output) + "\n  exit 0\nfi\nexit 0\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write claude stub: %v", err)
	}
	return path
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func cloneArgs(args []string) []string {
	return append([]string(nil), args...)
}

func requireArgsEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected args %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected args %v, got %v", want, got)
		}
	}
}

func TestBuildClaudeResumeCommandUsesSessionPath(t *testing.T) {
	dir := t.TempDir()
	session := claudehistory.Session{SessionID: "abc", ProjectPath: dir}
	project := claudehistory.Project{Path: "/tmp/other"}

	path, args, cwd, err := buildClaudeResumeCommand("/bin/claude", session, project, config.YoloModeOff)
	if err != nil {
		t.Fatalf("buildClaudeResumeCommand error: %v", err)
	}
	if path != "/bin/claude" {
		t.Fatalf("expected path /bin/claude, got %s", path)
	}
	if len(args) != 2 || args[0] != "--resume" || args[1] != "abc" {
		t.Fatalf("unexpected args: %#v", args)
	}
	if cwd != dir {
		t.Fatalf("expected cwd %s, got %s", dir, cwd)
	}
}

func TestBuildClaudeResumeCommandUsesProjectPath(t *testing.T) {
	dir := t.TempDir()
	session := claudehistory.Session{SessionID: "abc"}
	project := claudehistory.Project{Path: dir}

	_, _, cwd, err := buildClaudeResumeCommand("/bin/claude", session, project, config.YoloModeOff)
	if err != nil {
		t.Fatalf("buildClaudeResumeCommand error: %v", err)
	}
	if cwd != dir {
		t.Fatalf("expected cwd %s, got %s", dir, cwd)
	}
}

func TestBuildClaudeResumeCommandAddsYoloArgs(t *testing.T) {
	dir := t.TempDir()
	session := claudehistory.Session{SessionID: "abc"}
	project := claudehistory.Project{Path: dir}
	claudePath := writeClaudeHelpStub(t)

	_, args, _, err := buildClaudeResumeCommand(claudePath, session, project, config.YoloModeBypass)
	if err != nil {
		t.Fatalf("buildClaudeResumeCommand error: %v", err)
	}
	want := []string{"--permission-mode", "bypassPermissions", "--resume", "abc"}
	if len(args) != len(want) {
		t.Fatalf("expected args %v, got %v", want, args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("expected args %v, got %v", want, args)
		}
	}
}

func TestBuildClaudeResumeCommandAddsDangerousSkipWhenSupported(t *testing.T) {
	dir := t.TempDir()
	session := claudehistory.Session{SessionID: "abc"}
	project := claudehistory.Project{Path: dir}
	claudePath := writeClaudeHelpStubWithOutput(t, "--dangerously-skip-permissions\n--permission-mode")

	_, args, _, err := buildClaudeResumeCommand(claudePath, session, project, config.YoloModeBypass)
	if err != nil {
		t.Fatalf("buildClaudeResumeCommand error: %v", err)
	}
	want := []string{"--dangerously-skip-permissions", "--permission-mode", "bypassPermissions", "--resume", "abc"}
	requireArgsEqual(t, args, want)
}

func TestBuildClaudeResumeCommandKeepsRulesModeArgFree(t *testing.T) {
	dir := t.TempDir()
	session := claudehistory.Session{SessionID: "abc"}
	project := claudehistory.Project{Path: dir}

	_, args, _, err := buildClaudeResumeCommand("/bin/claude", session, project, config.YoloModeRules)
	if err != nil {
		t.Fatalf("buildClaudeResumeCommand error: %v", err)
	}
	want := []string{"--resume", "abc"}
	requireArgsEqual(t, args, want)
}

func TestBuildClaudeResumeCommandUsesManagedClaudeWhenUnset(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	managedClaude := filepath.Join(home, ".claude", "local", "claude")
	if err := os.MkdirAll(filepath.Dir(managedClaude), 0o755); err != nil {
		t.Fatalf("mkdir managed Claude dir: %v", err)
	}
	if err := os.WriteFile(managedClaude, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write managed Claude: %v", err)
	}

	dir := t.TempDir()
	session := claudehistory.Session{SessionID: "abc"}
	project := claudehistory.Project{Path: dir}

	path, args, cwd, err := buildClaudeResumeCommand("", session, project, config.YoloModeOff)
	if err != nil {
		t.Fatalf("buildClaudeResumeCommand error: %v", err)
	}
	if path != managedClaude {
		t.Fatalf("expected managed Claude path %q, got %q", managedClaude, path)
	}
	want := []string{"--resume", "abc"}
	requireArgsEqual(t, args, want)
	if cwd != dir {
		t.Fatalf("expected cwd %s, got %s", dir, cwd)
	}
}

func TestBuildClaudeResumeCommandUsesManagedClaudeViaUserHomeFallbackWhenEnvMissing(t *testing.T) {
	prevUserHomeDirFn := userHomeDirFn
	t.Cleanup(func() { userHomeDirFn = prevUserHomeDirFn })

	home := t.TempDir()
	userHomeDirFn = func() (string, error) { return home, nil }
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	managedClaude := filepath.Join(home, ".local", "bin", "claude")
	if err := os.MkdirAll(filepath.Dir(managedClaude), 0o755); err != nil {
		t.Fatalf("mkdir managed Claude dir: %v", err)
	}
	if err := os.WriteFile(managedClaude, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write managed Claude: %v", err)
	}

	dir := t.TempDir()
	session := claudehistory.Session{SessionID: "abc"}
	project := claudehistory.Project{Path: dir}

	path, args, cwd, err := buildClaudeResumeCommand("", session, project, config.YoloModeOff)
	if err != nil {
		t.Fatalf("buildClaudeResumeCommand error: %v", err)
	}
	if path != managedClaude {
		t.Fatalf("expected managed Claude path %q, got %q", managedClaude, path)
	}
	want := []string{"--resume", "abc"}
	requireArgsEqual(t, args, want)
	if cwd != dir {
		t.Fatalf("expected cwd %s, got %s", dir, cwd)
	}
}

func TestBuildClaudeResumeCommandUsesRecoveredLauncherWhenHomeEnvMissing(t *testing.T) {
	prevUserHomeDirFn := userHomeDirFn
	t.Cleanup(func() { userHomeDirFn = prevUserHomeDirFn })
	userHomeDirFn = func() (string, error) { return "", os.ErrNotExist }

	cacheRoot := filepath.Join(t.TempDir(), "cache")
	hostID := "test-host"
	launcherPath := filepath.Join(cacheRoot, "claude-proxy", "hosts", hostID, "install-recovery", "claude")
	if err := os.MkdirAll(filepath.Dir(launcherPath), 0o755); err != nil {
		t.Fatalf("mkdir launcher dir: %v", err)
	}
	if err := os.WriteFile(launcherPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write launcher: %v", err)
	}

	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv("CLAUDE_PROXY_HOST_ID", hostID)

	dir := t.TempDir()
	session := claudehistory.Session{SessionID: "abc"}
	project := claudehistory.Project{Path: dir}

	path, args, cwd, err := buildClaudeResumeCommand("", session, project, config.YoloModeOff)
	if err != nil {
		t.Fatalf("buildClaudeResumeCommand error: %v", err)
	}
	if path != launcherPath {
		t.Fatalf("expected recovered launcher path %q, got %q", launcherPath, path)
	}
	want := []string{"--resume", "abc"}
	requireArgsEqual(t, args, want)
	if cwd != dir {
		t.Fatalf("expected cwd %s, got %s", dir, cwd)
	}
}

func TestBuildClaudeResumeCommandRejectsMissingSession(t *testing.T) {
	dir := t.TempDir()
	session := claudehistory.Session{}
	project := claudehistory.Project{Path: dir}

	_, _, _, err := buildClaudeResumeCommand("/bin/claude", session, project, config.YoloModeOff)
	if err == nil {
		t.Fatalf("expected error for missing session id")
	}
}

func TestBuildClaudeResumeCommandRejectsMissingCwd(t *testing.T) {
	session := claudehistory.Session{SessionID: "abc", ProjectPath: filepath.Join(t.TempDir(), "missing")}
	project := claudehistory.Project{}

	_, _, _, err := buildClaudeResumeCommand("/bin/claude", session, project, config.YoloModeOff)
	if err == nil {
		t.Fatalf("expected error for missing cwd")
	}
}

func TestNormalizeWorkingDirResolvesRelative(t *testing.T) {
	dir := t.TempDir()
	rel := filepath.Base(dir)
	abs := filepath.Dir(dir)
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(old) }()
	if err := os.Chdir(abs); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	got, err := normalizeWorkingDir(rel)
	if err != nil {
		t.Fatalf("normalizeWorkingDir error: %v", err)
	}
	if canonicalPath(t, got) != canonicalPath(t, dir) {
		t.Fatalf("expected %s, got %s", dir, got)
	}
}

func TestNormalizeWorkingDirRejectsMissing(t *testing.T) {
	_, err := normalizeWorkingDir(filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatalf("expected error for missing cwd")
	}
}

func TestNormalizeWorkingDirRejectsFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := normalizeWorkingDir(file); err == nil {
		t.Fatalf("expected error for non-directory cwd")
	}
}

func TestRunClaudeSessionSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script execution on windows")
	}
	claudePath := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write claude: %v", err)
	}
	store := newTempStore(t)
	root := &rootOptions{configPath: store.Path()}

	projectDir := t.TempDir()
	session := claudehistory.Session{SessionID: "sess-1", ProjectPath: projectDir}
	project := claudehistory.Project{Path: projectDir}

	if err := runClaudeSession(context.Background(), root, store, nil, nil, session, project, claudePath, "", false, config.YoloModeOff, io.Discard); err != nil {
		t.Fatalf("runClaudeSession error: %v", err)
	}
}

func TestRunClaudeNewSessionSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script execution on windows")
	}
	claudePath := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write claude: %v", err)
	}
	store := newTempStore(t)
	root := &rootOptions{configPath: store.Path()}

	projectDir := t.TempDir()
	if err := runClaudeNewSession(context.Background(), root, store, nil, nil, projectDir, claudePath, "", false, config.YoloModeOff, io.Discard); err != nil {
		t.Fatalf("runClaudeNewSession error: %v", err)
	}
}

func TestRunClaudeSessionRequiresProfileWhenProxyEnabled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script execution on windows")
	}
	claudePath := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write claude: %v", err)
	}
	store := newTempStore(t)
	root := &rootOptions{configPath: store.Path()}

	projectDir := t.TempDir()
	session := claudehistory.Session{SessionID: "sess-1", ProjectPath: projectDir}
	project := claudehistory.Project{Path: projectDir}

	if err := runClaudeSession(context.Background(), root, store, nil, nil, session, project, claudePath, "", true, config.YoloModeOff, io.Discard); err == nil {
		t.Fatalf("expected proxy mode error")
	}
}

func TestRunClaudeNewSessionRequiresProfileWhenProxyEnabled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script execution on windows")
	}
	claudePath := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write claude: %v", err)
	}
	store := newTempStore(t)
	root := &rootOptions{configPath: store.Path()}

	projectDir := t.TempDir()
	if err := runClaudeNewSession(context.Background(), root, store, nil, nil, projectDir, claudePath, "", true, config.YoloModeOff, io.Discard); err == nil {
		t.Fatalf("expected proxy mode error")
	}
}

func TestRunClaudeNewSessionUsesCwdDirect(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	dir := t.TempDir()
	outFile := filepath.Join(t.TempDir(), "pwd.txt")
	scriptPath := filepath.Join(t.TempDir(), "claude")
	script := fmt.Sprintf("#!/bin/sh\npwd > %q\n", outFile)
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}

	store, err := config.NewStore(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	err = runClaudeNewSession(
		context.Background(),
		&rootOptions{},
		store,
		nil,
		nil,
		dir,
		scriptPath,
		"",
		false,
		config.YoloModeOff,
		io.Discard,
	)
	if err != nil {
		t.Fatalf("runClaudeNewSession error: %v", err)
	}
	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if strings.TrimSpace(string(got)) != dir {
		if canonicalPath(t, strings.TrimSpace(string(got))) != canonicalPath(t, dir) {
			t.Fatalf("expected cwd %s, got %q", dir, strings.TrimSpace(string(got)))
		}
	}
}

func TestRunClaudeNewSessionAddsYoloArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	dir := t.TempDir()
	outFile := filepath.Join(t.TempDir(), "args.txt")
	scriptPath := filepath.Join(t.TempDir(), "claude")
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo \"Claude Code 1.2.3\"\n  exit 0\nfi\nif [ \"$1\" = \"--help\" ]; then\n  echo \"--permission-mode\"\n  exit 0\nfi\nprintf '%%s\\n' \"$@\" > %q\n", outFile)
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}

	store, err := config.NewStore(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	err = runClaudeNewSession(
		context.Background(),
		&rootOptions{},
		store,
		nil,
		nil,
		dir,
		scriptPath,
		"",
		false,
		config.YoloModeBypass,
		io.Discard,
	)
	if err != nil {
		t.Fatalf("runClaudeNewSession error: %v", err)
	}
	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	want := []string{"--permission-mode", "bypassPermissions"}
	if len(lines) < len(want) {
		t.Fatalf("expected args %v, got %v", want, lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("expected args %v, got %v", want, lines)
		}
	}
}

func TestRunClaudeSessionDisablesBypassWhenNoFlagsExposed(t *testing.T) {
	withExePatchTestHooks(t)

	claudePath := writeClaudeHelpStubWithOutput(t, "usage: claude")
	store := newTempStore(t)
	root := &rootOptions{configPath: store.Path()}
	projectDir := t.TempDir()
	session := claudehistory.Session{SessionID: "sess-1", ProjectPath: projectDir}
	project := claudehistory.Project{Path: projectDir}

	var patchCalls [][]string
	maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
		patchCalls = append(patchCalls, cloneArgs(cmdArgs))
		return nil, nil
	}
	runTargetWithFallbackWithOptionsFn = func(ctx context.Context, cmdArgs []string, proxyURL string, healthCheck func() error, patchOutcome *patchOutcome, fatalCh <-chan error, opts runTargetOptions) error {
		requireArgsEqual(t, cmdArgs, []string{claudePath, "--resume", "sess-1"})
		if opts.YoloEnabled {
			t.Fatalf("expected bypass mode to be disabled when no yolo flags are exposed")
		}
		return nil
	}

	if err := runClaudeSession(context.Background(), root, store, nil, nil, session, project, claudePath, "", false, config.YoloModeBypass, io.Discard); err != nil {
		t.Fatalf("runClaudeSession error: %v", err)
	}
	if len(patchCalls) != 1 {
		t.Fatalf("expected 1 patch call, got %d", len(patchCalls))
	}
	requireArgsEqual(t, patchCalls[0], []string{claudePath, "--resume", "sess-1"})

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got := resolveYoloMode(cfg); got != config.YoloModeOff {
		t.Fatalf("expected yolo mode off after bypass disable, got %s", got)
	}
}

func TestRunClaudeNewSessionDisablesBypassWhenNoFlagsExposed(t *testing.T) {
	withExePatchTestHooks(t)

	claudePath := writeClaudeHelpStubWithOutput(t, "usage: claude")
	store := newTempStore(t)
	root := &rootOptions{configPath: store.Path()}
	projectDir := t.TempDir()

	var patchCalls [][]string
	maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
		patchCalls = append(patchCalls, cloneArgs(cmdArgs))
		return nil, nil
	}
	runTargetWithFallbackWithOptionsFn = func(ctx context.Context, cmdArgs []string, proxyURL string, healthCheck func() error, patchOutcome *patchOutcome, fatalCh <-chan error, opts runTargetOptions) error {
		requireArgsEqual(t, cmdArgs, []string{claudePath})
		if opts.YoloEnabled {
			t.Fatalf("expected bypass mode to be disabled when no yolo flags are exposed")
		}
		return nil
	}

	if err := runClaudeNewSession(context.Background(), root, store, nil, nil, projectDir, claudePath, "", false, config.YoloModeBypass, io.Discard); err != nil {
		t.Fatalf("runClaudeNewSession error: %v", err)
	}
	if len(patchCalls) != 1 {
		t.Fatalf("expected 1 patch call, got %d", len(patchCalls))
	}
	requireArgsEqual(t, patchCalls[0], []string{claudePath})

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got := resolveYoloMode(cfg); got != config.YoloModeOff {
		t.Fatalf("expected yolo mode off after bypass disable, got %s", got)
	}
}

func TestRunClaudeNewSessionRejectsProxyWithoutProfile(t *testing.T) {
	dir := t.TempDir()
	store, err := config.NewStore(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	err = runClaudeNewSession(
		context.Background(),
		&rootOptions{},
		store,
		nil,
		nil,
		dir,
		"/bin/claude",
		"",
		true,
		config.YoloModeOff,
		io.Discard,
	)
	if err == nil {
		t.Fatalf("expected error when proxy enabled without profile")
	}
}

func TestRunClaudeSessionWiresRunnerCallbacks(t *testing.T) {
	t.Run("direct", func(t *testing.T) {
		withExePatchTestHooks(t)

		claudePath := writeClaudeHelpStub(t)
		store := newTempStore(t)
		root := &rootOptions{configPath: store.Path()}
		projectDir := t.TempDir()
		session := claudehistory.Session{SessionID: "sess-1", ProjectPath: projectDir}
		project := claudehistory.Project{Path: projectDir}

		var patchCalls [][]string
		maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
			patchCalls = append(patchCalls, cloneArgs(cmdArgs))
			return &patchOutcome{TargetPath: fmt.Sprintf("patch-%d", len(patchCalls))}, nil
		}
		runTargetWithFallbackWithOptionsFn = func(ctx context.Context, cmdArgs []string, proxyURL string, healthCheck func() error, patchOutcome *patchOutcome, fatalCh <-chan error, opts runTargetOptions) error {
			requireArgsEqual(t, cmdArgs, []string{claudePath, "--permission-mode", "bypassPermissions", "--resume", "sess-1"})
			if opts.Cwd != projectDir {
				t.Fatalf("expected cwd %s, got %s", projectDir, opts.Cwd)
			}
			if opts.UseProxy {
				t.Fatalf("expected direct runner")
			}
			if patchOutcome == nil || patchOutcome.TargetPath != "patch-1" {
				t.Fatalf("expected initial patch outcome, got %#v", patchOutcome)
			}
			if !opts.YoloEnabled || opts.OnYoloRetryPrepare == nil {
				t.Fatalf("expected yolo retry prepare callback")
			}
			outcome, err := opts.OnYoloRetryPrepare([]string{claudePath, "--resume", "sess-1"})
			if err != nil {
				return err
			}
			if outcome == nil || outcome.TargetPath != "patch-2" {
				t.Fatalf("expected retry patch outcome, got %#v", outcome)
			}
			return nil
		}

		if err := runClaudeSession(context.Background(), root, store, nil, nil, session, project, claudePath, "", false, config.YoloModeBypass, io.Discard); err != nil {
			t.Fatalf("runClaudeSession error: %v", err)
		}
		if len(patchCalls) != 2 {
			t.Fatalf("expected 2 patch calls, got %d", len(patchCalls))
		}
		requireArgsEqual(t, patchCalls[0], []string{claudePath, "--permission-mode", "bypassPermissions", "--resume", "sess-1"})
		requireArgsEqual(t, patchCalls[1], []string{claudePath, "--resume", "sess-1"})
	})

	t.Run("proxy", func(t *testing.T) {
		withExePatchTestHooks(t)

		claudePath := writeClaudeHelpStub(t)
		store := newTempStore(t)
		root := &rootOptions{configPath: store.Path()}
		projectDir := t.TempDir()
		session := claudehistory.Session{SessionID: "sess-1", ProjectPath: projectDir}
		project := claudehistory.Project{Path: projectDir}
		profile := config.Profile{ID: "p1", Name: "profile", Host: "host", Port: 22, User: "user"}

		var patchCalls [][]string
		maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
			patchCalls = append(patchCalls, cloneArgs(cmdArgs))
			return &patchOutcome{TargetPath: fmt.Sprintf("patch-%d", len(patchCalls))}, nil
		}
		runWithProfileOptionsFn = func(ctx context.Context, store *config.Store, gotProfile config.Profile, instances []config.Instance, cmdArgs []string, patchOutcome *patchOutcome, opts runTargetOptions) error {
			requireArgsEqual(t, cmdArgs, []string{claudePath, "--permission-mode", "bypassPermissions", "--resume", "sess-1"})
			if gotProfile.ID != profile.ID {
				t.Fatalf("expected profile %s, got %s", profile.ID, gotProfile.ID)
			}
			if opts.Cwd != projectDir {
				t.Fatalf("expected cwd %s, got %s", projectDir, opts.Cwd)
			}
			if !opts.UseProxy {
				t.Fatalf("expected proxy runner")
			}
			if patchOutcome == nil || patchOutcome.TargetPath != "patch-1" {
				t.Fatalf("expected initial patch outcome, got %#v", patchOutcome)
			}
			if !opts.YoloEnabled || opts.OnYoloRetryPrepare == nil {
				t.Fatalf("expected yolo retry prepare callback")
			}
			outcome, err := opts.OnYoloRetryPrepare([]string{claudePath, "--resume", "sess-1"})
			if err != nil {
				return err
			}
			if outcome == nil || outcome.TargetPath != "patch-2" {
				t.Fatalf("expected retry patch outcome, got %#v", outcome)
			}
			return nil
		}

		if err := runClaudeSession(context.Background(), root, store, &profile, nil, session, project, claudePath, "", true, config.YoloModeBypass, io.Discard); err != nil {
			t.Fatalf("runClaudeSession error: %v", err)
		}
		if len(patchCalls) != 2 {
			t.Fatalf("expected 2 patch calls, got %d", len(patchCalls))
		}
		requireArgsEqual(t, patchCalls[0], []string{claudePath, "--permission-mode", "bypassPermissions", "--resume", "sess-1"})
		requireArgsEqual(t, patchCalls[1], []string{claudePath, "--resume", "sess-1"})
	})
}

func TestRunClaudeSessionWiresRulesModePatchWithoutBypassArg(t *testing.T) {
	withExePatchTestHooks(t)

	claudePath := writeClaudeHelpStub(t)
	store := newTempStore(t)
	root := &rootOptions{
		configPath: store.Path(),
		exePatch: exePatchOptions{
			enabledFlag:    true,
			policySettings: true,
		},
	}
	projectDir := t.TempDir()
	session := claudehistory.Session{SessionID: "sess-1", ProjectPath: projectDir}
	project := claudehistory.Project{Path: projectDir}

	var patchCalls [][]string
	var patchOpts []exePatchOptions
	maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
		patchCalls = append(patchCalls, cloneArgs(cmdArgs))
		patchOpts = append(patchOpts, opts)
		return &patchOutcome{TargetPath: "patch-rules", BuiltInClaudePatchActive: true}, nil
	}
	runTargetWithFallbackWithOptionsFn = func(ctx context.Context, cmdArgs []string, proxyURL string, healthCheck func() error, patchOutcome *patchOutcome, fatalCh <-chan error, opts runTargetOptions) error {
		requireArgsEqual(t, cmdArgs, []string{claudePath, "--resume", "sess-1"})
		if opts.Cwd != projectDir {
			t.Fatalf("expected cwd %s, got %s", projectDir, opts.Cwd)
		}
		if opts.YoloEnabled {
			t.Fatalf("expected rules mode not to enable bypass fallback logic")
		}
		if patchOutcome == nil || patchOutcome.TargetPath != "patch-rules" {
			t.Fatalf("expected rules patch outcome, got %#v", patchOutcome)
		}
		return nil
	}

	if err := runClaudeSession(context.Background(), root, store, nil, nil, session, project, claudePath, "", false, config.YoloModeRules, io.Discard); err != nil {
		t.Fatalf("runClaudeSession error: %v", err)
	}
	if len(patchCalls) != 1 {
		t.Fatalf("expected 1 patch call, got %d", len(patchCalls))
	}
	requireArgsEqual(t, patchCalls[0], []string{claudePath, "--resume", "sess-1"})
	if len(patchOpts) != 1 || !patchOpts[0].allowBuiltInWithoutBypass {
		t.Fatalf("expected rules mode to force built-in patch without bypass, got %+v", patchOpts)
	}
}

func TestRunClaudeSessionRejectsRulesModeWithoutActiveBuiltInPatch(t *testing.T) {
	withExePatchTestHooks(t)

	claudePath := writeClaudeHelpStub(t)
	store := newTempStore(t)
	root := &rootOptions{
		configPath: store.Path(),
		exePatch: exePatchOptions{
			enabledFlag:    true,
			policySettings: true,
		},
	}
	projectDir := t.TempDir()
	session := claudehistory.Session{SessionID: "sess-1", ProjectPath: projectDir}
	project := claudehistory.Project{Path: projectDir}

	maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
		return &patchOutcome{TargetPath: claudePath}, nil
	}
	runTargetWithFallbackWithOptionsFn = func(ctx context.Context, cmdArgs []string, proxyURL string, healthCheck func() error, patchOutcome *patchOutcome, fatalCh <-chan error, opts runTargetOptions) error {
		t.Fatalf("unexpected launch attempt without active built-in rules patch")
		return nil
	}

	err := runClaudeSession(context.Background(), root, store, nil, nil, session, project, claudePath, "", false, config.YoloModeRules, io.Discard)
	if err == nil {
		t.Fatalf("expected rules mode active-patch validation error")
	}
	if !strings.Contains(err.Error(), "active built-in Claude patch") {
		t.Fatalf("expected missing active patch error, got %v", err)
	}
}

func TestRunClaudeNewSessionWiresRunnerCallbacks(t *testing.T) {
	t.Run("direct", func(t *testing.T) {
		withExePatchTestHooks(t)

		claudePath := writeClaudeHelpStub(t)
		store := newTempStore(t)
		root := &rootOptions{configPath: store.Path()}
		projectDir := t.TempDir()

		var patchCalls [][]string
		maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
			patchCalls = append(patchCalls, cloneArgs(cmdArgs))
			return &patchOutcome{TargetPath: fmt.Sprintf("patch-%d", len(patchCalls))}, nil
		}
		runTargetWithFallbackWithOptionsFn = func(ctx context.Context, cmdArgs []string, proxyURL string, healthCheck func() error, patchOutcome *patchOutcome, fatalCh <-chan error, opts runTargetOptions) error {
			requireArgsEqual(t, cmdArgs, []string{claudePath, "--permission-mode", "bypassPermissions"})
			if opts.Cwd != projectDir {
				t.Fatalf("expected cwd %s, got %s", projectDir, opts.Cwd)
			}
			if opts.UseProxy {
				t.Fatalf("expected direct runner")
			}
			if patchOutcome == nil || patchOutcome.TargetPath != "patch-1" {
				t.Fatalf("expected initial patch outcome, got %#v", patchOutcome)
			}
			if !opts.YoloEnabled || opts.OnYoloRetryPrepare == nil {
				t.Fatalf("expected yolo retry prepare callback")
			}
			outcome, err := opts.OnYoloRetryPrepare([]string{claudePath})
			if err != nil {
				return err
			}
			if outcome == nil || outcome.TargetPath != "patch-2" {
				t.Fatalf("expected retry patch outcome, got %#v", outcome)
			}
			return nil
		}

		if err := runClaudeNewSession(context.Background(), root, store, nil, nil, projectDir, claudePath, "", false, config.YoloModeBypass, io.Discard); err != nil {
			t.Fatalf("runClaudeNewSession error: %v", err)
		}
		if len(patchCalls) != 2 {
			t.Fatalf("expected 2 patch calls, got %d", len(patchCalls))
		}
		requireArgsEqual(t, patchCalls[0], []string{claudePath, "--permission-mode", "bypassPermissions"})
		requireArgsEqual(t, patchCalls[1], []string{claudePath})
	})

	t.Run("proxy", func(t *testing.T) {
		withExePatchTestHooks(t)

		claudePath := writeClaudeHelpStub(t)
		store := newTempStore(t)
		root := &rootOptions{configPath: store.Path()}
		projectDir := t.TempDir()
		profile := config.Profile{ID: "p1", Name: "profile", Host: "host", Port: 22, User: "user"}

		var patchCalls [][]string
		maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
			patchCalls = append(patchCalls, cloneArgs(cmdArgs))
			return &patchOutcome{TargetPath: fmt.Sprintf("patch-%d", len(patchCalls))}, nil
		}
		runWithProfileOptionsFn = func(ctx context.Context, store *config.Store, gotProfile config.Profile, instances []config.Instance, cmdArgs []string, patchOutcome *patchOutcome, opts runTargetOptions) error {
			requireArgsEqual(t, cmdArgs, []string{claudePath, "--permission-mode", "bypassPermissions"})
			if gotProfile.ID != profile.ID {
				t.Fatalf("expected profile %s, got %s", profile.ID, gotProfile.ID)
			}
			if opts.Cwd != projectDir {
				t.Fatalf("expected cwd %s, got %s", projectDir, opts.Cwd)
			}
			if !opts.UseProxy {
				t.Fatalf("expected proxy runner")
			}
			if patchOutcome == nil || patchOutcome.TargetPath != "patch-1" {
				t.Fatalf("expected initial patch outcome, got %#v", patchOutcome)
			}
			if !opts.YoloEnabled || opts.OnYoloRetryPrepare == nil {
				t.Fatalf("expected yolo retry prepare callback")
			}
			outcome, err := opts.OnYoloRetryPrepare([]string{claudePath})
			if err != nil {
				return err
			}
			if outcome == nil || outcome.TargetPath != "patch-2" {
				t.Fatalf("expected retry patch outcome, got %#v", outcome)
			}
			return nil
		}

		if err := runClaudeNewSession(context.Background(), root, store, &profile, nil, projectDir, claudePath, "", true, config.YoloModeBypass, io.Discard); err != nil {
			t.Fatalf("runClaudeNewSession error: %v", err)
		}
		if len(patchCalls) != 2 {
			t.Fatalf("expected 2 patch calls, got %d", len(patchCalls))
		}
		requireArgsEqual(t, patchCalls[0], []string{claudePath, "--permission-mode", "bypassPermissions"})
		requireArgsEqual(t, patchCalls[1], []string{claudePath})
	})
}

func TestRunClaudeNewSessionWiresRulesModePatchWithoutBypassArg(t *testing.T) {
	withExePatchTestHooks(t)

	claudePath := writeClaudeHelpStub(t)
	store := newTempStore(t)
	root := &rootOptions{
		configPath: store.Path(),
		exePatch: exePatchOptions{
			enabledFlag:    true,
			policySettings: true,
		},
	}
	projectDir := t.TempDir()

	var patchCalls [][]string
	var patchOpts []exePatchOptions
	maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
		patchCalls = append(patchCalls, cloneArgs(cmdArgs))
		patchOpts = append(patchOpts, opts)
		return &patchOutcome{TargetPath: "patch-rules", BuiltInClaudePatchActive: true}, nil
	}
	runTargetWithFallbackWithOptionsFn = func(ctx context.Context, cmdArgs []string, proxyURL string, healthCheck func() error, patchOutcome *patchOutcome, fatalCh <-chan error, opts runTargetOptions) error {
		requireArgsEqual(t, cmdArgs, []string{claudePath})
		if opts.YoloEnabled {
			t.Fatalf("expected rules mode not to enable bypass fallback logic")
		}
		if patchOutcome == nil || patchOutcome.TargetPath != "patch-rules" {
			t.Fatalf("expected rules patch outcome, got %#v", patchOutcome)
		}
		return nil
	}

	if err := runClaudeNewSession(context.Background(), root, store, nil, nil, projectDir, claudePath, "", false, config.YoloModeRules, io.Discard); err != nil {
		t.Fatalf("runClaudeNewSession error: %v", err)
	}
	if len(patchCalls) != 1 {
		t.Fatalf("expected 1 patch call, got %d", len(patchCalls))
	}
	requireArgsEqual(t, patchCalls[0], []string{claudePath})
	if len(patchOpts) != 1 || !patchOpts[0].allowBuiltInWithoutBypass {
		t.Fatalf("expected rules mode to force built-in patch without bypass, got %+v", patchOpts)
	}
}

func TestRunClaudeNewSessionRejectsRulesModeWhenBuiltinPatchDisabled(t *testing.T) {
	withExePatchTestHooks(t)

	claudePath := writeClaudeHelpStub(t)
	store := newTempStore(t)
	root := &rootOptions{
		configPath: store.Path(),
		exePatch: exePatchOptions{
			policySettings: true,
		},
	}
	projectDir := t.TempDir()

	maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
		t.Fatalf("unexpected patch attempt in invalid rules mode")
		return nil, nil
	}
	runTargetWithFallbackWithOptionsFn = func(ctx context.Context, cmdArgs []string, proxyURL string, healthCheck func() error, patchOutcome *patchOutcome, fatalCh <-chan error, opts runTargetOptions) error {
		t.Fatalf("unexpected launch attempt in invalid rules mode")
		return nil
	}

	err := runClaudeNewSession(context.Background(), root, store, nil, nil, projectDir, claudePath, "", false, config.YoloModeRules, io.Discard)
	if err == nil {
		t.Fatalf("expected rules mode validation error")
	}
	if !strings.Contains(err.Error(), "--exe-patch-enabled") {
		t.Fatalf("expected exe patch disabled error, got %v", err)
	}
}

func TestRunClaudeNewSessionRejectsRulesModeForNonClaudePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script execution on windows")
	}
	withExePatchTestHooks(t)

	wrapperPath := filepath.Join(t.TempDir(), "claude-wrapper")
	if err := os.WriteFile(wrapperPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}
	store := newTempStore(t)
	root := &rootOptions{
		configPath: store.Path(),
		exePatch: exePatchOptions{
			enabledFlag:    true,
			policySettings: true,
		},
	}
	projectDir := t.TempDir()

	maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
		t.Fatalf("unexpected patch attempt for non-claude rules mode")
		return nil, nil
	}
	runTargetWithFallbackWithOptionsFn = func(ctx context.Context, cmdArgs []string, proxyURL string, healthCheck func() error, patchOutcome *patchOutcome, fatalCh <-chan error, opts runTargetOptions) error {
		t.Fatalf("unexpected launch attempt for non-claude rules mode")
		return nil
	}

	err := runClaudeNewSession(context.Background(), root, store, nil, nil, projectDir, wrapperPath, "", false, config.YoloModeRules, io.Discard)
	if err == nil {
		t.Fatalf("expected rules mode validation error")
	}
	if !strings.Contains(err.Error(), "wrappers or renamed binaries") {
		t.Fatalf("expected non-claude rules mode error, got %v", err)
	}
}

func TestRunClaudeNewSessionRejectsRulesModeWithoutActiveBuiltInPatch(t *testing.T) {
	withExePatchTestHooks(t)

	claudePath := writeClaudeHelpStub(t)
	store := newTempStore(t)
	root := &rootOptions{
		configPath: store.Path(),
		exePatch: exePatchOptions{
			enabledFlag:    true,
			policySettings: true,
		},
	}
	projectDir := t.TempDir()

	maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
		return &patchOutcome{TargetPath: claudePath}, nil
	}
	runTargetWithFallbackWithOptionsFn = func(ctx context.Context, cmdArgs []string, proxyURL string, healthCheck func() error, patchOutcome *patchOutcome, fatalCh <-chan error, opts runTargetOptions) error {
		t.Fatalf("unexpected launch attempt without active built-in rules patch")
		return nil
	}

	err := runClaudeNewSession(context.Background(), root, store, nil, nil, projectDir, claudePath, "", false, config.YoloModeRules, io.Discard)
	if err == nil {
		t.Fatalf("expected rules mode active-patch validation error")
	}
	if !strings.Contains(err.Error(), "active built-in Claude patch") {
		t.Fatalf("expected missing active patch error, got %v", err)
	}
}

func canonicalPath(t *testing.T, path string) string {
	t.Helper()
	if path == "" {
		return path
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	return filepath.Clean(path)
}
