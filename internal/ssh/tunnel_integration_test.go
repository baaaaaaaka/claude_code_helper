//go:build linux

package ssh

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestTunnelIntegrationStartStop(t *testing.T) {
	if os.Getenv("SSH_TEST_ENABLED") != "1" {
		t.Skip("SSH integration tests disabled")
	}
	host := os.Getenv("SSH_TEST_HOST")
	portStr := os.Getenv("SSH_TEST_PORT")
	user := os.Getenv("SSH_TEST_USER")
	key := os.Getenv("SSH_TEST_KEY")
	if host == "" || portStr == "" || user == "" || key == "" {
		t.Skip("missing SSH_TEST_* env vars")
	}
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skipf("ssh not available: %v", err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("invalid SSH_TEST_PORT: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_, portStr, _ = net.SplitHostPort(ln.Addr().String())
	_ = ln.Close()
	socksPort, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse socks port: %v", err)
	}

	cfg := TunnelConfig{
		Host:      host,
		Port:      port,
		User:      user,
		SocksPort: socksPort,
		ExtraArgs: []string{
			"-i", key,
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
		},
		BatchMode: true,
	}
	tun, err := NewTunnel(cfg)
	if err != nil {
		t.Fatalf("NewTunnel error: %v", err)
	}

	if err := tun.Start(); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	t.Cleanup(func() { _ = tun.Stop(1 * time.Second) })

	if tun.PID() == 0 {
		t.Fatalf("expected tunnel PID to be set")
	}

	select {
	case <-tun.Done():
		// Retry once to avoid flakiness if sshd isn't fully ready.
		if err := tun.Stop(200 * time.Millisecond); err != nil {
			// best effort cleanup
		}
		tun, err = NewTunnel(cfg)
		if err != nil {
			t.Fatalf("NewTunnel retry error: %v", err)
		}
		if err := tun.Start(); err != nil {
			t.Fatalf("Start retry error: %v", err)
		}
		select {
		case <-tun.Done():
			t.Fatalf("tunnel exited early")
		case <-time.After(300 * time.Millisecond):
		}
	case <-time.After(300 * time.Millisecond):
	}

	if err := tun.Stop(1 * time.Second); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("Stop error: %v", err)
		}
	}
}
