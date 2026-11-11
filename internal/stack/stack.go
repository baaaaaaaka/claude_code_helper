package stack

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
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
)

type Options struct {
	SocksPort      int
	HTTPListenAddr string

	SocksReadyTimeout time.Duration

	MaxRestarts       int
	RestartBackoff    time.Duration
	TunnelStopGrace   time.Duration
	StableWindow      time.Duration
	MaxRestartBackoff time.Duration
}

type Stack struct {
	InstanceID string
	Profile    config.Profile

	SocksPort int
	HTTPAddr  string
	HTTPPort  int

	mu        sync.Mutex
	proxy     httpProxy
	tunnel    tunnel
	closed    bool
	closeOnce sync.Once
	closeErr  error

	fatalCh chan error
	stopCh  chan struct{}
}

// ValidateProfile returns an error if the profile is missing required fields
// for starting a proxy stack (host, port, user).
func ValidateProfile(p config.Profile) error {
	if p.Host == "" {
		return errors.New("profile host is required")
	}
	if p.Port <= 0 {
		return errors.New("profile port is required")
	}
	if p.User == "" {
		return errors.New("profile user is required")
	}
	return nil
}

func Start(profile config.Profile, instanceID string, opts Options) (*Stack, error) {
	for attempt := 0; ; attempt++ {
		s, err := startOnce(profile, instanceID, opts)
		if err == nil || opts.SocksPort != 0 || !errors.Is(err, ssh.ErrBindConflict) || attempt >= 2 {
			return s, err
		}
	}
}
func startOnce(profile config.Profile, instanceID string, opts Options) (*Stack, error) {
	if err := ValidateProfile(profile); err != nil {
		return nil, err
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

	httpAddr, err := hp.Start(opts.HTTPListenAddr)
	if err != nil {
		_ = tun.Stop(opts.TunnelStopGrace)
		_ = hp.Close(context.Background())
		return nil, err
	}
	_, portStr, err := net.SplitHostPort(httpAddr)
	if err != nil {
		_ = tun.Stop(opts.TunnelStopGrace)
		_ = hp.Close(context.Background())
		return nil, err
	}
	httpPort, err := parsePort(portStr)
	if err != nil {
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
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		if s.stopCh != nil {
			close(s.stopCh)
		}
		tun, proxy := s.tunnel, s.proxy
		s.tunnel = nil
		s.proxy = nil
		s.mu.Unlock()
		var proxyErr error
		if proxy != nil {
			proxyErr = proxy.Close(ctx)
		}
		if tun != nil {
			s.closeErr = tun.Stop(2 * time.Second)
		}
		if s.closeErr == nil {
			s.closeErr = proxyErr
		}
	})
	return s.closeErr
}

func (s *Stack) backend(state string) {
	s.mu.Lock()
	p := s.proxy
	s.mu.Unlock()
	if p, ok := p.(interface{ SetBackendState(string) }); ok {
		p.SetBackendState(state)
	}
}
func (s *Stack) restartDelay(opts Options, failures int) bool {
	d := opts.RestartBackoff
	capDelay := opts.MaxRestartBackoff
	if capDelay <= 0 {
		capDelay = 10 * time.Second
	}
	for i := 1; i < failures && d < capDelay; i++ {
		if d > capDelay/2 {
			d = capDelay
		} else {
			d *= 2
		}
	}
	if d > capDelay {
		d = capDelay
	}
	if d > 0 {
		d = d*4/5 + time.Duration(rand.Int64N(int64(d/5)+1))
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-s.stopCh:
		return false
	case <-timer.C:
		return !s.stopRequested()
	}
}
func (s *Stack) monitor(opts Options) {
	failures := 0
	started := time.Now()
	stable := opts.StableWindow
	if stable <= 0 {
		stable = 30 * time.Second
	}
	for {
		current := s.currentTunnel()
		if current == nil {
			return
		}
		err := current.Wait()
		if s.stopRequested() {
			return
		}
		if time.Since(started) >= stable {
			failures = 0
		}
		s.backend("recovering")
		for {
			failures++
			if failures > opts.MaxRestarts {
				s.backend("failed")
				s.fatalCh <- fmt.Errorf("ssh tunnel exited too many times: %w", err)
				return
			}
			if !s.restartDelay(opts, failures) {
				return
			}
			tun, terr := newTunnelForStack(s.Profile, s.SocksPort)
			if terr == nil {
				// Publish even a starting tunnel so Close owns and stops it while readiness waits.
				s.mu.Lock()
				if s.closed {
					s.mu.Unlock()
					return
				}
				terr = tun.Start()
				if terr == nil {
					s.tunnel = tun
				}
				s.mu.Unlock()
				if terr == nil {
					terr = waitForTunnelReady(fmt.Sprintf("127.0.0.1:%d", s.SocksPort), opts.SocksReadyTimeout, tun)
					if terr != nil {
						_ = tun.Stop(opts.TunnelStopGrace)
					}
				}
			}
			if s.stopRequested() {
				return
			}
			if terr == nil {
				started = time.Now()
				s.backend("ready")
				break
			}
			err = terr
			if failures >= opts.MaxRestarts {
				s.backend("failed")
				s.fatalCh <- terr
				return
			}
		}
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
		ExtraArgs: append([]string{"-o", "ControlMaster=no", "-o", "ControlPath=none", "-v"}, profile.SSHArgs...),
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

		if own, ok := tun.(interface{ BindingReady() bool }); ok && !own.BindingReady() {
			time.Sleep(25 * time.Millisecond)
			continue
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
