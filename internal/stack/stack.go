package stack

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
	"github.com/baaaaaaaka/claude_code_helper/internal/localproxy"
	"github.com/baaaaaaaka/claude_code_helper/internal/ssh"
)

type httpProxy interface {
	Start(listenAddr string) (string, error)
	Close(ctx context.Context) error
}

type tunnel interface {
	Start() error
	Stop(grace time.Duration) error
	Wait() error
	Done() <-chan struct{}
}

var (
	newSOCKS5Dialer    = localproxy.NewSOCKS5Dialer
	newHTTPProxy       = func(d localproxy.Dialer, opts localproxy.Options) httpProxy { return localproxy.NewHTTPProxy(d, opts) }
	newTunnelForStack  = func(profile config.Profile, socksPort int) (tunnel, error) { return newTunnel(profile, socksPort) }
	waitForTunnelReady = waitForTCPTunnel
	sleepForRestart    = time.Sleep
)

type Options struct {
	SocksPort      int
	HTTPListenAddr string

	SocksReadyTimeout time.Duration

	MaxRestarts     int
	RestartBackoff  time.Duration
	TunnelStopGrace time.Duration
}

type Stack struct {
	InstanceID string
	Profile    config.Profile

	SocksPort int
	HTTPAddr  string
	HTTPPort  int

	mu     sync.Mutex
	proxy  httpProxy
	tunnel tunnel
	closed bool

	fatalCh chan error
	stopCh  chan struct{}
}

func Start(profile config.Profile, instanceID string, opts Options) (*Stack, error) {
	if profile.Host == "" {
		return nil, errors.New("profile host is required")
	}
	if profile.Port <= 0 {
		return nil, errors.New("profile port is required")
	}
	if profile.User == "" {
		return nil, errors.New("profile user is required")
	}
	if instanceID == "" {
		return nil, errors.New("instance id is required")
	}

	if opts.HTTPListenAddr == "" {
		opts.HTTPListenAddr = "127.0.0.1:0"
	}
	if opts.MaxRestarts <= 0 {
		opts.MaxRestarts = 3
	}
	if opts.RestartBackoff <= 0 {
		opts.RestartBackoff = 1 * time.Second
	}
	if opts.TunnelStopGrace <= 0 {
		opts.TunnelStopGrace = 2 * time.Second
	}
	if opts.SocksReadyTimeout <= 0 {
		opts.SocksReadyTimeout = 30 * time.Second
	}

	socksPort := opts.SocksPort
	if socksPort == 0 {
		p, err := pickFreePort()
		if err != nil {
			return nil, err
		}
		socksPort = p
	}

	socksAddr := fmt.Sprintf("127.0.0.1:%d", socksPort)
	dialer, err := newSOCKS5Dialer(socksAddr, 10*time.Second)
	if err != nil {
		return nil, err
	}

	hp := newHTTPProxy(dialer, localproxy.Options{InstanceID: instanceID})
	httpAddr, err := hp.Start(opts.HTTPListenAddr)
	if err != nil {
		return nil, err
	}
	_, portStr, err := net.SplitHostPort(httpAddr)
	if err != nil {
		_ = hp.Close(context.Background())
		return nil, err
	}
	httpPort, err := parsePort(portStr)
	if err != nil {
		_ = hp.Close(context.Background())
		return nil, err
	}

	tun, err := newTunnelForStack(profile, socksPort)
	if err != nil {
		_ = hp.Close(context.Background())
		return nil, err
	}
	if err := tun.Start(); err != nil {
		_ = hp.Close(context.Background())
		return nil, err
	}
	if err := waitForTunnelReady(socksAddr, opts.SocksReadyTimeout, tun); err != nil {
		_ = tun.Stop(opts.TunnelStopGrace)
		_ = hp.Close(context.Background())
		return nil, err
	}

	s := &Stack{
		InstanceID: instanceID,
		Profile:    profile,
		SocksPort:  socksPort,
		HTTPAddr:   httpAddr,
		HTTPPort:   httpPort,
		proxy:      hp,
		tunnel:     tun,
		fatalCh:    make(chan error, 1),
		stopCh:     make(chan struct{}),
	}

	go s.monitor(opts)
	return s, nil
}

func (s *Stack) HTTPProxyURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.HTTPPort)
}

func (s *Stack) Fatal() <-chan error { return s.fatalCh }

func (s *Stack) Close(ctx context.Context) error {
	select {
	case <-s.stopCh:
		// already closed
	default:
		close(s.stopCh)
	}

	s.mu.Lock()
	s.closed = true
	tun := s.tunnel
	proxy := s.proxy
	s.tunnel = nil
	s.proxy = nil
	s.mu.Unlock()

	var firstErr error
	if tun != nil {
		if err := tun.Stop(2 * time.Second); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if proxy != nil {
		if err := proxy.Close(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Stack) monitor(opts Options) {
	restarts := 0
	for {
		current := s.currentTunnel()
		if current == nil {
			return
		}
		err := current.Wait()

		if s.stopRequested() {
			return
		}

		restarts++
		if restarts > opts.MaxRestarts {
			s.fatalCh <- fmt.Errorf("ssh tunnel exited too many times: %w", err)
			return
		}

		sleepForRestart(opts.RestartBackoff)
		if s.stopRequested() {
			return
		}

		tun, terr := newTunnelForStack(s.Profile, s.SocksPort)
		if terr != nil {
			s.fatalCh <- terr
			return
		}
		if s.stopRequested() {
			return
		}
		if terr := tun.Start(); terr != nil {
			s.fatalCh <- terr
			return
		}
		if s.stopRequested() {
			_ = tun.Stop(opts.TunnelStopGrace)
			return
		}
		if terr := waitForTunnelReady(fmt.Sprintf("127.0.0.1:%d", s.SocksPort), opts.SocksReadyTimeout, tun); terr != nil {
			_ = tun.Stop(opts.TunnelStopGrace)
			s.fatalCh <- terr
			return
		}

		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = tun.Stop(opts.TunnelStopGrace)
			return
		}
		s.tunnel = tun
		s.mu.Unlock()
		restarts = 0
	}
}

func (s *Stack) currentTunnel() tunnel {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tunnel
}

func (s *Stack) stopRequested() bool {
	select {
	case <-s.stopCh:
		return true
	default:
		return false
	}
}

func newTunnel(profile config.Profile, socksPort int) (*ssh.Tunnel, error) {
	return ssh.NewTunnel(ssh.TunnelConfig{
		Host:      profile.Host,
		Port:      profile.Port,
		User:      profile.User,
		SocksPort: socksPort,
		ExtraArgs: profile.SSHArgs,
		BatchMode: true,
		Stdout:    os.Stderr,
		Stderr:    os.Stderr,
	})
}

func waitForTCP(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return nil
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr != nil {
		return fmt.Errorf("timeout waiting for %s: %w", addr, lastErr)
	}
	return fmt.Errorf("timeout waiting for %s", addr)
}

func waitForTCPTunnel(addr string, timeout time.Duration, tun tunnel) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if tun != nil {
			select {
			case <-tun.Done():
				return fmt.Errorf("ssh tunnel exited before SOCKS ready: %w", tun.Wait())
			default:
			}
		}

		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return nil
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr != nil {
		return fmt.Errorf("timeout waiting for %s: %w", addr, lastErr)
	}
	return fmt.Errorf("timeout waiting for %s", addr)
}

func pickFreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()

	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		return 0, err
	}
	return parsePort(portStr)
}

func parsePort(s string) (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:"+s)
	if err != nil {
		return 0, err
	}
	return addr.Port, nil
}
