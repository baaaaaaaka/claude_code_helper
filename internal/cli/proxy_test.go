package cli

import (
	"fmt"
	"net"
	"runtime"
	"testing"
)

func TestParsePort(t *testing.T) {
	if _, err := parsePort("abc"); err == nil {
		t.Fatalf("expected parsePort error for non-numeric")
	}
	if _, err := parsePort("70000"); err == nil {
		t.Fatalf("expected parsePort error for out-of-range port")
	}
	port, err := parsePort("12345")
	if err != nil {
		t.Fatalf("parsePort error: %v", err)
	}
	if port != 12345 {
		t.Fatalf("expected port 12345, got %d", port)
	}
}

func TestPickFreePort(t *testing.T) {
	port, err := pickFreePort()
	if err != nil {
		t.Fatalf("pickFreePort error: %v", err)
	}
	if port <= 0 {
		t.Fatalf("expected positive port, got %d", port)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port)))
	if err != nil {
		t.Skipf("port %d became unavailable: %v", port, err)
	}
	_ = ln.Close()
}

func TestInstallHintsNonEmpty(t *testing.T) {
	hints := installHints()
	if len(hints) == 0 {
		t.Fatalf("expected non-empty install hints")
	}
}

func TestLinuxOSReleaseID(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linuxOSReleaseID only relevant on linux")
	}
	_ = linuxOSReleaseID()
}
