package manager

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

func TestFindReusableInstance_PicksMostRecentHealthy(t *testing.T) {
	port1, close1 := startHealthServer(t, "inst-a")
	defer close1()
	port2, close2 := startHealthServer(t, "inst-b")
	defer close2()

	pid := os.Getpid()
	now := time.Now()

	instances := []config.Instance{
		{
			ID:         "inst-a",
			ProfileID:  "prof-1",
			HTTPPort:   port1,
			DaemonPID:  pid,
			LastSeenAt: now.Add(-1 * time.Minute),
		},
		{
			ID:         "inst-b",
			ProfileID:  "prof-1",
			HTTPPort:   port2,
			DaemonPID:  pid,
			LastSeenAt: now,
		},
	}

	got := FindReusableInstance(instances, "prof-1", HealthClient{Timeout: 500 * time.Millisecond})
	if got == nil {
		t.Fatalf("expected an instance")
	}
	if got.ID != "inst-b" {
		t.Fatalf("got %q want inst-b", got.ID)
	}
}

func TestFindReusableInstance_IgnoresWrongInstanceID(t *testing.T) {
	port, closeFn := startHealthServer(t, "different-id")
	defer closeFn()

	instances := []config.Instance{
		{
			ID:         "inst-a",
			ProfileID:  "prof-1",
			HTTPPort:   port,
			DaemonPID:  os.Getpid(),
			LastSeenAt: time.Now(),
		},
	}

	got := FindReusableInstance(instances, "prof-1", HealthClient{Timeout: 500 * time.Millisecond})
	if got != nil {
		t.Fatalf("expected nil, got %q", got.ID)
	}
}

func TestIsInstanceStale(t *testing.T) {
	now := time.Now()
	inst := config.Instance{LastSeenAt: now.Add(-10 * time.Minute)}
	if !IsInstanceStale(inst, now, 5*time.Minute) {
		t.Fatalf("expected instance to be stale")
	}
	if IsInstanceStale(inst, now, 0) {
		t.Fatalf("expected maxAge<=0 to disable stale check")
	}
	if IsInstanceStale(config.Instance{}, now, 5*time.Minute) {
		t.Fatalf("expected zero LastSeenAt to be treated as fresh")
	}
}

func TestFindReusableInstanceSkipsUnhealthy(t *testing.T) {
	t.Run("no instances", func(t *testing.T) {
		if got := FindReusableInstance(nil, "prof-1", HealthClient{}); got != nil {
			t.Fatalf("expected nil for empty instances")
		}
	})

	t.Run("skips missing pid", func(t *testing.T) {
		instances := []config.Instance{{ID: "inst", ProfileID: "prof-1", HTTPPort: 1, DaemonPID: 0}}
		if got := FindReusableInstance(instances, "prof-1", HealthClient{}); got != nil {
			t.Fatalf("expected nil for missing pid")
		}
	})

	t.Run("skips failed health check", func(t *testing.T) {
		instances := []config.Instance{{
			ID:         "inst",
			ProfileID:  "prof-1",
			HTTPPort:   1,
			DaemonPID:  os.Getpid(),
			LastSeenAt: time.Now(),
		}}
		if got := FindReusableInstance(instances, "prof-1", HealthClient{Timeout: 50 * time.Millisecond}); got != nil {
			t.Fatalf("expected nil for failed health check")
		}
	})

	t.Run("keeps first when timestamps equal", func(t *testing.T) {
		port, closeFn := startHealthServer(t, "inst-1")
		defer closeFn()
		now := time.Now()
		instances := []config.Instance{
			{ID: "inst-1", ProfileID: "prof-1", HTTPPort: port, DaemonPID: os.Getpid(), LastSeenAt: now},
			{ID: "inst-2", ProfileID: "prof-1", HTTPPort: port, DaemonPID: os.Getpid(), LastSeenAt: now},
		}
		got := FindReusableInstance(instances, "prof-1", HealthClient{Timeout: 500 * time.Millisecond})
		if got == nil || got.ID != "inst-1" {
			t.Fatalf("expected first instance to be chosen, got %#v", got)
		}
	})
}

func startHealthServer(t *testing.T, instanceID string) (port int, closeFn func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/_claude_proxy/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":         true,
			"instanceId": instanceID,
		})
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	p, err := net.ResolveTCPAddr("tcp", "127.0.0.1:"+portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	return p.Port, func() {
		_ = srv.Shutdown(context.Background())
		_ = ln.Close()
	}
}
