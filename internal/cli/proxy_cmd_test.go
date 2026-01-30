package cli

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

func TestRunProxyDaemonErrors(t *testing.T) {
	t.Run("missing instance", func(t *testing.T) {
		store := newTempStore(t)
		if err := store.Save(config.Config{Version: config.CurrentVersion}); err != nil {
			t.Fatalf("save config: %v", err)
		}
		if err := runProxyDaemon(context.Background(), store, "missing"); err == nil {
			t.Fatalf("expected missing instance error")
		}
	})

	t.Run("missing profile", func(t *testing.T) {
		store := newTempStore(t)
		cfg := config.Config{
			Version: config.CurrentVersion,
			Instances: []config.Instance{{
				ID:        "inst-1",
				ProfileID: "profile-1",
			}},
		}
		if err := store.Save(cfg); err != nil {
			t.Fatalf("save config: %v", err)
		}
		if err := runProxyDaemon(context.Background(), store, "inst-1"); err == nil {
			t.Fatalf("expected missing profile error")
		}
	})
}

func TestProxyStopCmd(t *testing.T) {
	t.Run("missing instance returns error", func(t *testing.T) {
		store := newTempStore(t)
		if err := store.Save(config.Config{Version: config.CurrentVersion}); err != nil {
			t.Fatalf("save config: %v", err)
		}

		cmd := newProxyStopCmd(&rootOptions{configPath: store.Path()})
		cmd.SetArgs([]string{"missing"})
		if err := cmd.Execute(); err == nil {
			t.Fatalf("expected error for missing instance")
		}
	})

	t.Run("removes instance and prints", func(t *testing.T) {
		store := newTempStore(t)
		cfg := config.Config{
			Version:   config.CurrentVersion,
			Instances: []config.Instance{{ID: "inst-1", ProfileID: "p1"}},
		}
		if err := store.Save(cfg); err != nil {
			t.Fatalf("save config: %v", err)
		}

		cmd := newProxyStopCmd(&rootOptions{configPath: store.Path()})
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"inst-1"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute error: %v", err)
		}
		if !strings.Contains(out.String(), "Stopped instance inst-1") {
			t.Fatalf("unexpected output: %s", out.String())
		}

		loaded, err := store.Load()
		if err != nil {
			t.Fatalf("load config: %v", err)
		}
		if len(loaded.Instances) != 0 {
			t.Fatalf("expected instances to be removed, got %#v", loaded.Instances)
		}
	})
}

func TestProxyPruneCmdRemovesDead(t *testing.T) {
	store := newTempStore(t)
	cfg := config.Config{
		Version: config.CurrentVersion,
		Instances: []config.Instance{{
			ID:        "inst-1",
			ProfileID: "p1",
			DaemonPID: 0,
		}},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	cmd := newProxyPruneCmd(&rootOptions{configPath: store.Path()})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out.String(), "Pruned 1") {
		t.Fatalf("unexpected output: %s", out.String())
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(loaded.Instances) != 0 {
		t.Fatalf("expected instances to be pruned, got %#v", loaded.Instances)
	}
}

func TestProxyListCmdStatuses(t *testing.T) {
	store := newTempStore(t)
	cfg := config.Config{
		Version: config.CurrentVersion,
		Profiles: []config.Profile{{
			ID:   "profile-1",
			Name: "Profile One",
		}},
		Instances: []config.Instance{
			{ID: "dead-1", ProfileID: "profile-1", DaemonPID: 0},
			{ID: "alive-1", ProfileID: "profile-1", DaemonPID: os.Getpid()},
		},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	cmd := newProxyListCmd(&rootOptions{configPath: store.Path()})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "INSTANCE") || !strings.Contains(text, "STATUS") {
		t.Fatalf("expected header, got %s", text)
	}
	if !strings.Contains(text, "dead-1") || !strings.Contains(text, "dead") {
		t.Fatalf("expected dead instance row, got %s", text)
	}
	if !strings.Contains(text, "alive-1") || !strings.Contains(text, "alive") {
		t.Fatalf("expected alive instance row, got %s", text)
	}
	if !strings.Contains(text, "Profile One") {
		t.Fatalf("expected profile name mapping, got %s", text)
	}
}

func TestProxyDoctorCmdReportsIssues(t *testing.T) {
	store := newTempStore(t)
	t.Setenv("PATH", "")
	cmd := newProxyDoctorCmd(&rootOptions{configPath: store.Path()})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "Issues found:") {
		t.Fatalf("expected issues report, got %s", text)
	}
	if !strings.Contains(text, "missing `ssh`") {
		t.Fatalf("expected ssh missing message, got %s", text)
	}
	if !strings.Contains(text, "Install hints:") {
		t.Fatalf("expected install hints, got %s", text)
	}
}

