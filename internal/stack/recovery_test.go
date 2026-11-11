package stack

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
	"github.com/baaaaaaaka/claude_code_helper/internal/localproxy"
	"github.com/baaaaaaaka/claude_code_helper/internal/ssh"
)

func TestMonitorCountsRapidReadyExits(t *testing.T) {
	initial := newFakeTunnel(errors.New("exit"))
	initial.exit()
	created := 0
	withStackTestHooks(t, nil, nil, func(config.Profile, int) (tunnel, error) {
		created++
		f := newFakeTunnel(errors.New("exit"))
		f.exit()
		return f, nil
	}, func(string, time.Duration, tunnel) error { return nil })
	s := &Stack{Profile: config.Profile{Host: "h", Port: 22, User: "u"}, SocksPort: 19090, tunnel: initial, stopCh: make(chan struct{}), fatalCh: make(chan error, 1)}
	done := make(chan struct{})
	go func() { s.monitor(Options{MaxRestarts: 3}); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("rapid ready/exit loop ignored restart budget")
	}
	if created != 3 {
		t.Fatalf("created %d restart tunnels, want 3", created)
	}
	_ = s.Close(context.Background())
}
func TestCloseOwnsTunnelDuringRestartReadiness(t *testing.T) {
	initial := newFakeTunnel(errors.New("exit"))
	initial.exit()
	next := newFakeTunnel(nil)
	waiting := make(chan struct{})
	withStackTestHooks(t, nil, nil, func(config.Profile, int) (tunnel, error) { return next, nil }, func(_ string, _ time.Duration, tun tunnel) error {
		close(waiting)
		<-tun.Done()
		return errors.New("stopped")
	})
	s := &Stack{Profile: config.Profile{Host: "h", Port: 22, User: "u"}, SocksPort: 19090, tunnel: initial, stopCh: make(chan struct{}), fatalCh: make(chan error, 1)}
	done := make(chan struct{})
	go func() { s.monitor(Options{MaxRestarts: 1}); close(done) }()
	select {
	case <-waiting:
	case <-time.After(time.Second):
		t.Fatal("restart did not reach readiness")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = s.Close(context.Background()) }()
	}
	wg.Wait()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel startup readiness")
	}
	if next.stopCount() < 1 {
		t.Fatalf("Stop calls %d", next.stopCount())
	}
}
func TestRestartBackoffIsCancellable(t *testing.T) {
	s := &Stack{stopCh: make(chan struct{})}
	done := make(chan bool, 1)
	go func() { done <- s.restartDelay(Options{RestartBackoff: time.Hour, MaxRestartBackoff: time.Hour}, 1) }()
	close(s.stopCh)
	select {
	case ready := <-done:
		if ready {
			t.Fatal("continued after close")
		}
	case <-time.After(time.Second):
		t.Fatal("backoff not cancelled")
	}
}
func TestStartRetriesOnlyAutomaticBindConflicts(t *testing.T) {
	for _, fixed := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic", true: "fixed"}[fixed], func(t *testing.T) {
			calls := 0
			p := &fakeProxy{startAddr: "127.0.0.1:18080"}
			withStackTestHooks(t, nil, func(localproxy.Dialer, localproxy.Options) httpProxy { return p }, func(config.Profile, int) (tunnel, error) { calls++; return newFakeTunnel(nil), nil }, func(string, time.Duration, tunnel) error { return ssh.ErrBindConflict })
			opts := Options{}
			if fixed {
				opts.SocksPort = 19090
			}
			_, err := Start(config.Profile{Host: "h", Port: 22, User: "u"}, "id", opts)
			if !errors.Is(err, ssh.ErrBindConflict) {
				t.Fatalf("error %v", err)
			}
			want := 3
			if fixed {
				want = 1
			}
			if calls != want {
				t.Fatalf("startup calls %d, want %d", calls, want)
			}
		})
	}
}
