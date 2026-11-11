package localproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// ContextDialer supports cancellation through both TCP connect and SOCKS negotiation.
type ContextDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

var ErrBusy = errors.New("proxy connection capacity reached")

type HTTPProxy struct {
	instanceID  string
	dialer      Dialer
	transport   *http.Transport
	root        context.Context
	cancel      context.CancelFunc
	dialRoot    context.Context
	cancelDials context.CancelFunc
	mu          sync.Mutex
	listener    net.Listener
	server      *http.Server
	closing     bool
	backend     string
	conns       map[*trackedConn]struct{}
	slots       chan struct{}
	dials       chan struct{}
	closeOnce   sync.Once
	closeErr    error
}

type Options struct {
	InstanceID      string
	MaxConnections  int
	MaxPendingDials int
}

func NewHTTPProxy(d Dialer, opts Options) *HTTPProxy {
	if opts.MaxConnections <= 0 {
		opts.MaxConnections = 512
	}
	if opts.MaxPendingDials <= 0 {
		opts.MaxPendingDials = 32
	}
	root, cancel := context.WithCancel(context.Background())
	dr, dc := context.WithCancel(root)
	p := &HTTPProxy{
		instanceID: opts.InstanceID, dialer: d, root: root, cancel: cancel,
		dialRoot: dr, cancelDials: dc, backend: "ready",
		conns: make(map[*trackedConn]struct{}),
		slots: make(chan struct{}, opts.MaxConnections), dials: make(chan struct{}, opts.MaxPendingDials),
	}
	p.transport = &http.Transport{
		Proxy: nil, ForceAttemptHTTP2: false,
		ResponseHeaderTimeout: 30 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout: 90 * time.Second, MaxIdleConns: 64, MaxIdleConnsPerHost: 8,
		DialContext: p.dialContext,
	}
	return p
}

// Capture the underlying TCP connection so CONNECT can preserve TCP half-close
// semantics. The SOCKS library's returned wrapper hides CloseWrite.
type rawSOCKSConnKey struct{}

// Transport preserves context values while detaching request cancellation for dials.
type requestDialContextKey struct{}
type socksForward struct{ net.Dialer }

func (d *socksForward) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	c, err := d.Dialer.DialContext(ctx, network, addr)
	if raw, ok := ctx.Value(rawSOCKSConnKey{}).(*net.Conn); ok && err == nil {
		*raw = c
	}
	return c, err
}

type socksDialer struct {
	dialer  ContextDialer
	timeout time.Duration
}

func (d *socksDialer) Dial(network, addr string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, addr)
}
func (d *socksDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	var raw net.Conn
	c, err := d.dialer.DialContext(context.WithValue(ctx, rawSOCKSConnKey{}, &raw), network, addr)
	if err != nil {
		return nil, err
	}
	if raw != nil {
		return raw, nil
	}
	return c, nil
}
func NewSOCKS5Dialer(socksAddr string, timeout time.Duration) (Dialer, error) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	d, err := xproxy.SOCKS5("tcp", socksAddr, nil, &socksForward{Dialer: net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}})
	if err != nil {
		return nil, err
	}
	cd, ok := d.(ContextDialer)
	if !ok {
		return nil, errors.New("SOCKS dialer does not support context")
	}
	return &socksDialer{dialer: cd, timeout: timeout}, nil
}

// SetBackendState gates business requests while preserving the liveness endpoint.
func (p *HTTPProxy) SetBackendState(state string) {
	p.mu.Lock()
	p.backend = state
	p.mu.Unlock()
	if state != "ready" {
		p.transport.CloseIdleConnections()
	}
}