func TestProxyListCmdMarksUnhealthy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/_claude_proxy/health", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = ln.Close()
	})
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port := 0
	if p, err := net.ResolveTCPAddr("tcp", "127.0.0.1:"+portStr); err == nil {
		port = p.Port
	}
	if port == 0 {
		t.Fatalf("failed to parse port")
	}

	store := newTempStore(t)
	cfg := config.Config{
		Version: config.CurrentVersion,
		Instances: []config.Instance{{
			ID:        "inst-1",
			ProfileID: "p1",
			DaemonPID: os.Getpid(),
			HTTPPort:  port,
		}},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	cmd := newProxyListCmd(&rootOptions{configPath: store.Path()})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out.String(), "unhealthy") {
		t.Fatalf("expected unhealthy status, got %s", out.String())
	}
}

func TestProxyStartCmdRequiresProfile(t *testing.T) {
	store := newTempStore(t)
	if err := store.Save(config.Config{Version: config.CurrentVersion}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	cmd := newProxyStartCmd(&rootOptions{configPath: store.Path()})
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error when no profiles exist")
	}
}

func TestProxyStartCmdUnknownProfile(t *testing.T) {
	store := newTempStore(t)
	cfg := config.Config{
		Version:  config.CurrentVersion,
		Profiles: []config.Profile{{ID: "p1", Name: "one"}},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	cmd := newProxyStartCmd(&rootOptions{configPath: store.Path()})
	cmd.SetArgs([]string{"missing"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error for unknown profile")
	}
}

func TestProxyStartCmdInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := newProxyStartCmd(&rootOptions{configPath: path})
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error for invalid config")
	}
}

func TestRunProxyDaemonStartError(t *testing.T) {
	store := newTempStore(t)
	cfg := config.Config{
		Version: config.CurrentVersion,
		Profiles: []config.Profile{{
			ID:   "p1",
			Name: "profile",
			Host: "host",
			Port: 22,
			User: "user",
		}},
		Instances: []config.Instance{{
			ID:        "inst-1",
			ProfileID: "p1",
		}},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	t.Setenv("PATH", "")

	if err := runProxyDaemon(context.Background(), store, "inst-1"); err == nil {
		t.Fatalf("expected error from stack.Start")
	}
}

func TestProxyStartCmdForegroundError(t *testing.T) {
	store := newTempStore(t)
	cfg := config.Config{
		Version: config.CurrentVersion,
		Profiles: []config.Profile{{
			ID:   "p1",
			Name: "profile",
			Host: "host",
			Port: 22,
			User: "user",
		}},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	t.Setenv("PATH", "")

	cmd := newProxyStartCmd(&rootOptions{configPath: store.Path()})
	cmd.SetArgs([]string{"--foreground", "p1"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected foreground start to fail without ssh")
	}
}

func TestProxyDaemonCmdRequiresInstanceID(t *testing.T) {
	store := newTempStore(t)
	cmd := newProxyDaemonCmd(&rootOptions{configPath: store.Path()})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error for missing instance-id flag")
	}
}
