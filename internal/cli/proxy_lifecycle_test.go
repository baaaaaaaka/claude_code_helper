package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
	"github.com/baaaaaaaka/claude_code_helper/internal/manager"
	"github.com/baaaaaaaka/claude_code_helper/internal/stack"
)

type fakeProxyTicker struct {
	ch chan time.Time
}

func (t *fakeProxyTicker) Chan() <-chan time.Time { return t.ch }
func (t *fakeProxyTicker) Stop()                  {}

func withProxyTestHooks(t *testing.T) {
	t.Helper()
	prevStore := newProxyStore
	prevID := newProxyInstanceID
	prevExe := proxyExecutable
	prevLauncher := proxyDaemonLauncher
	prevRecord := recordProxyInstance
	prevRemove := removeProxyInstance
	prevHeartbeat := heartbeatProxyInstance
	prevTicker := newProxyTicker
	prevStackStart := stackStart
	t.Cleanup(func() {
		newProxyStore = prevStore
		newProxyInstanceID = prevID
		proxyExecutable = prevExe
		proxyDaemonLauncher = prevLauncher
		recordProxyInstance = prevRecord
		removeProxyInstance = prevRemove
		heartbeatProxyInstance = prevHeartbeat
		newProxyTicker = prevTicker
		stackStart = prevStackStart
	})
}

