package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
	"github.com/spf13/cobra"
)

func writeRunEnvStub(t *testing.T, outFile string) string {
	t.Helper()

	dir := t.TempDir()
	unix := "#!/bin/sh\nprintf \"%s\" \"$HTTP_PROXY\" > \"$OUT_FILE\"\n"
	win := "@echo off\r\n<nul set /p =%HTTP_PROXY% > \"%OUT_FILE%\"\r\nexit /b 0\r\n"
	writeStub(t, dir, "print-proxy-env", unix, win)

	t.Setenv("OUT_FILE", outFile)

	path := filepath.Join(dir, "print-proxy-env")
	if runtime.GOOS == "windows" {
		path += ".cmd"
	}
	return path
}

func startRunHealthServer(t *testing.T, instanceID string) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/_claude_proxy/health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"ok":true,"instanceId":%q}`, instanceID)
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = ln.Close()
	})

	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected addr type %T", ln.Addr())
	}
	return addr.Port
}

func setRunTestStdin(t *testing.T, input string) {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	prevStdin := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() { os.Stdin = prevStdin })

	if _, err := writer.Write([]byte(input)); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	_ = writer.Close()
}

func TestSelectProfile(t *testing.T) {
	cfg := config.Config{
		Profiles: []config.Profile{
			{ID: "one", Name: "first"},
			{ID: "two", Name: "second"},
		},
	}

	if _, err := selectProfile(cfg, "one"); err != nil {
		t.Fatalf("expected profile by ID, got error %v", err)
	}
	if _, err := selectProfile(cfg, "second"); err != nil {
		t.Fatalf("expected profile by name, got error %v", err)
	}
	if _, err := selectProfile(cfg, "missing"); err == nil {
		t.Fatalf("expected missing profile error")
	}
	if _, err := selectProfile(cfg, ""); err == nil {
		t.Fatalf("expected error when multiple profiles exist without ref")
	}
}

func TestRunTargetSupervisedSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "ok.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}

	if err := runTargetSupervised(context.Background(), []string{script}, "", nil, nil, nil); err != nil {
		t.Fatalf("runTargetSupervised error: %v", err)
	}
}

func TestRunTargetOnceWithOptionsNoProxyKeepsEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	dir := t.TempDir()
	outFile := filepath.Join(dir, "env.txt")
	script := filepath.Join(dir, "print.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf \"%s\" \"$HTTP_PROXY\" > \"$OUT_FILE\"\n"), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}

	t.Setenv("HTTP_PROXY", "http://example.com")
	opts := runTargetOptions{
		ExtraEnv: []string{"OUT_FILE=" + outFile},
		UseProxy: false,
	}

	if err := runTargetOnceWithOptions(context.Background(), []string{script}, "http://127.0.0.1:9999", nil, nil, &bytes.Buffer{}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("runTargetOnceWithOptions error: %v", err)
	}
	content, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	if got := string(content); got != "http://example.com" {
		t.Fatalf("expected HTTP_PROXY preserved, got %q", got)
	}
}

func TestTerminateProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip process signal test on windows")
	}
	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	if err := terminateProcess(cmd.Process, 100*time.Millisecond); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("terminateProcess error: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected process to exit")
	}
}

func TestRunTargetWithFallbackDisablesYolo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "yolo.sh")
	content := "#!/bin/sh\nfor arg in \"$@\"; do\n  if [ \"$arg\" = \"--permission-mode\" ]; then\n    echo \"unknown flag: --permission-mode\" >&2\n    exit 2\n  fi\n done\nexit 0\n"
	if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}

	disabled := false
	opts := runTargetOptions{
		UseProxy:    false,
		PreserveTTY: false,
		YoloEnabled: true,
		OnYoloFallback: func() error {
			disabled = true
			return nil
		},
	}
	cmdArgs := []string{script, "--permission-mode", "bypassPermissions"}
	if err := runTargetWithFallbackWithOptions(context.Background(), cmdArgs, "", nil, nil, nil, opts); err != nil {
		t.Fatalf("runTargetWithFallbackWithOptions error: %v", err)
	}
	if !disabled {
		t.Fatalf("expected yolo to be disabled on failure")
	}
}

func TestRunTargetWithFallbackUsesLaunchArgsPrefix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	dir := t.TempDir()
	outFile := filepath.Join(dir, "args.txt")
	sourceScript := filepath.Join(dir, "source.sh")
	if err := os.WriteFile(sourceScript, []byte("#!/bin/sh\nexit 9\n"), 0o700); err != nil {
		t.Fatalf("write source script: %v", err)
	}
	launchScript := filepath.Join(dir, "launch.sh")
	content := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + outFile + "\"\n"
	if err := os.WriteFile(launchScript, []byte(content), 0o700); err != nil {
		t.Fatalf("write launch script: %v", err)
	}

	outcome := &patchOutcome{
		LaunchArgsPrefix: []string{launchScript, "--shim"},
	}
	if err := runTargetWithFallbackWithOptions(context.Background(), []string{sourceScript, "--resume", "abc"}, "", nil, outcome, nil, runTargetOptions{UseProxy: false}); err != nil {
		t.Fatalf("runTargetWithFallbackWithOptions error: %v", err)
	}
	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}
	if string(got) != "--shim\n--resume\nabc\n" {
		t.Fatalf("unexpected launch args: %q", string(got))
	}
}

func TestLimitedBufferWrite(t *testing.T) {
	buf := &limitedBuffer{max: 5}
	if _, err := buf.Write([]byte("abc")); err != nil {
		t.Fatalf("write error: %v", err)
	}
	if got := buf.String(); got != "abc" {
		t.Fatalf("expected %q, got %q", "abc", got)
	}
	if _, err := buf.Write([]byte("def")); err != nil {
		t.Fatalf("write error: %v", err)
	}
	if got := buf.String(); got != "bcdef" {
		t.Fatalf("expected %q, got %q", "bcdef", got)
	}

	buf = &limitedBuffer{max: 5}
	_, _ = buf.Write([]byte("0123456789"))
	if got := buf.String(); got != "56789" {
		t.Fatalf("expected %q, got %q", "56789", got)
	}

	buf = &limitedBuffer{max: 0}
	_, _ = buf.Write([]byte("abc"))
	if got := buf.String(); got != "" {
		t.Fatalf("expected empty buffer, got %q", got)
	}
}

func TestRunLikeRejectsMultipleProfiles(t *testing.T) {
	cmd := &cobra.Command{}
	if err := cmd.Flags().Parse([]string{"a", "b"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	root := &rootOptions{}
	if err := runLike(cmd, root, false); err == nil {
		t.Fatalf("expected error for multiple profile args")
	}
}

func TestRunLikePropagatesPatchError(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Parse([]string{"--", "echo"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	root := &rootOptions{
		exePatch: exePatchOptions{
			enabledFlag: true,
			regex1:      "(",
			regex2:      []string{"a"},
			regex3:      []string{"b"},
			replace:     []string{"c"},
		},
	}
	if err := runLike(cmd, root, false); err == nil {
		t.Fatalf("expected runLike to return patch error")
	}
}

func TestRunLikeReleasesPatchPrepMemoryBeforeProfileSelection(t *testing.T) {
	withExePatchTestHooks(t)

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Parse([]string{"--", "echo"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	dir := t.TempDir()
	releaseCalls := 0
	releasePatchPrepMemoryFn = func(cmdArgs []string, opts exePatchOptions, outcome *patchOutcome) {
		releaseCalls++
		if len(cmdArgs) != 1 || cmdArgs[0] != "echo" {
			t.Fatalf("unexpected command args: %#v", cmdArgs)
		}
		if outcome == nil || outcome.TargetPath != filepath.Join(dir, "claude") {
			t.Fatalf("unexpected patch outcome: %#v", outcome)
		}
	}
	maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
		return &patchOutcome{TargetPath: filepath.Join(dir, "claude")}, nil
	}

	root := &rootOptions{
		configPath: filepath.Join(dir, "config.json"),
		exePatch: exePatchOptions{
			enabledFlag:    true,
			policySettings: true,
		},
	}
	store, err := config.NewStore(root.configPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Save(config.Config{
		Version: config.CurrentVersion,
		Profiles: []config.Profile{
			{ID: "p1", Name: "one", Host: "proxy-1", Port: 22, User: "alice"},
			{ID: "p2", Name: "two", Host: "proxy-2", Port: 22, User: "bob"},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	if err := runLike(cmd, root, false); err == nil {
		t.Fatalf("expected runLike to fail without explicit profile when multiple profiles exist")
	}
	if releaseCalls != 1 {
		t.Fatalf("expected one release call, got %d", releaseCalls)
	}
}

func TestRunLikeHonorsDisabledProxyPreference(t *testing.T) {
	store := newTempStore(t)
	disabled := false
	if err := store.Save(config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &disabled,
		Profiles: []config.Profile{
			{ID: "p1", Name: "one", Host: "proxy-1", Port: 22, User: "alice"},
			{ID: "p2", Name: "two", Host: "proxy-2", Port: 22, User: "bob"},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	outFile := filepath.Join(t.TempDir(), "env.txt")
	cmdPath := writeRunEnvStub(t, outFile)
	t.Setenv("HTTP_PROXY", "http://example.com")

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Parse([]string{"--", cmdPath}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	root := &rootOptions{configPath: store.Path()}
	if err := runLike(cmd, root, false); err != nil {
		t.Fatalf("runLike error: %v", err)
	}

	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	if got := strings.TrimSpace(string(got)); got != "http://example.com" {
		t.Fatalf("expected direct run to preserve HTTP_PROXY, got %q", got)
	}
}

func TestRunLikeUsesProxyWhenPreferenceEnabled(t *testing.T) {
	store := newTempStore(t)
	enabled := true
	port := startRunHealthServer(t, "inst-1")
	if err := store.Save(config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &enabled,
		Profiles: []config.Profile{
			{ID: "p1", Name: "one", Host: "proxy-1", Port: 22, User: "alice"},
		},
		Instances: []config.Instance{
			{
				ID:         "inst-1",
				ProfileID:  "p1",
				Kind:       config.InstanceKindDaemon,
				HTTPPort:   port,
				DaemonPID:  os.Getpid(),
				LastSeenAt: time.Now(),
			},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	outFile := filepath.Join(t.TempDir(), "env.txt")
	cmdPath := writeRunEnvStub(t, outFile)
	t.Setenv("HTTP_PROXY", "http://example.com")

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Parse([]string{"--", cmdPath}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	root := &rootOptions{configPath: store.Path()}
	if err := runLike(cmd, root, false); err != nil {
		t.Fatalf("runLike error: %v", err)
	}

	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	want := fmt.Sprintf("http://127.0.0.1:%d", port)
	if got := strings.TrimSpace(string(got)); got != want {
		t.Fatalf("expected proxy run to set HTTP_PROXY=%q, got %q", want, got)
	}
}

func TestRunLikeDirectPromptPersistsDisabledPreference(t *testing.T) {
	store := newTempStore(t)
	setRunTestStdin(t, "n\n")

	outFile := filepath.Join(t.TempDir(), "env.txt")
	cmdPath := writeRunEnvStub(t, outFile)
	t.Setenv("HTTP_PROXY", "http://example.com")

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Parse([]string{"--", cmdPath}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	root := &rootOptions{configPath: store.Path()}
	if err := runLike(cmd, root, true); err != nil {
		t.Fatalf("runLike error: %v", err)
	}

	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	if got := strings.TrimSpace(string(got)); got != "http://example.com" {
		t.Fatalf("expected direct run to preserve HTTP_PROXY, got %q", got)
	}

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.ProxyEnabled == nil || *cfg.ProxyEnabled {
		t.Fatalf("expected ProxyEnabled=false to be persisted, got %#v", cfg.ProxyEnabled)
	}
	if len(cfg.Profiles) != 0 {
		t.Fatalf("expected direct choice not to auto-create profiles, got %#v", cfg.Profiles)
	}
}

func TestRunLikeProxyPromptFailureDoesNotPersistPreference(t *testing.T) {
	store := newTempStore(t)
	setRunTestStdin(t, "y\n")

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Parse([]string{"--", "echo"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	root := &rootOptions{configPath: store.Path()}
	if err := runLike(cmd, root, false); err == nil {
		t.Fatalf("expected runLike to fail when proxy is chosen but no profile can be resolved")
	}

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.ProxyEnabled != nil {
		t.Fatalf("expected proxy preference not to persist on setup failure, got %#v", cfg.ProxyEnabled)
	}
}

func TestRunLikeInfersProxyFromProfilesAndPersistsPreference(t *testing.T) {
	store := newTempStore(t)
	port := startRunHealthServer(t, "inst-1")
	if err := store.Save(config.Config{
		Version: config.CurrentVersion,
		Profiles: []config.Profile{
			{ID: "p1", Name: "one", Host: "proxy-1", Port: 22, User: "alice"},
		},
		Instances: []config.Instance{
			{
				ID:         "inst-1",
				ProfileID:  "p1",
				Kind:       config.InstanceKindDaemon,
				HTTPPort:   port,
				DaemonPID:  os.Getpid(),
				LastSeenAt: time.Now(),
			},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	outFile := filepath.Join(t.TempDir(), "env.txt")
	cmdPath := writeRunEnvStub(t, outFile)
	t.Setenv("HTTP_PROXY", "http://example.com")

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Parse([]string{"--", cmdPath}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	root := &rootOptions{configPath: store.Path()}
	if err := runLike(cmd, root, false); err != nil {
		t.Fatalf("runLike error: %v", err)
	}

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.ProxyEnabled == nil || !*cfg.ProxyEnabled {
		t.Fatalf("expected inferred proxy preference to be persisted as true, got %#v", cfg.ProxyEnabled)
	}
}

func TestRunLikeExplicitProfileForcesProxy(t *testing.T) {
	store := newTempStore(t)
	disabled := false
	port := startRunHealthServer(t, "inst-1")
	if err := store.Save(config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &disabled,
		Profiles: []config.Profile{
			{ID: "p1", Name: "one", Host: "proxy-1", Port: 22, User: "alice"},
		},
		Instances: []config.Instance{
			{
				ID:         "inst-1",
				ProfileID:  "p1",
				Kind:       config.InstanceKindDaemon,
				HTTPPort:   port,
				DaemonPID:  os.Getpid(),
				LastSeenAt: time.Now(),
			},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	outFile := filepath.Join(t.TempDir(), "env.txt")
	cmdPath := writeRunEnvStub(t, outFile)
	t.Setenv("HTTP_PROXY", "http://example.com")

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Parse([]string{"one", "--", cmdPath}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	root := &rootOptions{configPath: store.Path()}
	if err := runLike(cmd, root, false); err != nil {
		t.Fatalf("runLike error: %v", err)
	}

	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	want := fmt.Sprintf("http://127.0.0.1:%d", port)
	if got := strings.TrimSpace(string(got)); got != want {
		t.Fatalf("expected explicit profile to force proxy HTTP_PROXY=%q, got %q", want, got)
	}

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.ProxyEnabled == nil || *cfg.ProxyEnabled {
		t.Fatalf("expected explicit profile override not to mutate saved ProxyEnabled=false, got %#v", cfg.ProxyEnabled)
	}
}
