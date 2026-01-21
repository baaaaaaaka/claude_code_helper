package manager

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestHealthClient_CheckHTTPProxy(t *testing.T) {
	port, closeFn := startHealthOnlyServer(t, "inst-1")
	defer closeFn()

	hc := HealthClient{Timeout: 1 * time.Second}
	if err := hc.CheckHTTPProxy(port, "inst-1"); err != nil {
		t.Fatalf("CheckHTTPProxy: %v", err)
	}
	if err := hc.CheckHTTPProxy(port, "wrong"); err == nil {
		t.Fatalf("expected instance id mismatch error")
	}
}

func startHealthOnlyServer(t *testing.T, instanceID string) (port int, closeFn func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/_claude_proxy/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":         true,
			"instanceId": instanceID,
		})
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	tcp, err := net.ResolveTCPAddr("tcp", "127.0.0.1:"+portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	return tcp.Port, func() {
		_ = srv.Shutdown(context.Background())
		_ = ln.Close()
	}
}
