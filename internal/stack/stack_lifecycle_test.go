package stack

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
	"github.com/baaaaaaaka/claude_code_helper/internal/localproxy"
)

type fakeDialer struct{}

func (fakeDialer) Dial(network, addr string) (net.Conn, error) { return nil, errors.New("unused") }

type fakeProxy struct {
	startAddr  string
	startErr   error
	closeErr   error
	closeCalls int
}

func (p *fakeProxy) Start(listenAddr string) (string, error) {
	if p.startErr != nil {
		return "", p.startErr
	}
	return p.startAddr, nil
}

func (p *fakeProxy) Close(context.Context) error {
	p.closeCalls++
	return p.closeErr
}

type fakeTunnel struct {
	mu         sync.Mutex
	waitErr    error
	startErr   error
	stopErr    error
	startCalls int
	stopCalls  int
	done       chan struct{}
	once       sync.Once
}

func newFakeTunnel(waitErr error) *fakeTunnel {
	return &fakeTunnel{
		waitErr: waitErr,
		done:    make(chan struct{}),
	}
}

func (t *fakeTunnel) Start() error {
	t.mu.Lock()
	t.startCalls++
	err := t.startErr
	t.mu.Unlock()
	return err
}

func (t *fakeTunnel) Stop(time.Duration) error {
	t.mu.Lock()
	t.stopCalls++
	err := t.stopErr
	t.mu.Unlock()
	t.once.Do(func() { close(t.done) })
	return err
}

func (t *fakeTunnel) Wait() error {
	<-t.done
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.waitErr
}

func (t *fakeTunnel) Done() <-chan struct{} { return t.done }

func (t *fakeTunnel) exit() {
	t.once.Do(func() { close(t.done) })
}

func (t *fakeTunnel) startCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.startCalls
}

func (t *fakeTunnel) stopCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stopCalls
}

func withStackTestHooks(t *testing.T, dialerFn func(string, time.Duration) (localproxy.Dialer, error), proxyFn func(localproxy.Dialer, localproxy.Options) httpProxy, tunnelFn func(config.Profile, int) (tunnel, error), waitFn func(string, time.Duration, tunnel) error) {
	t.Helper()

	prevDialer := newSOCKS5Dialer
	prevProxy := newHTTPProxy
	prevTunnel := newTunnelForStack
	prevWait := waitForTunnelReady
	prevSleep := sleepForRestart
	t.Cleanup(func() {
		newSOCKS5Dialer = prevDialer
		newHTTPProxy = prevProxy
		newTunnelForStack = prevTunnel
		waitForTunnelReady = prevWait
		sleepForRestart = prevSleep
	})

	if dialerFn != nil {
		newSOCKS5Dialer = dialerFn
	}
	if proxyFn != nil {
		newHTTPProxy = proxyFn
	}
	if tunnelFn != nil {
		newTunnelForStack = tunnelFn
	}
	if waitFn != nil {
		waitForTunnelReady = waitFn
	}
	sleepForRestart = func(time.Duration) {}
}

func TestStartSuccessWithInjectedDependencies(t *testing.T) {
	proxy := &fakeProxy{startAddr: "127.0.0.1:18080"}
	tun := newFakeTunnel(nil)
	withStackTestHooks(
		t,
		func(string, time.Duration) (localproxy.Dialer, error) { return fakeDialer{}, nil },
		func(localproxy.Dialer, localproxy.Options) httpProxy { return proxy },
		func(profile config.Profile, socksPort int) (tunnel, error) {
			if socksPort != 19090 {
				t.Fatalf("expected socks port 19090, got %d", socksPort)
			}
			return tun, nil
		},
		func(addr string, timeout time.Duration, got tunnel) error {
			if addr != "127.0.0.1:19090" {
				t.Fatalf("unexpected tunnel addr: %s", addr)
			}
			if timeout != 250*time.Millisecond {
				t.Fatalf("unexpected timeout: %s", timeout)
			}
			if got != tun {
				t.Fatalf("expected wait to receive created tunnel")
			}
			return nil
		},
	)

	profile := config.Profile{Host: "host", Port: 22, User: "user"}
	st, err := Start(profile, "inst-1", Options{
		SocksPort:         19090,
		SocksReadyTimeout: 250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}
	if st.HTTPPort != 18080 {
		t.Fatalf("expected http port 18080, got %d", st.HTTPPort)
	}
	if st.HTTPAddr != "127.0.0.1:18080" {
		t.Fatalf("unexpected http addr: %s", st.HTTPAddr)
	}
	if st.SocksPort != 19090 {
		t.Fatalf("unexpected socks port: %d", st.SocksPort)
	}
	if tun.startCount() != 1 {
		t.Fatalf("expected tunnel Start once, got %d", tun.startCount())
	}

	if err := st.Close(context.Background()); err != nil {
		t.Fatalf("Close error: %v", err)
	}
}

func TestStartClosesProxyWhenTunnelCreationFails(t *testing.T) {
	proxy := &fakeProxy{startAddr: "127.0.0.1:18080"}
	withStackTestHooks(
		t,
		func(string, time.Duration) (localproxy.Dialer, error) { return fakeDialer{}, nil },
		func(localproxy.Dialer, localproxy.Options) httpProxy { return proxy },
		func(config.Profile, int) (tunnel, error) { return nil, errors.New("boom") },
		nil,
	)

	profile := config.Profile{Host: "host", Port: 22, User: "user"}
	_, err := Start(profile, "inst-1", Options{SocksPort: 19090})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected tunnel creation error, got %v", err)
	}
	if proxy.closeCalls != 1 {
		t.Fatalf("expected proxy Close once, got %d", proxy.closeCalls)
	}
}

