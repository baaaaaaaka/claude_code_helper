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

	pref, err := ensureProxyPreferenceWithReader(context.Background(), store, "", io.Discard, bufio.NewReader(strings.NewReader("n\n")))
	if err != nil {
		t.Fatalf("ensureProxyPreference error: %v", err)
	}
	if !pref.Enabled || pref.Cfg.ProxyEnabled == nil || !*pref.Cfg.ProxyEnabled {
		t.Fatalf("expected proxy enabled from config")
	}
	if pref.NeedsPersist {
		t.Fatalf("expected NeedsPersist=false when value already on disk")
	}
}

func TestEnsureProxyPreferencePromptsWhenNoProfiles(t *testing.T) {
	store := newTempStore(t)

	pref, err := ensureProxyPreferenceWithReader(context.Background(), store, "", io.Discard, bufio.NewReader(strings.NewReader("y\n")))
	if err != nil {
		t.Fatalf("ensureProxyPreference error: %v", err)
	}
	if !pref.Enabled {
		t.Fatalf("expected proxy enabled from prompt")
	}
	if !pref.NeedsPersist {
		t.Fatalf("expected NeedsPersist=true for fresh preference")
	}
	// Preference should NOT be persisted yet — callers persist after full setup.
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.ProxyEnabled != nil {
		t.Fatalf("expected proxy preference not persisted until setup completes")
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

	pref, err := ensureProxyPreferenceWithReader(context.Background(), store, "", io.Discard, bufio.NewReader(strings.NewReader("n\n")))
	if err != nil {
		t.Fatalf("ensureProxyPreference error: %v", err)
	}
	if !pref.Enabled || pref.Cfg.ProxyEnabled == nil || !*pref.Cfg.ProxyEnabled {
		t.Fatalf("expected proxy enabled when profiles exist")
	}
	if !pref.NeedsPersist {
		t.Fatalf("expected NeedsPersist=true when inferred from profiles")
	}
}

func TestEnsureProxyPreferenceDoesNotPersist(t *testing.T) {
	store := newTempStore(t)

	reader := bufio.NewReader(strings.NewReader("y\n"))
	pref, err := ensureProxyPreferenceWithReader(context.Background(), store, "", io.Discard, reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !pref.Enabled {
		t.Fatalf("expected proxy enabled from prompt")
	}

	// Verify nothing was written to disk.
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.ProxyEnabled != nil {
		t.Fatalf("expected proxy preference not persisted by ensureProxyPreference")
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

	pref, err := ensureProxyPreference(context.Background(), store, "", io.Discard)
	if err != nil {
		t.Fatalf("ensureProxyPreference error: %v", err)
	}
	if !pref.Enabled {
		t.Fatalf("expected proxy enabled from stdin input")
	}
}

// Simulates the bug scenario: user picks proxy=yes, but profile setup fails
// before persistProxyPreference is called. On re-entry the user should be
// prompted again because nothing was written to disk.
func TestProxyPreferenceNotPersistedWhenSetupIncomplete(t *testing.T) {
	store := newTempStore(t)

	// First call: user answers "yes".
	pref, err := ensureProxyPreferenceWithReader(
		context.Background(), store, "", io.Discard,
		bufio.NewReader(strings.NewReader("y\n")),
	)
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}
	if !pref.Enabled {
		t.Fatalf("expected proxy enabled")
	}

	// Simulate: profile setup fails here — persistProxyPreference is NOT called.

	// Second call: because nothing was persisted, user should be prompted again.
	// This time user answers "no".
	pref2, err := ensureProxyPreferenceWithReader(
		context.Background(), store, "", io.Discard,
		bufio.NewReader(strings.NewReader("n\n")),
	)
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}
	if pref2.Enabled {
		t.Fatalf("expected proxy disabled on second prompt")
	}
}

// After successful setup, the caller persists the preference. Subsequent calls
// must return the persisted value without prompting.
func TestProxyPreferencePersistedAfterSuccessfulSetup(t *testing.T) {
	store := newTempStore(t)

	// User answers "yes".
	pref, err := ensureProxyPreferenceWithReader(
		context.Background(), store, "", io.Discard,
		bufio.NewReader(strings.NewReader("y\n")),
	)
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}
	if !pref.Enabled {
		t.Fatalf("expected proxy enabled")
	}

	// Simulate successful profile setup → caller persists.
	if err := persistProxyPreference(store, true); err != nil {
		t.Fatalf("persist error: %v", err)
	}

	// Second call: should return true without prompting (reader has "n" to
	// detect if it accidentally prompts).
	pref2, err := ensureProxyPreferenceWithReader(
		context.Background(), store, "", io.Discard,
		bufio.NewReader(strings.NewReader("n\n")),
	)
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}
	if !pref2.Enabled {
		t.Fatalf("expected persisted proxy preference to be respected")
	}
	if pref2.NeedsPersist {
		t.Fatalf("expected NeedsPersist=false after preference already on disk")
	}
}

// When user chooses no, the caller persists false. Subsequent calls return
// false without prompting.
func TestProxyPreferenceNoPersistedCorrectly(t *testing.T) {
	store := newTempStore(t)

	// User answers "no".
	pref, err := ensureProxyPreferenceWithReader(
		context.Background(), store, "", io.Discard,
		bufio.NewReader(strings.NewReader("n\n")),
	)
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}
	if pref.Enabled {
		t.Fatalf("expected proxy disabled")
	}

	// Caller persists.
	if err := persistProxyPreference(store, false); err != nil {
		t.Fatalf("persist error: %v", err)
	}

	// Second call: should return false without prompting.
	pref2, err := ensureProxyPreferenceWithReader(
		context.Background(), store, "", io.Discard,
		bufio.NewReader(strings.NewReader("y\n")),
	)
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}
	if pref2.Enabled {
		t.Fatalf("expected persisted proxy=false to be respected")
	}
	if pref2.NeedsPersist {
		t.Fatalf("expected NeedsPersist=false after preference already on disk")
	}
}

// When profiles exist but ProxyEnabled is nil, the function returns true
// in-memory but does NOT write to disk.
func TestProxyPreferenceProfilesExistDoesNotPersist(t *testing.T) {
	store := newTempStore(t)
	if err := store.Save(config.Config{
		Version:  config.CurrentVersion,
		Profiles: []config.Profile{{ID: "p1", Name: "p1"}},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	pref, err := ensureProxyPreferenceWithReader(
		context.Background(), store, "", io.Discard,
		bufio.NewReader(strings.NewReader("n\n")),
	)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !pref.Enabled {
		t.Fatalf("expected proxy enabled when profiles exist")
	}

	// Verify not persisted to disk.
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.ProxyEnabled != nil {
		t.Fatalf("expected ProxyEnabled not persisted (profiles-exist path)")
	}
}

// Regression: `printf ” | clp run ...` used to succeed by letting the
// "Use SSH proxy?" prompt fall back to its default "no" answer. After the
// EOF propagation fix, closed stdin should still reach the default instead
// of surfacing io.EOF to the caller, or scripted non-interactive runs break.
func TestEnsureProxyPreferenceFallsBackToDefaultOnEOF(t *testing.T) {
	store := newTempStore(t)

	pref, err := ensureProxyPreferenceWithReader(context.Background(), store, "", io.Discard, bufio.NewReader(strings.NewReader("")))
	if err != nil {
		t.Fatalf("expected EOF to accept default, got error: %v", err)
	}
	if pref.Enabled {
		t.Fatalf("expected default (no proxy) on EOF, got enabled=true")
	}
	if !pref.NeedsPersist {
		t.Fatalf("expected NeedsPersist=true for a fresh preference")
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
