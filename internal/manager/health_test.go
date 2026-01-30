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

func TestHealthClient_CheckHTTPProxyErrors(t *testing.T) {
	hc := HealthClient{Timeout: 200 * time.Millisecond}

	t.Run("invalid port", func(t *testing.T) {
		if err := hc.CheckHTTPProxy(0, ""); err == nil {
			t.Fatalf("expected invalid port error")
		}
	})

	t.Run("non-200 status", func(t *testing.T) {
		port, closeFn := startCustomHealthServer(t, http.StatusInternalServerError, `{"ok":true,"instanceId":"x"}`)
		defer closeFn()
		if err := hc.CheckHTTPProxy(port, ""); err == nil {
			t.Fatalf("expected status error")
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		port, closeFn := startCustomHealthServer(t, http.StatusOK, "not-json")
		defer closeFn()
		if err := hc.CheckHTTPProxy(port, ""); err == nil {
			t.Fatalf("expected json decode error")
		}
	})

	t.Run("ok false", func(t *testing.T) {
		port, closeFn := startCustomHealthServer(t, http.StatusOK, `{"ok":false,"instanceId":"x"}`)
		defer closeFn()
		if err := hc.CheckHTTPProxy(port, ""); err == nil {
			t.Fatalf("expected health not ok error")
		}
	})
}

func startCustomHealthServer(t *testing.T, status int, body string) (port int, closeFn func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/_claude_proxy/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
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