func (p *HTTPProxy) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	select {
	case p.dials <- struct{}{}:
		defer func() { <-p.dials }()
	default:
		return nil, ErrBusy
	}
	select {
	case p.slots <- struct{}{}:
	default:
		return nil, ErrBusy
	}
	release := true
	defer func() {
		if release {
			<-p.slots
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	stop := context.AfterFunc(p.dialRoot, cancel)
	defer stop()
	if requestCtx, ok := ctx.Value(requestDialContextKey{}).(context.Context); ok {
		stopRequest := context.AfterFunc(requestCtx, cancel)
		defer stopRequest()
		if requestCtx.Err() != nil {
			cancel()
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var c net.Conn
	var err error
	if d, ok := p.dialer.(ContextDialer); ok {
		c, err = d.DialContext(ctx, network, addr)
	} else {
		c, err = p.dialer.Dial(network, addr)
	}
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		c.Close()
		return nil, err
	}
	tc := &trackedConn{Conn: c, owner: p, slot: true}
	p.mu.Lock()
	if p.closing || p.backend != "ready" {
		p.mu.Unlock()
		c.Close()
		return nil, errors.New("proxy backend unavailable")
	}
	p.conns[tc] = struct{}{}
	p.mu.Unlock()
	release = false
	return tc, nil
}

type trackedConn struct {
	net.Conn
	owner *HTTPProxy
	slot  bool
	once  sync.Once
}

func (c *trackedConn) Close() error {
	var err error
	c.once.Do(func() {
		err = c.Conn.Close()
		c.owner.mu.Lock()
		delete(c.owner.conns, c)
		c.owner.mu.Unlock()
		if c.slot {
			<-c.owner.slots
		}
	})
	return err
}
func (c *trackedConn) CloseWrite() error {
	if h, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return h.CloseWrite()
	}
	return c.Close()
}

type dialerFunc func(network, addr string) (net.Conn, error)

func (d dialerFunc) Dial(network, addr string) (net.Conn, error) { return d(network, addr) }

func (p *HTTPProxy) Start(listenAddr string) (actualAddr string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.listener != nil || p.closing {
		return "", errors.New("proxy already started")
	}

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return "", err
	}

	srv := &http.Server{
		Handler:           http.HandlerFunc(p.serveHTTP),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return p.root },
	}

	p.listener = ln
	p.server = srv

	go func() {
		_ = srv.Serve(ln)
	}()

	return ln.Addr().String(), nil
}

func (p *HTTPProxy) Close(ctx context.Context) error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closing = true
		srv := p.server
		p.mu.Unlock()
		p.cancelDials()
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if srv != nil {
			p.closeErr = srv.Shutdown(bounded)
		}
		p.cancel()
		p.mu.Lock()
		conns := make([]*trackedConn, 0, len(p.conns))
		for c := range p.conns {
			conns = append(conns, c)
		}
		p.mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
		if srv != nil {
			_ = srv.Close()
		}
		p.transport.CloseIdleConnections()
	})
	return p.closeErr
}

func (p *HTTPProxy) serveHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	state, closing := p.backend, p.closing
	p.mu.Unlock()
	// Local health check (not proxied).
	if r.Method == http.MethodGet && r.URL.Path == "/_claude_proxy/health" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":           true,
			"instanceId":   p.instanceID,
			"backendState": state,
		})
		return
	}

	if closing || state != "ready" {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "proxy backend "+state, http.StatusServiceUnavailable)
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

	upstream, err := p.dialContext(r.Context(), "tcp", dest)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, ErrBusy) {
			status = http.StatusServiceUnavailable
			w.Header().Set("Retry-After", "1")
		}
		http.Error(w, "dial upstream: "+err.Error(), status)
		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}

	clientConn, buffered, err := hj.Hijack()
	if err != nil {
		_ = upstream.Close()
		http.Error(w, "hijack: "+err.Error(), http.StatusInternalServerError)
		return
	}

	client := &trackedConn{Conn: clientConn, owner: p}
	p.mu.Lock()
	if p.closing {
		p.mu.Unlock()
		clientConn.Close()
		upstream.Close()
		return
	}
	p.conns[client] = struct{}{}
	p.mu.Unlock()
	defer client.Close()
	defer upstream.Close()
	if _, err = fmt.Fprint(buffered, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err = buffered.Flush(); err != nil {
		return
	}
	done := make(chan error, 2)
	go func() {
		_, e := io.Copy(upstream, buffered)
		if e == nil {
			if h, ok := upstream.(interface{ CloseWrite() error }); ok {
				_ = h.CloseWrite()
			}
		}
		done <- e
	}()
	go func() {
		_, e := io.Copy(client, upstream)
		if e == nil {
			_ = client.CloseWrite()
		}
		done <- e
	}()
	if e := <-done; e != nil {
		client.Close()
		upstream.Close()
	}
	<-done
}

func (p *HTTPProxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	// Minimal forward-proxy support for absolute-form requests.
	// Many clients will only use CONNECT for HTTPS; this covers plain HTTP too.

	outReq := r.Clone(context.WithValue(r.Context(), requestDialContextKey{}, r.Context()))
	outReq.RequestURI = ""
	outReq.Header.Del("Proxy-Connection")

	resp, err := p.transport.RoundTrip(outReq)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, ErrBusy) {
			status = http.StatusServiceUnavailable
			w.Header().Set("Retry-After", "1")
		}
		http.Error(w, "round trip: "+err.Error(), status)
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
