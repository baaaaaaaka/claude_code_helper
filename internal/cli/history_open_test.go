package cli

import (
	"os"
	"path/filepath"
	"testing"

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
