package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baaaaaaaka/claude_code_helper/internal/claudehistory"
	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

func TestHistoryOpenCmdMissingSession(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-1")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	sessionPath := filepath.Join(projectDir, "sess-1.jsonl")
	content := `{"type":"user","message":{"role":"user","content":"Hello"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project"}`
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	store := newTempStore(t)
	disabled := false
	if err := store.Save(config.Config{Version: config.CurrentVersion, ProxyEnabled: &disabled}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	claudeDir := root
	claudePath := ""
	profileRef := ""
	cmd := newHistoryOpenCmd(&rootOptions{configPath: store.Path()}, &claudeDir, &claudePath, &profileRef)
	cmd.SetArgs([]string{"missing"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected missing session error")
	}
}

func TestHistoryOpenCmdInvalidConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "projects"), 0o755); err != nil {
		t.Fatalf("mkdir projects: %v", err)
	}

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte("{"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	claudeDir := root
	claudePath := ""
	profileRef := ""
	cmd := newHistoryOpenCmd(&rootOptions{configPath: configPath}, &claudeDir, &claudePath, &profileRef)
	cmd.SetArgs([]string{"sess-1"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected config load error")
	}
}

func TestHistoryOpenCmdResolvesAliasToCanonical(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-alias")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	sessionPath := filepath.Join(projectDir, "canonical-id.jsonl")
	content := `{"type":"user","message":{"role":"user","content":"Hello"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project","sessionId":"legacy-id"}`
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	store := newTempStore(t)
	disabled := false
	if err := store.Save(config.Config{Version: config.CurrentVersion, ProxyEnabled: &disabled}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	prevRun := runClaudeSessionFunc
	t.Cleanup(func() { runClaudeSessionFunc = prevRun })

	called := false
	gotSessionID := ""
	runClaudeSessionFunc = func(
		ctx context.Context,
		root *rootOptions,
		store *config.Store,
		profile *config.Profile,
		instances []config.Instance,
		session claudehistory.Session,
		project claudehistory.Project,
		path string,
		dir string,
		useProxy bool,
		useYolo bool,
		log io.Writer,
	) error {
		called = true
		gotSessionID = session.SessionID
		return nil
	}

	claudeDir := root
	claudePath := ""
	profileRef := ""
	cmd := newHistoryOpenCmd(&rootOptions{configPath: store.Path()}, &claudeDir, &claudePath, &profileRef)
	cmd.SetArgs([]string{"legacy-id"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected alias session id to resolve, got error: %v", err)
	}
	if !called {
		t.Fatalf("expected runClaudeSession to be called")
	}
	if gotSessionID != "canonical-id" {
		t.Fatalf("expected canonical session id, got %q", gotSessionID)
	}
}

func TestHistoryOpenCmdReturnsAmbiguousAliasError(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-ambiguous")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	contentA := `{"type":"user","message":{"role":"user","content":"A"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project","sessionId":"legacy-dup"}`
	contentB := `{"type":"user","message":{"role":"user","content":"B"},"timestamp":"2026-01-02T00:00:00Z","cwd":"/tmp/project","sessionId":"legacy-dup"}`
	if err := os.WriteFile(filepath.Join(projectDir, "canonical-a.jsonl"), []byte(contentA), 0o644); err != nil {
		t.Fatalf("write session A: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "canonical-b.jsonl"), []byte(contentB), 0o644); err != nil {
		t.Fatalf("write session B: %v", err)
	}

	store := newTempStore(t)
	disabled := false
	if err := store.Save(config.Config{Version: config.CurrentVersion, ProxyEnabled: &disabled}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	prevRun := runClaudeSessionFunc
	t.Cleanup(func() { runClaudeSessionFunc = prevRun })
	runClaudeSessionFunc = func(
		ctx context.Context,
		root *rootOptions,
		store *config.Store,
		profile *config.Profile,
		instances []config.Instance,
		session claudehistory.Session,
		project claudehistory.Project,
		path string,
		dir string,
		useProxy bool,
		useYolo bool,
		log io.Writer,
	) error {
		t.Fatalf("runClaudeSession should not be called for ambiguous alias")
		return nil
	}

	claudeDir := root
	claudePath := ""
	profileRef := ""
	cmd := newHistoryOpenCmd(&rootOptions{configPath: store.Path()}, &claudeDir, &claudePath, &profileRef)
	cmd.SetArgs([]string{"legacy-dup"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected ambiguous alias error")
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "ambiguous") {
		t.Fatalf("expected ambiguous error, got: %v", err)
	}
	if !strings.Contains(msg, "candidate canonical sessions") {
		t.Fatalf("expected candidate list in error, got: %v", err)
	}
	if !strings.Contains(msg, "canonical-a") || !strings.Contains(msg, "canonical-b") {
		t.Fatalf("expected canonical candidates in error, got: %v", err)
	}
}
