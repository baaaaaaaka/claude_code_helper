package localproxy

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
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
