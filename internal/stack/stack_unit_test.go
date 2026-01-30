package stack

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
	"github.com/baaaaaaaka/claude_code_helper/internal/ssh"
)

func TestStartValidationErrors(t *testing.T) {
	profile := config.Profile{Host: "", Port: 22, User: "u"}
	if _, err := Start(profile, "id", Options{}); err == nil {
		t.Fatalf("expected error for missing host")
	}

	profile = config.Profile{Host: "h", Port: 0, User: "u"}
	if _, err := Start(profile, "id", Options{}); err == nil {
		t.Fatalf("expected error for missing port")
	}

	profile = config.Profile{Host: "h", Port: 22, User: ""}
	if _, err := Start(profile, "id", Options{}); err == nil {
		t.Fatalf("expected error for missing user")
	}

	profile = config.Profile{Host: "h", Port: 22, User: "u"}
	if _, err := Start(profile, "", Options{}); err == nil {
		t.Fatalf("expected error for missing instance id")
	}
}

func TestStackHelpers(t *testing.T) {
	s := &Stack{
		HTTPPort: 1234,
		fatalCh:  make(chan error),
		stopCh:   make(chan struct{}),
	}
	if got := s.HTTPProxyURL(); got != "http://127.0.0.1:1234" {
		t.Fatalf("unexpected HTTPProxyURL: %q", got)
	}
	if s.Fatal() == nil {
		t.Fatalf("expected fatal channel")
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close error: %v", err)
	}
	select {
	case <-s.stopCh:
	default:
		t.Fatalf("expected stopCh to be closed")
	}
}

func TestNewTunnel(t *testing.T) {
	profile := config.Profile{Host: "example.com", Port: 22, User: "alice"}
	if _, err := newTunnel(profile, 12345); err != nil {
		t.Fatalf("newTunnel error: %v", err)
	}
}

func TestStartFailsWhenHTTPPortInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	profile := config.Profile{Host: "example.com", Port: 22, User: "alice"}
	_, err = Start(profile, "inst-1", Options{
		HTTPListenAddr:    ln.Addr().String(),
		SocksPort:         12345,
		SocksReadyTimeout: 10 * time.Millisecond,
	})
	if err == nil {
		t.Fatalf("expected Start to fail for occupied http port")
	}
}

func TestStartFailsWhenSSHMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip ssh PATH test on windows")
	}
	t.Setenv("PATH", "")
	profile := config.Profile{Host: "example.com", Port: 22, User: "alice"}
	_, err := Start(profile, "inst-ssh-missing", Options{
		SocksPort:         12345,
		SocksReadyTimeout: 10 * time.Millisecond,
	})
	if err == nil {
		t.Fatalf("expected Start to fail when ssh is missing")
	}
}

func TestMonitorReportsFatalOnImmediateExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "ssh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	t.Setenv("PATH", dir)

	tun, err := ssh.NewTunnel(ssh.TunnelConfig{
		Host:      "example.com",
		Port:      22,
		User:      "alice",
		SocksPort: 12345,
	})
	if err != nil {
		t.Fatalf("NewTunnel error: %v", err)
	}
	if err := tun.Start(); err != nil {
		t.Fatalf("Start error: %v", err)
	}

	s := &Stack{
		Profile:   config.Profile{Host: "example.com", Port: 22, User: "alice"},
		SocksPort: 12345,
		tunnel:    tun,
		fatalCh:   make(chan error, 1),
		stopCh:    make(chan struct{}),
	}

	go s.monitor(Options{MaxRestarts: 0, RestartBackoff: 5 * time.Millisecond})

	select {
	case err := <-s.fatalCh:
		if err == nil {
			t.Fatalf("expected fatal error")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for fatal error")
	}
}
