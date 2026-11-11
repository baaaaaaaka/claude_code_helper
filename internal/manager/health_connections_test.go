package manager

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHealthClientReusesAndClosesConnections(t *testing.T) {
	var opened, closed atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "instanceId": "pool"})
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			opened.Add(1)
		}
		if s == http.StateClosed {
			closed.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()
	_, ps, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ := strconv.Atoi(ps)
	hc := NewHealthClient(time.Second)
	defer hc.Close()
	for i := 0; i < 500; i++ {
		if err := hc.CheckHTTPProxy(port, "pool"); err != nil {
			t.Fatal(err)
		}
	}
	if n := opened.Load(); n != 1 {
		t.Fatalf("500 checks opened %d connections, want 1", n)
	}
	hc.Close()
	deadline := time.Now().Add(time.Second)
	for closed.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if closed.Load() != 1 {
		t.Fatal("health connection not closed")
	}
}
func TestHealthClientRecoveryAndResponseBounds(t *testing.T) {
	var state atomic.Value
	state.Store("recovering")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_claude_proxy/health" {
			if state.Load() == "oversized" {
				_, _ = w.Write([]byte(strings.Repeat("x", 9000)))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "instanceId": "id", "backendState": state.Load()})
		}
	}))
	defer srv.Close()
	_, ps, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ := strconv.Atoi(ps)
	hc := NewHealthClient(time.Second)
	defer hc.Close()
	if err := hc.CheckHTTPProxy(port, "id"); !errors.Is(err, ErrRecovering) {
		t.Fatalf("recovering: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if err := hc.WaitReady(ctx, port, "id"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait: %v", err)
	}
	state.Store("ready")
	if err := hc.WaitReady(context.Background(), port, "id"); err != nil {
		t.Fatal(err)
	}
	state.Store("failed")
	if err := hc.CheckHTTPProxy(port, "id"); err == nil {
		t.Fatal("failed backend accepted")
	}
	state.Store("oversized")
	if err := hc.CheckHTTPProxy(port, "id"); err == nil {
		t.Fatal("oversized response accepted")
	}
}
func BenchmarkHealthClientPooled(b *testing.B) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"instanceId":"bench"}`))
	}))
	defer srv.Close()
	_, ps, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ := strconv.Atoi(ps)
	hc := NewHealthClient(time.Second)
	defer hc.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := hc.CheckHTTPProxy(port, "bench"); err != nil {
			b.Fatal(err)
		}
	}
}
