package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoryListCmdOutputsJSON(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "projects"), 0o755); err != nil {
		t.Fatalf("mkdir projects: %v", err)
	}
	claudeDir := root
	cmd := newHistoryListCmd(&claudeDir)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	var payload struct {
		Projects []any `json:"projects"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if len(payload.Projects) != 0 {
		t.Fatalf("expected empty projects, got %#v", payload.Projects)
	}
}

func TestHistoryListCmdErrorsWhenMissingDir(t *testing.T) {
	claudeDir := filepath.Join(t.TempDir(), "missing")
	cmd := newHistoryListCmd(&claudeDir)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error for missing claude dir")
	}
}

func TestHistoryShowCmdSessionNotFound(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "projects"), 0o755); err != nil {
		t.Fatalf("mkdir projects: %v", err)
	}
	claudeDir := root
	cmd := newHistoryShowCmd(&claudeDir)
	cmd.SetArgs([]string{"missing"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected missing session error")
	}
}

func TestHistoryShowCmdMissingFile(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-1")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	index := `{"version":1,"entries":[{"sessionId":"sess-1","fullPath":"/missing/session.jsonl","messageCount":1,"projectPath":"/tmp/project"}],"originalPath":"/tmp/project"}`
	if err := os.WriteFile(filepath.Join(projectDir, "sessions-index.json"), []byte(index), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	claudeDir := root
	cmd := newHistoryShowCmd(&claudeDir)
	cmd.SetArgs([]string{"sess-1"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error for missing session file")
	}
}

func TestHistoryShowCmdAmbiguousAliasListsCandidates(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-ambiguous-show")
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

	claudeDir := root
	cmd := newHistoryShowCmd(&claudeDir)
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
