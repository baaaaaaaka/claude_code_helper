package stack

import (
	"net"
	"strings"
	"testing"
	"time"
)

func TestPickFreePort_IsBindable(t *testing.T) {
	port, err := pickFreePort()
	if err != nil {
		t.Fatalf("pickFreePort: %v", err)
	}

	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", intToString(port)))
	if err != nil {
		t.Fatalf("listen on picked port %d: %v", port, err)
	}
	_ = ln.Close()
}

func TestWaitForTCP_SucceedsWhenListening(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	if err := waitForTCP(ln.Addr().String(), 1*time.Second); err != nil {
		t.Fatalf("waitForTCP: %v", err)
	}
}

func TestWaitForTCP_TimesOutAndIncludesCause(t *testing.T) {
	addr := "127.0.0.1:0"
	err := waitForTCP(addr, 150*time.Millisecond)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "timeout waiting for") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), addr) {
		t.Fatalf("expected addr in error: %v", err)
	}
}

func TestWaitForTCPTunnel_TimesOutWhenNoTunnelAndNotListening(t *testing.T) {
	addr := "127.0.0.1:0"
	err := waitForTCPTunnel(addr, 150*time.Millisecond, nil)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "timeout waiting for") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), addr) {
		t.Fatalf("expected addr in error: %v", err)
	}
}

func intToString(n int) string {
	// tiny helper to avoid fmt in tests
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + (n % 10))
		n /= 10
	}
	return string(buf[i:])
}
