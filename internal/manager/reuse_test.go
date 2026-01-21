package manager

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/config"
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
