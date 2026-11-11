package localproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

func TestHTTPProxyReusesAndClosesUpstream(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer origin.Close()
	d := &recordingDialer{}
	p := NewHTTPProxy(d, Options{})
	defer p.Close(context.Background())
	addr, err := p.Start("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse("http://" + addr)
	tr := &http.Transport{Proxy: http.ProxyURL(u)}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: time.Second}
	for i := 0; i < 200; i++ {
		r, e := client.Get(origin.URL)
		if e != nil {
			t.Fatal(e)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		r.Body.Close()
	}
	if n := len(d.Addrs()); n != 1 {
		t.Fatalf("200 requests made %d upstream dials, want 1", n)
	}
	if err = p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	n := len(p.conns)
	p.mu.Unlock()
	if n != 0 || len(p.slots) != 0 {
		t.Fatalf("upstream connections after Close: %d", n)
	}
}
func TestSOCKSHandshakeDeadlineAndCancellation(t *testing.T) {
	for _, cancelEarly := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelEarly), func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			accepted := make(chan net.Conn, 1)
			go func() {
				c, e := ln.Accept()
				if e == nil {
					accepted <- c
				}
			}()
			d, err := NewSOCKS5Dialer(ln.Addr().String(), 150*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, e := d.(ContextDialer).DialContext(ctx, "tcp", "example.org:443"); done <- e }()
			var peer net.Conn
			select {
			case peer = <-accepted:
			case <-time.After(time.Second):
				t.Fatal("no TCP connect")
			}
			defer peer.Close()
			if cancelEarly {
				cancel()
			}
			select {
			case e := <-done:
				if e == nil {
					t.Fatal("missing handshake error")
				}
			case <-time.After(time.Second):
				t.Fatal("SOCKS handshake ignored deadline/cancellation")
			}
			_ = peer.SetReadDeadline(time.Now().Add(time.Second))
			_, err = io.Copy(io.Discard, peer)
			if err != nil {
				t.Fatalf("SOCKS socket not closed: %v", err)
			}
		})
	}
}
func TestSOCKSHandshakeDeadlineDoesNotExpireTunnel(t *testing.T) {
	echo, closeEcho := startTCPEchoServer(t)
	defer closeEcho()
	socks := startSOCKS5Server(t)
	defer socks.Close()
	d, err := NewSOCKS5Dialer(socks.Addr(), 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	c, err := d.Dial("tcp", echo)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	time.Sleep(150 * time.Millisecond)
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	if _, err = c.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 1)
	if _, err = io.ReadFull(c, b); err != nil {
		t.Fatal(err)
	}
}
func TestHTTPProxyCloseTracksBufferedCONNECT(t *testing.T) {
	echo, closeEcho := startTCPEchoServer(t)
	defer closeEcho()
	p := NewHTTPProxy(&recordingDialer{}, Options{})
	defer p.Close(context.Background())
	a, err := p.Start("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("tcp", a)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\nping", echo, echo)
	br := bufio.NewReader(c)
	for {
		line, e := br.ReadString('\n')
		if e != nil {
			t.Fatal(e)
		}
		if line == "\r\n" {
			break
		}
	}
	b := make([]byte, 4)
	if _, err = io.ReadFull(br, b); err != nil || string(b) != "ping" {
		t.Fatalf("buffered payload: %q %v", b, err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := p.Close(context.Background()); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if _, err = br.ReadByte(); err == nil {
		t.Fatal("CONNECT not closed")
	}
	p.mu.Lock()
	n := len(p.conns)
	p.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d connections remain", n)
	}
}
func TestHTTPProxyRecoveringRejectsBusinessButServesHealth(t *testing.T) {
	p := NewHTTPProxy(dialerFunc(func(string, string) (net.Conn, error) { t.Error("dial during recovery"); return nil, io.EOF }), Options{})
	defer p.Close(context.Background())
	p.SetBackendState("recovering")
	w := httptest.NewRecorder()
	p.serveHTTP(w, httptest.NewRequest("GET", "http://example.org/", nil))
	if w.Code != 503 {
		t.Fatalf("business status %d", w.Code)
	}
	w = httptest.NewRecorder()
	p.serveHTTP(w, httptest.NewRequest("GET", "http://localhost/_claude_proxy/health", nil))
	if w.Code != 200 {
		t.Fatalf("liveness status %d", w.Code)
	}
}
func TestHTTPProxyConnectionCapacity(t *testing.T) {
	p := NewHTTPProxy(&recordingDialer{}, Options{MaxConnections: 1})
	defer p.Close(context.Background())
	echo, closeEcho := startTCPEchoServer(t)
	defer closeEcho()
	c, err := p.dialContext(context.Background(), "tcp", echo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.dialContext(context.Background(), "tcp", echo); !errors.Is(err, ErrBusy) {
		t.Fatalf("capacity error %v", err)
	}
	c.Close()
	if c, err = p.dialContext(context.Background(), "tcp", echo); err != nil {
		t.Fatal(err)
	}
	c.Close()
}

type waitingDialer struct{ started chan struct{} }

func (d waitingDialer) Dial(n, a string) (net.Conn, error) {
	return d.DialContext(context.Background(), n, a)
}
func (d waitingDialer) DialContext(ctx context.Context, _ string, _ string) (net.Conn, error) {
	close(d.started)
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestHTTPProxyCloseCancelsPendingDialAndLimitsAttempts(t *testing.T) {
	started := make(chan struct{})
	p := NewHTTPProxy(waitingDialer{started}, Options{MaxPendingDials: 1})
	defer p.Close(context.Background())
	done := make(chan error, 1)
	go func() { _, err := p.dialContext(context.Background(), "tcp", "unused:443"); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("no pending dial")
	}
	if _, err := p.dialContext(context.Background(), "tcp", "unused:443"); !errors.Is(err, ErrBusy) {
		t.Fatalf("pending capacity: %v", err)
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close left dial blocked")
	}
	if len(p.slots) != 0 || len(p.dials) != 0 {
		t.Fatal("dial permits leaked")
	}
}
func TestHTTPProxyCONNECTPreservesHalfCloseResponse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	peerDone := make(chan struct{})
	go func() {
		defer close(peerDone)
		c, e := ln.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(2 * time.Second))
		body, _ := io.ReadAll(c)
		_, _ = c.Write(append([]byte("response:"), body...))
	}()
	p := NewHTTPProxy(&recordingDialer{}, Options{})
	defer p.Close(context.Background())
	a, err := p.Start("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("tcp", a)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\npayload", ln.Addr(), ln.Addr())
	br := bufio.NewReader(c)
	for {
		line, e := br.ReadString('\n')
		if e != nil {
			t.Fatal(e)
		}
		if line == "\r\n" {
			break
		}
	}
	_ = c.(*net.TCPConn).CloseWrite()
	body, err := io.ReadAll(br)
	if err != nil || string(body) != "response:payload" {
		t.Fatalf("half-close response %q: %v", body, err)
	}
	<-peerDone
}

// The HTTP transport detaches its dial context from request cancellation.
// A canceled request must still cancel its pending SOCKS handshake.
type cancelObservedDialer struct{ started, canceled chan struct{} }

func (d cancelObservedDialer) Dial(n, a string) (net.Conn, error) {
	return d.DialContext(context.Background(), n, a)
}
func (d cancelObservedDialer) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	close(d.started)
	<-ctx.Done()
	close(d.canceled)
	return nil, ctx.Err()
}
func TestHTTPProxyRequestCancellationStopsTransportDial(t *testing.T) {
	d := cancelObservedDialer{make(chan struct{}), make(chan struct{})}
	p := NewHTTPProxy(d, Options{})
	defer p.Close(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "http://example.org/", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() { p.serveHTTP(httptest.NewRecorder(), req); close(done) }()
	select {
	case <-d.started:
	case <-time.After(time.Second):
		t.Fatal("transport did not start dial")
	}
	cancel()
	select {
	case <-d.canceled:
	case <-time.After(time.Second):
		t.Fatal("HTTP request canceled but transport dial kept running")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("HTTP handler did not finish")
	}
}
