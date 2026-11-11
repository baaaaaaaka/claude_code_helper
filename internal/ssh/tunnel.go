package ssh

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type TunnelConfig struct {
	Host      string
	Port      int
	User      string
	SocksPort int

	// ExtraArgs are appended before the destination argument.
	ExtraArgs []string

	// BatchMode enables non-interactive SSH behavior (recommended for tunnels).
	BatchMode bool

	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

func (c TunnelConfig) destination() string {
	host := strings.TrimSpace(c.Host)
	user := strings.TrimSpace(c.User)
	if user == "" {
		return host
	}
	return user + "@" + host
}

func BuildArgs(c TunnelConfig) ([]string, error) {
	c.Host = strings.TrimSpace(c.Host)
	c.User = strings.TrimSpace(c.User)
	if c.Host == "" {
		return nil, errors.New("host is required")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return nil, fmt.Errorf("invalid ssh port %d", c.Port)
	}
	if c.SocksPort <= 0 || c.SocksPort > 65535 {
		return nil, fmt.Errorf("invalid socks port %d", c.SocksPort)
	}

	args := []string{
		"-N",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ConnectTimeout=15",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "TCPKeepAlive=yes",
		"-p", strconv.Itoa(c.Port),
		"-D", "127.0.0.1:" + strconv.Itoa(c.SocksPort),
	}

	if c.BatchMode {
		args = append(args, "-o", "BatchMode=yes")
	}

	args = append(args, c.ExtraArgs...)
	args = append(args, c.destination())
	return args, nil
}

type Tunnel struct {
	cfg TunnelConfig

	mu           sync.Mutex
	cmd          *exec.Cmd
	waitErr      error
	done         chan struct{}
	diagnosticMu sync.Mutex
	diagnostic   string
	bindingReady bool
	stopOnce     sync.Once
	stopErr      error
}

func NewTunnel(cfg TunnelConfig) (*Tunnel, error) {
	args, err := BuildArgs(cfg)
	if err != nil {
		return nil, err
	}

	t := &Tunnel{
		cfg:  cfg,
		cmd:  exec.Command("ssh", args...),
		done: make(chan struct{}),
	}
	t.cmd.Stdin = cfg.Stdin
	t.cmd.Stdout = cfg.Stdout
	t.cmd.Stderr = tunnelDiagnostics{t: t}
	return t, nil
}

func (t *Tunnel) PID() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cmd == nil || t.cmd.Process == nil {
		return 0
	}
	return t.cmd.Process.Pid
}

func (t *Tunnel) Start() error {
	t.mu.Lock()
	cmd := t.cmd
	defer t.mu.Unlock()

	if cmd == nil {
		return errors.New("tunnel not initialized")
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	go func() {
		err := cmd.Wait()
		t.mu.Lock()
		t.waitErr = err
		t.mu.Unlock()
		close(t.done)
	}()

	return nil
}

func (t *Tunnel) Done() <-chan struct{} { return t.done }

func (t *Tunnel) Wait() error {
	<-t.done
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.waitErr != nil && t.BindConflict() {
		return fmt.Errorf("%w: %v", ErrBindConflict, t.waitErr)
	}
	return t.waitErr
}

var ErrBindConflict = errors.New("SSH SOCKS port already in use")

type tunnelDiagnostics struct{ t *Tunnel }

func (w tunnelDiagnostics) Write(b []byte) (int, error) {
	t := w.t
	t.diagnosticMu.Lock()
	t.diagnostic += string(b)
	if len(t.diagnostic) > 8192 {
		t.diagnostic = t.diagnostic[len(t.diagnostic)-8192:]
	}
	if strings.Contains(t.diagnostic, "Local forwarding listening on 127.0.0.1 port "+strconv.Itoa(t.cfg.SocksPort)+".") {
		t.bindingReady = true
	}
	t.diagnosticMu.Unlock()
	// Keep operational errors, not the verbose protocol trace used for startup confirmation.
	if t.cfg.Stderr != nil {
		for _, line := range strings.SplitAfter(string(b), "\n") {
			if !strings.HasPrefix(line, "debug") {
				_, _ = io.WriteString(t.cfg.Stderr, line)
			}
		}
	}
	return len(b), nil
}
func (t *Tunnel) BindingReady() bool {
	t.diagnosticMu.Lock()
	defer t.diagnosticMu.Unlock()
	return t.bindingReady
}
func (t *Tunnel) BindConflict() bool {
	t.diagnosticMu.Lock()
	defer t.diagnosticMu.Unlock()
	s := strings.ToLower(t.diagnostic)
	return strings.Contains(s, "address already in use") || strings.Contains(s, "only one usage of each socket address")
}

func (t *Tunnel) Stop(grace time.Duration) error {
	t.mu.Lock()
	cmd := t.cmd
	t.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return nil
	}

	t.stopOnce.Do(func() { t.stopErr = t.stopStarted(cmd, grace) })
	return t.stopErr
}
func (t *Tunnel) stopStarted(cmd *exec.Cmd, grace time.Duration) error {
	_ = cmd.Process.Signal(os.Interrupt)

	select {
	case <-t.done:
		return t.Wait()
	case <-time.After(grace):
		_ = cmd.Process.Kill()
		<-t.done
		return t.Wait()
	}
}
