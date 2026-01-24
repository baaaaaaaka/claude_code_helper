package ssh

import (
	"errors"
	"os/exec"
	"testing"
)

func TestNewTunnelInvalidConfig(t *testing.T) {
	if _, err := NewTunnel(TunnelConfig{Host: "", Port: 22, User: "u", SocksPort: 1234}); err == nil {
		t.Fatalf("expected error for missing host")
	}
}

func TestTunnelPIDBeforeStart(t *testing.T) {
	tun, err := NewTunnel(TunnelConfig{Host: "example.com", Port: 22, User: "u", SocksPort: 1234})
	if err != nil {
		t.Fatalf("NewTunnel error: %v", err)
	}
	if tun.PID() != 0 {
		t.Fatalf("expected PID 0 before start")
	}
}

func TestTunnelStartNotInitialized(t *testing.T) {
	tun := &Tunnel{done: make(chan struct{})}
	if err := tun.Start(); err == nil {
		t.Fatalf("expected Start to fail for nil cmd")
	}
}

func TestTunnelWaitReturnsErr(t *testing.T) {
	tun := &Tunnel{done: make(chan struct{})}
	close(tun.done)
	tun.waitErr = errors.New("wait error")
	if err := tun.Wait(); err == nil {
		t.Fatalf("expected Wait to return error")
	}
}

func TestTunnelStopNoProcess(t *testing.T) {
	tun := &Tunnel{cmd: &exec.Cmd{}, done: make(chan struct{})}
	if err := tun.Stop(0); err != nil {
		t.Fatalf("expected Stop to return nil, got %v", err)
	}
}
