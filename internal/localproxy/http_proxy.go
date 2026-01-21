package localproxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	xproxy "golang.org/x/net/proxy"
)

type Dialer interface {
	Dial(network, addr string) (net.Conn, error)
}

type HTTPProxy struct {
	instanceID string
	dialer     Dialer

	mu       sync.Mutex
	listener net.Listener
	server   *http.Server
}

type Options struct {
	InstanceID string
}

func NewHTTPProxy(d Dialer, opts Options) *HTTPProxy {
	return &HTTPProxy{
		instanceID: opts.InstanceID,
		dialer:     d,
	}
}

func NewSOCKS5Dialer(socksAddr string, timeout time.Duration) (Dialer, error) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	forward := &net.Dialer{Timeout: timeout}
	fwd := dialerFunc(func(network, addr string) (net.Conn, error) {
		return forward.Dial(network, addr)
	})

	d, err := xproxy.SOCKS5("tcp", socksAddr, nil, fwd)
	if err != nil {
		return nil, err
	}
	return d, nil
}

type dialerFunc func(network, addr string) (net.Conn, error)

func (d dialerFunc) Dial(network, addr string) (net.Conn, error) { return d(network, addr) }

func (p *HTTPProxy) Start(listenAddr string) (actualAddr string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.listener != nil {
		return "", errors.New("proxy already started")
	}

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return "", err
	}

	srv := &http.Server{
		Handler:           http.HandlerFunc(p.serveHTTP),
		ReadHeaderTimeout: 10 * time.Second,
	}

	p.listener = ln
	p.server = srv

	go func() {
		_ = srv.Serve(ln)
	}()

	return ln.Addr().String(), nil
}

func (p *HTTPProxy) Close(ctx context.Context) error {
	p.mu.Lock()
	srv := p.server
	p.mu.Unlock()

	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}

func (p *HTTPProxy) serveHTTP(w http.ResponseWriter, r *http.Request) {
	// Local health check (not proxied).
	if r.Method == http.MethodGet && r.URL.Path == "/_claude_proxy/health" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":         true,
			"instanceId": p.instanceID,
		})
		return
	}

	if strings.EqualFold(r.Method, http.MethodConnect) {
		p.handleConnect(w, r)
		return
	}

	p.handleHTTP(w, r)
}

func (p *HTTPProxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	dest := r.Host
	if dest == "" {
		http.Error(w, "missing host", http.StatusBadRequest)
		return
	}

	upstream, err := p.dialer.Dial("tcp", dest)
	if err != nil {
		http.Error(w, "dial upstream: "+err.Error(), http.StatusBadGateway)
		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}

	clientConn, _, err := hj.Hijack()
	if err != nil {
		_ = upstream.Close()
		http.Error(w, "hijack: "+err.Error(), http.StatusInternalServerError)
		return
	}

	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	go func() {
		_, _ = io.Copy(upstream, clientConn)
		_ = upstream.Close()
	}()

	_, _ = io.Copy(clientConn, upstream)
	_ = clientConn.Close()
}

func (p *HTTPProxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	// Minimal forward-proxy support for absolute-form requests.
	// Many clients will only use CONNECT for HTTPS; this covers plain HTTP too.

	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""
	outReq.Header.Del("Proxy-Connection")

	tr := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     false,
		ResponseHeaderTimeout: 30 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return p.dialer.Dial(network, addr)
		},
	}

	resp, err := tr.RoundTrip(outReq)
	if err != nil {
		http.Error(w, "round trip: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		dst.Del(k)
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}
