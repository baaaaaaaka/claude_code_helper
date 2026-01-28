package cli

import (
	"bufio"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

func TestEnsureProxyPreferenceRespectsExistingValue(t *testing.T) {
	store := newTempStore(t)
	enabled := true
	if err := store.Save(config.Config{Version: config.CurrentVersion, ProxyEnabled: &enabled}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	got, cfg, err := ensureProxyPreferenceWithReader(context.Background(), store, "", io.Discard, bufio.NewReader(strings.NewReader("n\n")))
	if err != nil {
		t.Fatalf("ensureProxyPreference error: %v", err)
	}
	if !got || cfg.ProxyEnabled == nil || !*cfg.ProxyEnabled {
		t.Fatalf("expected proxy enabled from config")
	}
}

func TestEnsureProxyPreferencePromptsWhenNoProfiles(t *testing.T) {
	store := newTempStore(t)

	got, _, err := ensureProxyPreferenceWithReader(context.Background(), store, "", io.Discard, bufio.NewReader(strings.NewReader("y\n")))
	if err != nil {
		t.Fatalf("ensureProxyPreference error: %v", err)
	}
	if !got {
		t.Fatalf("expected proxy enabled from prompt")
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.ProxyEnabled == nil || !*cfg.ProxyEnabled {
		t.Fatalf("expected proxy enabled in config")
	}
}

func TestEnsureProxyPreferenceDefaultsToProxyWhenProfilesExist(t *testing.T) {
	store := newTempStore(t)
	if err := store.Save(config.Config{
		Version:  config.CurrentVersion,
		Profiles: []config.Profile{{ID: "p1", Name: "p1"}},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	got, cfg, err := ensureProxyPreferenceWithReader(context.Background(), store, "", io.Discard, bufio.NewReader(strings.NewReader("n\n")))
	if err != nil {
		t.Fatalf("ensureProxyPreference error: %v", err)
	}
	if !got || cfg.ProxyEnabled == nil || !*cfg.ProxyEnabled {
		t.Fatalf("expected proxy enabled when profiles exist")
	}
}

func newTempStore(t *testing.T) *config.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	store, err := config.NewStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return store
}