func TestStartStopsTunnelAndClosesProxyWhenWaitFails(t *testing.T) {
	proxy := &fakeProxy{startAddr: "127.0.0.1:18080"}
	tun := newFakeTunnel(nil)
	withStackTestHooks(
		t,
		func(string, time.Duration) (localproxy.Dialer, error) { return fakeDialer{}, nil },
		func(localproxy.Dialer, localproxy.Options) httpProxy { return proxy },
		func(config.Profile, int) (tunnel, error) { return tun, nil },
		func(string, time.Duration, tunnel) error { return errors.New("not ready") },
	)

	profile := config.Profile{Host: "host", Port: 22, User: "user"}
	_, err := Start(profile, "inst-1", Options{SocksPort: 19090})
	if err == nil || err.Error() != "not ready" {
		t.Fatalf("expected readiness error, got %v", err)
	}
	if tun.stopCount() != 1 {
		t.Fatalf("expected tunnel Stop once, got %d", tun.stopCount())
	}
	if proxy.closeCalls != 1 {
		t.Fatalf("expected proxy Close once, got %d", proxy.closeCalls)
	}
}

func TestCloseReturnsFirstError(t *testing.T) {
	tun := newFakeTunnel(nil)
	tun.stopErr = errors.New("stop failed")
	proxy := &fakeProxy{closeErr: errors.New("close failed")}
	s := &Stack{
		tunnel: tun,
		proxy:  proxy,
		stopCh: make(chan struct{}),
	}

	err := s.Close(context.Background())
	if err == nil || err.Error() != "stop failed" {
		t.Fatalf("expected first tunnel error, got %v", err)
	}
	if tun.stopCount() != 1 {
		t.Fatalf("expected tunnel Stop once, got %d", tun.stopCount())
	}
	if proxy.closeCalls != 1 {
		t.Fatalf("expected proxy Close once, got %d", proxy.closeCalls)
	}
}

func TestMonitorRestartsTunnelAfterExit(t *testing.T) {
	initial := newFakeTunnel(errors.New("initial exit"))
	restarted := newFakeTunnel(nil)
	initial.exit()

	withStackTestHooks(
		t,
		nil,
		nil,
		func(config.Profile, int) (tunnel, error) {
			return restarted, nil
		},
		func(string, time.Duration, tunnel) error { return nil },
	)

	s := &Stack{
		Profile:   config.Profile{Host: "host", Port: 22, User: "user"},
		SocksPort: 19090,
		tunnel:    initial,
		fatalCh:   make(chan error, 1),
		stopCh:    make(chan struct{}),
	}

	done := make(chan struct{})
	go func() {
		s.monitor(Options{MaxRestarts: 1})
		close(done)
	}()

	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if s.currentTunnel() == restarted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s.currentTunnel() != restarted {
		t.Fatalf("timeout waiting for tunnel restart")
	}
	select {
	case err := <-s.fatalCh:
		t.Fatalf("unexpected fatal error: %v", err)
	default:
	}
	if restarted.startCount() != 1 {
		t.Fatalf("expected restarted tunnel Start once, got %d", restarted.startCount())
	}

	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close error: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("monitor did not exit after Close")
	}
	if restarted.stopCount() != 1 {
		t.Fatalf("expected restarted tunnel to be stopped during Close, got %d", restarted.stopCount())
	}
}

func TestMonitorReportsFatalWhenRestartCreationFails(t *testing.T) {
	initial := newFakeTunnel(errors.New("initial exit"))
	initial.exit()
	withStackTestHooks(
		t,
		nil,
		nil,
		func(config.Profile, int) (tunnel, error) { return nil, errors.New("restart boom") },
		nil,
	)

	s := &Stack{
		Profile:   config.Profile{Host: "host", Port: 22, User: "user"},
		SocksPort: 19090,
		tunnel:    initial,
		fatalCh:   make(chan error, 1),
		stopCh:    make(chan struct{}),
	}

	go s.monitor(Options{MaxRestarts: 1})

	select {
	case err := <-s.fatalCh:
		if err == nil || err.Error() != "restart boom" {
			t.Fatalf("expected restart error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for fatal error")
	}
}