func TestProxyStartCmdBackgroundSuccess(t *testing.T) {
	withProxyTestHooks(t)
	store := newTempStore(t)
	cfg := config.Config{
		Version: config.CurrentVersion,
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

	newProxyInstanceID = func() (string, error) { return "inst-fixed", nil }
	proxyExecutable = func() (string, error) { return "/tmp/claude-proxy", nil }

	var gotExe string
	var gotArgs []string
	var gotLogPath string
	proxyDaemonLauncher = func(exe string, args []string, logPath string) (int, error) {
		gotExe = exe
		gotArgs = append([]string(nil), args...)
		gotLogPath = logPath
		return 4242, nil
	}

	cmd := newProxyStartCmd(&rootOptions{configPath: store.Path()})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"p1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if gotExe != "/tmp/claude-proxy" {
		t.Fatalf("unexpected executable: %s", gotExe)
	}
	if strings.Join(gotArgs, " ") != "--config "+store.Path()+" proxy daemon --instance-id inst-fixed" {
		t.Fatalf("unexpected daemon args: %v", gotArgs)
	}
	wantLogPath := filepath.Join(filepath.Dir(store.Path()), "instances", "inst-fixed.log")
	if gotLogPath != wantLogPath {
		t.Fatalf("expected log path %q, got %q", wantLogPath, gotLogPath)
	}
	if !strings.Contains(out.String(), "Started instance inst-fixed (pid 4242)") {
		t.Fatalf("unexpected output: %s", out.String())
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(loaded.Instances) != 1 {
		t.Fatalf("expected one instance, got %d", len(loaded.Instances))
	}
	inst := loaded.Instances[0]
	if inst.ID != "inst-fixed" || inst.DaemonPID != 4242 || inst.ProfileID != "p1" || inst.Kind != config.InstanceKindDaemon {
		t.Fatalf("unexpected instance: %#v", inst)
	}
}

func TestProxyStartCmdForegroundSuccess(t *testing.T) {
	withProxyTestHooks(t)
	store := newTempStore(t)
	cfg := config.Config{
		Version: config.CurrentVersion,
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

	ctx, cancel := context.WithCancel(context.Background())
	ticker := &fakeProxyTicker{ch: make(chan time.Time, 1)}
	newProxyTicker = func(time.Duration) proxyTicker { return ticker }
	stackStart = func(profile config.Profile, instanceID string, opts stack.Options) (*stack.Stack, error) {
		return stack.NewStackForTest(12345, 23456), nil
	}
	heartbeatProxyInstance = func(store *config.Store, instanceID string, now time.Time) error {
		cancel()
		return manager.Heartbeat(store, instanceID, now)
	}

	cmd := newProxyStartCmd(&rootOptions{configPath: store.Path()})
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{"--foreground", "p1"})

	go func() {
		ticker.ch <- time.Now()
	}()

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(loaded.Instances) != 0 {
		t.Fatalf("expected foreground daemon to clean up instance, got %#v", loaded.Instances)
	}
}

func TestRunProxyDaemonRemovesInstanceOnFatal(t *testing.T) {
	withProxyTestHooks(t)
	store := newTempStore(t)
	cfg := config.Config{
		Version: config.CurrentVersion,
		Profiles: []config.Profile{{
			ID:   "p1",
			Name: "profile",
			Host: "host",
			Port: 22,
			User: "user",
		}},
		Instances: []config.Instance{{
			ID:        "inst-1",
			ProfileID: "p1",
		}},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	fatalCh := make(chan error, 1)
	stackStart = func(profile config.Profile, instanceID string, opts stack.Options) (*stack.Stack, error) {
		return stack.NewStackWithFatalForTest(12345, 23456, fatalCh), nil
	}

	go func() {
		fatalCh <- errors.New("tunnel died")
	}()

	err := runProxyDaemon(context.Background(), store, "inst-1")
	if err == nil || err.Error() != "tunnel died" {
		t.Fatalf("expected fatal tunnel error, got %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(loaded.Instances) != 0 {
		t.Fatalf("expected instance to be removed on fatal, got %#v", loaded.Instances)
	}
}

func TestRunProxyDaemonHeartbeatsAndUsesConfiguredHTTPPort(t *testing.T) {
	withProxyTestHooks(t)
	store := newTempStore(t)
	startedAt := time.Now().Add(-time.Hour)
	cfg := config.Config{
		Version: config.CurrentVersion,
		Profiles: []config.Profile{{
			ID:   "p1",
			Name: "profile",
			Host: "host",
			Port: 22,
			User: "user",
		}},
		Instances: []config.Instance{{
			ID:        "inst-1",
			ProfileID: "p1",
			HTTPPort:  18080,
			StartedAt: startedAt,
		}},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	ticker := &fakeProxyTicker{ch: make(chan time.Time, 1)}
	newProxyTicker = func(time.Duration) proxyTicker { return ticker }

	var gotOpts stack.Options
	stackStart = func(profile config.Profile, instanceID string, opts stack.Options) (*stack.Stack, error) {
		gotOpts = opts
		return stack.NewStackForTest(18080, 23456), nil
	}

	heartbeatCalls := 0
	heartbeatProxyInstance = func(store *config.Store, instanceID string, now time.Time) error {
		heartbeatCalls++
		cancel()
		return manager.Heartbeat(store, instanceID, now)
	}

	done := make(chan error, 1)
	go func() {
		done <- runProxyDaemon(ctx, store, "inst-1")
	}()

	ticker.ch <- time.Now()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("unexpected runProxyDaemon error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for runProxyDaemon to exit")
	}

	if gotOpts.HTTPListenAddr != "127.0.0.1:18080" {
		t.Fatalf("expected fixed HTTP listen addr, got %q", gotOpts.HTTPListenAddr)
	}
	if gotOpts.SocksPort != 0 {
		t.Fatalf("expected empty socks port to reuse config default, got %d", gotOpts.SocksPort)
	}
	if heartbeatCalls != 1 {
		t.Fatalf("expected one heartbeat, got %d", heartbeatCalls)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(loaded.Instances) != 0 {
		t.Fatalf("expected instance to be removed on shutdown, got %#v", loaded.Instances)
	}
}

func TestLaunchProxyDaemonProcessWritesLogs(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "instances", "inst-1.log")

	unix := "#!/bin/sh\necho daemon-stdout\necho daemon-stderr >&2\nexit 0\n"
	win := "@echo off\r\necho daemon-stdout\r\necho daemon-stderr 1>&2\r\nexit /b 0\r\n"
	writeStub(t, dir, "proxy-launcher", unix, win)

	exePath := filepath.Join(dir, "proxy-launcher")
	if runtime.GOOS == "windows" {
		exePath += ".cmd"
	}

	pid, err := launchProxyDaemonProcess(exePath, nil, logPath)
	if err != nil {
		t.Fatalf("launchProxyDaemonProcess error: %v", err)
	}
	if pid <= 0 {
		t.Fatalf("expected child pid, got %d", pid)
	}

	var data []byte
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		data, err = os.ReadFile(logPath)
		if err == nil && strings.Contains(string(data), "daemon-stdout") && strings.Contains(string(data), "daemon-stderr") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected log file to contain child output, got %q err=%v", string(data), err)
}
