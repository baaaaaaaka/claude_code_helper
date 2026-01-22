package ssh

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
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
	if c.User == "" {
		return c.Host
	}
	return c.User + "@" + c.Host
}

func BuildArgs(c TunnelConfig) ([]string, error) {
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

	mu      sync.Mutex
	cmd     *exec.Cmd
	waitErr error
	done    chan struct{}
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
	t.cmd.Stderr = cfg.Stderr
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
	t.mu.Unlock()

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
	return t.waitErr
}

func (t *Tunnel) Stop(grace time.Duration) error {
	t.mu.Lock()
	cmd := t.cmd
	t.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return nil
	}

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
