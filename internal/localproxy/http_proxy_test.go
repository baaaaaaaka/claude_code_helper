package localproxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHTTPProxy_HealthEndpoint(t *testing.T) {
	p := NewHTTPProxy(dialerFunc(func(network, addr string) (net.Conn, error) {
		return nil, io.EOF
	}), Options{InstanceID: "health-id"})

	httpAddr, err := p.Start("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = p.Close(context.Background()) }()

	resp, err := http.Get("http://" + httpAddr + "/_claude_proxy/health")
	if err != nil {
		t.Fatalf("GET health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%s", resp.Status)
	}

	var body struct {
		OK         bool   `json:"ok"`
		InstanceID string `json:"instanceId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.OK || body.InstanceID != "health-id" {
		t.Fatalf("body=%#v", body)
	}
}

func TestHTTPProxy_ForwardsPlainHTTP(t *testing.T) {
	originAddr, closeOrigin := startHTTPOrigin(t)
	defer closeOrigin()

	rec := &recordingDialer{}

	p := NewHTTPProxy(rec, Options{InstanceID: "plain-http"})
	httpAddr, err := p.Start("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = p.Close(context.Background()) }()

	proxyURL, _ := url.Parse("http://" + httpAddr)
	client := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}

	resp, err := client.Get("http://" + originAddr + "/hello")
	if err != nil {
		t.Fatalf("GET via proxy: %v", err)
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	if got := strings.TrimSpace(string(b)); got != "hello" {
		t.Fatalf("body=%q", got)
	}

	if !rec.SawAddr(originAddr) {
		t.Fatalf("expected dialer to see origin addr %q, got %#v", originAddr, rec.Addrs())
	}
}

func TestHTTPProxyEdgeCases(t *testing.T) {
	t.Run("Start rejects double start", func(t *testing.T) {
		p := NewHTTPProxy(dialerFunc(func(network, addr string) (net.Conn, error) {
			return nil, io.EOF
		}), Options{})
		addr, err := p.Start("127.0.0.1:0")
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = p.Close(context.Background()) }()
		if _, err := p.Start(addr); err == nil {
			t.Fatalf("expected Start to fail when already started")
		}
	})

	t.Run("Start fails when port is occupied", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		defer ln.Close()
		addr := ln.Addr().String()
		p := NewHTTPProxy(dialerFunc(func(network, addr string) (net.Conn, error) {
			return nil, io.EOF
		}), Options{})
		if _, err := p.Start(addr); err == nil {
			t.Fatalf("expected Start to fail for occupied port")
		}
	})

	t.Run("handleConnect validates host and hijack", func(t *testing.T) {
		p := NewHTTPProxy(dialerFunc(func(network, addr string) (net.Conn, error) {
			return nil, errors.New("dial failed")
		}), Options{})
		rec := httptest.NewRecorder()
		req := &http.Request{Method: http.MethodConnect}
		p.handleConnect(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for missing host, got %d", rec.Code)
		}

		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodConnect, "http://example.com", nil)
		req.Host = "example.com:443"
		p.handleConnect(rec, req)
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("expected 502 for dial failure, got %d", rec.Code)
		}

		upstream, downstream := net.Pipe()
		defer upstream.Close()
		defer downstream.Close()
		p = NewHTTPProxy(dialerFunc(func(network, addr string) (net.Conn, error) {
			return upstream, nil
		}), Options{})
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodConnect, "http://example.com", nil)
		req.Host = "example.com:443"
		p.handleConnect(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500 for missing hijacker, got %d", rec.Code)
		}
	})

	t.Run("handleHTTP forwards non-200 status", func(t *testing.T) {
		originAddr, closeOrigin := startHTTPOrigin(t)
		defer closeOrigin()
		originURL := "http://" + originAddr + "/fail"

		p := NewHTTPProxy(dialerFunc(func(network, addr string) (net.Conn, error) {
			return net.DialTimeout(network, addr, 2*time.Second)
		}), Options{})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, originURL, nil)
		p.handleHTTP(rec, req)
		if rec.Code == http.StatusOK {
			t.Fatalf("expected non-200 status")
		}
	})

	t.Run("NewSOCKS5Dialer defaults timeout", func(t *testing.T) {
		d, err := NewSOCKS5Dialer("127.0.0.1:9999", 0)
		if err != nil {
			t.Fatalf("NewSOCKS5Dialer error: %v", err)
		}
		if d == nil {
			t.Fatalf("expected dialer to be created")
		}
	})
}

func startHTTPOrigin(t *testing.T) (addr string, closeFn func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen origin: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()

	return ln.Addr().String(), func() {
		_ = srv.Shutdown(context.Background())
		_ = ln.Close()
	}
}

type recordingDialer struct {
	mu    sync.Mutex
	addrs []string
}

func (d *recordingDialer) Dial(network, addr string) (net.Conn, error) {
	d.mu.Lock()
	d.addrs = append(d.addrs, addr)
	d.mu.Unlock()
	return net.DialTimeout(network, addr, 2*time.Second)
}

func (d *recordingDialer) Addrs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, len(d.addrs))
	copy(out, d.addrs)
	return out
}

func (d *recordingDialer) SawAddr(addr string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, a := range d.addrs {
		if a == addr {
			return true
		}
	}
	return false
}
