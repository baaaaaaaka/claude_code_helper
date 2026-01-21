package config

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStore_LoadMissingReturnsDefault(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Version != CurrentVersion {
		t.Fatalf("Version=%d want %d", cfg.Version, CurrentVersion)
	}
}

func TestStore_SaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	in := Config{
		Version: CurrentVersion,
		Profiles: []Profile{
			{ID: "p1", Name: "n1", Host: "h", Port: 22, User: "u", CreatedAt: now},
		},
	}

	if err := store.Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}

	out, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.Version != CurrentVersion {
		t.Fatalf("Version=%d want %d", out.Version, CurrentVersion)
	}
	if len(out.Profiles) != 1 || out.Profiles[0].ID != "p1" {
		t.Fatalf("Profiles=%#v", out.Profiles)
	}
}

func TestStore_UpdateIsSerialized(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	const n = 25
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			errCh <- store.Update(func(cfg *Config) error {
				cfg.UpsertProfile(Profile{
					ID:        fmt.Sprintf("p%02d", i),
					Name:      "n",
					Host:      "h",
					Port:      22,
					User:      "u",
					CreatedAt: time.Now(),
				})
				return nil
			})
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
	}

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Profiles) != n {
		t.Fatalf("Profiles len=%d want %d", len(cfg.Profiles), n)
	}
}
