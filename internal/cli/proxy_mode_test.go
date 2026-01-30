package cli

import (
	"bufio"
	"context"
	"io"
	"os"
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

func TestEnsureProxyPreferenceWriteFailure(t *testing.T) {
	store := newTempStore(t)
	lockPath := store.Path() + ".lock"
	if err := os.WriteFile(lockPath, []byte(""), 0o600); err != nil {
		t.Fatalf("write lock file: %v", err)
	}
	dir := filepath.Dir(store.Path())
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	reader := bufio.NewReader(strings.NewReader("y\n"))
	_, _, err := ensureProxyPreferenceWithReader(context.Background(), store, "", io.Discard, reader)
	if err == nil {
		t.Fatalf("expected error when config dir is read-only")
	}
}

func TestEnsureProxyPreferenceUsesStdin(t *testing.T) {
	store := newTempStore(t)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	prevStdin := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() { os.Stdin = prevStdin })

	if _, err := writer.Write([]byte("y\n")); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	_ = writer.Close()

	enabled, _, err := ensureProxyPreference(context.Background(), store, "", io.Discard)
	if err != nil {
		t.Fatalf("ensureProxyPreference error: %v", err)
	}
	if !enabled {
		t.Fatalf("expected proxy enabled from stdin input")
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
