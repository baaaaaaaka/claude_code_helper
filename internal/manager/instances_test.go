package manager

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

func TestInstanceRecordHeartbeatRemove(t *testing.T) {
	dir := t.TempDir()
	store, err := config.NewStore(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	inst := config.Instance{
		ID:         "i1",
		ProfileID:  "p1",
		HTTPPort:   8080,
		SocksPort:  1080,
		DaemonPID:  123,
		StartedAt:  time.Now().Add(-1 * time.Minute),
		LastSeenAt: time.Now().Add(-1 * time.Minute),
	}

	if err := RecordInstance(store, inst); err != nil {
		t.Fatalf("RecordInstance: %v", err)
	}

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Instances) != 1 || cfg.Instances[0].ID != "i1" {
		t.Fatalf("Instances=%#v", cfg.Instances)
	}

	now := time.Now()
	if err := Heartbeat(store, "i1", now); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	cfg, _ = store.Load()
	if !cfg.Instances[0].LastSeenAt.Equal(now) {
		t.Fatalf("LastSeenAt=%s want %s", cfg.Instances[0].LastSeenAt, now)
	}

	if err := RemoveInstance(store, "i1"); err != nil {
		t.Fatalf("RemoveInstance: %v", err)
	}
	cfg, _ = store.Load()
	if len(cfg.Instances) != 0 {
		t.Fatalf("expected empty instances, got %#v", cfg.Instances)
	}
}
