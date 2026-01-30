package cli

import (
	"bytes"
	"context"
	"errors"
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
	"github.com/baaaaaaaka/claude_code_helper/internal/manager"
	"github.com/baaaaaaaka/claude_code_helper/internal/stack"
)

func requireShell(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("skip shell-based test on windows")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	return shell
}

func TestRunTargetOnceSuccess(t *testing.T) {
	shell := requireShell(t)
	if err := runTargetOnce(context.Background(), []string{shell, "-c", "exit 0"}, "http://127.0.0.1:9999", nil, nil, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("runTargetOnce error: %v", err)
	}
}

func TestRunWithExistingInstance(t *testing.T) {
	shell := requireShell(t)
	inst := config.Instance{
		ID:        "inst-1",
		ProfileID: "p1",
		HTTPPort:  0,
		DaemonPID: os.Getpid(),
	}
	if err := runWithExistingInstance(context.Background(), manager.HealthClient{Timeout: time.Second}, inst, []string{shell, "-c", "exit 0"}, nil); err != nil {
		t.Fatalf("runWithExistingInstance error: %v", err)
	}
}

func TestRunWithProfileUsesExistingInstance(t *testing.T) {
	shell := requireShell(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/_claude_proxy/health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"instanceId":"inst-1"}`))
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = ln.Close()
	})
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port := 0
	if p, err := net.ResolveTCPAddr("tcp", "127.0.0.1:"+portStr); err == nil {
		port = p.Port
	}
	if port == 0 {
		t.Fatalf("failed to resolve port")
	}

	store := newTempStore(t)
	profile := config.Profile{ID: "p1", Name: "profile", Host: "host", Port: 22, User: "user"}
	instances := []config.Instance{{
		ID:         "inst-1",
		ProfileID:  profile.ID,
		HTTPPort:   port,
		DaemonPID:  os.Getpid(),
		LastSeenAt: time.Now(),
	}}
	if err := runWithProfile(context.Background(), store, profile, instances, []string{shell, "-c", "exit 0"}, nil); err != nil {
		t.Fatalf("runWithProfile error: %v", err)
	}
}

func TestRunWithNewStackFailsWhenSSHMissing(t *testing.T) {
	shell := requireShell(t)
	t.Setenv("PATH", "")

	store := newTempStore(t)
	profile := config.Profile{ID: "p1", Host: "host", Port: 22, User: "user"}
	if err := runWithNewStack(context.Background(), store, profile, []string{shell, "-c", "exit 0"}, nil); err == nil {
		t.Fatalf("expected runWithNewStack to fail without ssh")
	}
}

func TestRunTargetOnceWithOptionsFailures(t *testing.T) {
	t.Run("start error", func(t *testing.T) {
		err := runTargetOnceWithOptions(context.Background(), []string{"/nope"}, "", nil, nil, nil, nil, runTargetOptions{UseProxy: false})
		if err == nil {
			t.Fatalf("expected start error")
		}
	})

	t.Run("fatal channel terminates process", func(t *testing.T) {
		shell := requireShell(t)
		fatalCh := make(chan error, 1)
		go func() {
			time.Sleep(50 * time.Millisecond)
			fatalCh <- context.Canceled
		}()
		err := runTargetOnceWithOptions(
			context.Background(),
			[]string{shell, "-c", "sleep 5"},
			"",
			nil,
			fatalCh,
			nil,
			nil,
			runTargetOptions{UseProxy: false, PreserveTTY: false},
		)
		if err == nil || !strings.Contains(err.Error(), "proxy stack failed") {
			t.Fatalf("expected proxy stack failed error, got %v", err)
		}
	})

	t.Run("context cancel terminates process", func(t *testing.T) {
		shell := requireShell(t)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()
		err := runTargetOnceWithOptions(
			ctx,
			[]string{shell, "-c", "sleep 5"},
			"",
			nil,
			nil,
			nil,
			nil,
			runTargetOptions{UseProxy: false, PreserveTTY: false},
		)
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context canceled error, got %v", err)
		}
	})
}

func TestRunTargetWithFallbackYoloRetry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mockcmd")
	if runtime.GOOS == "windows" {
		path += ".cmd"
	}
	unix := "#!/bin/sh\nfor arg in \"$@\"; do\n  if [ \"$arg\" = \"--permission-mode\" ]; then\n    echo \"permission-mode unknown\"\n    exit 1\n  fi\n done\nexit 0\n"
	win := "@echo off\r\nset has=0\r\n:loop\r\nif \"%~1\"==\"\" goto done\r\nif \"%~1\"==\"--permission-mode\" set has=1\r\nshift\r\ngoto loop\r\n:done\r\nif \"%has%\"==\"1\" (\r\n  echo permission-mode unknown\r\n  exit /b 1\r\n)\r\nexit /b 0\r\n"
	writeStub(t, dir, "mockcmd", unix, win)

	called := false
	opts := runTargetOptions{
		UseProxy:    false,
		YoloEnabled: true,
		OnYoloFallback: func() error {
			called = true
			return nil
		},
	}
	cmdArgs := []string{path, "--permission-mode", "bypassPermissions"}
	if err := runTargetWithFallbackWithOptions(context.Background(), cmdArgs, "", nil, nil, nil, opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatalf("expected yolo fallback callback to be called")
	}
}

func TestRunWithNewStackOptionsSuccess(t *testing.T) {
	shell := requireShell(t)
	store := newTempStore(t)
	profile := config.Profile{ID: "p1", Host: "host", Port: 22, User: "user"}

	prevStart := stackStart
	t.Cleanup(func() { stackStart = prevStart })
	stackStart = func(profile config.Profile, instanceID string, opts stack.Options) (*stack.Stack, error) {
		return stack.NewStackForTest(12345, 23456), nil
	}

	if err := runWithNewStackOptions(context.Background(), store, profile, []string{shell, "-c", "exit 0"}, nil, runTargetOptions{UseProxy: false}); err != nil {
		t.Fatalf("runWithNewStackOptions error: %v", err)
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.Instances) != 0 {
		t.Fatalf("expected instances to be removed, got %d", len(cfg.Instances))
	}
}
