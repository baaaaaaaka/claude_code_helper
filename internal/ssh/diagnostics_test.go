package ssh

import (
	"strings"
	"testing"
)

func TestTunnelBindingConfirmationAndConflictClassification(t *testing.T) {
	tun := &Tunnel{cfg: TunnelConfig{SocksPort: 12345}}
	w := tunnelDiagnostics{t: tun}
	_, _ = w.Write([]byte("debug1: Local forwarding listening on 127.0.0.1 port 12"))
	if tun.BindingReady() {
		t.Fatal("partial confirmation accepted")
	}
	_, _ = w.Write([]byte("345.\r\n"))
	if !tun.BindingReady() {
		t.Fatal("binding confirmation not captured")
	}
	_, _ = w.Write([]byte(strings.Repeat("x", 20000)))
	if len(tun.diagnostic) > 8192 {
		t.Fatal("diagnostic tail unbounded")
	}
	tun = &Tunnel{}
	w = tunnelDiagnostics{t: tun}
	_, _ = w.Write([]byte("Permission denied (publickey)"))
	if tun.BindConflict() {
		t.Fatal("authentication failure classified as bind conflict")
	}
	_, _ = w.Write([]byte("bind [127.0.0.1]:12345: Address already in use"))
	if !tun.BindConflict() {
		t.Fatal("bind conflict not recognized")
	}
}
