package claudehistory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverProjectsReadsJsonlSessions(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-1")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	sessionPath := filepath.Join(projectDir, "sess-1.jsonl")
	content := `{"type":"user","message":{"role":"user","content":"Hello there"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project"}
{"type":"assistant","message":{"role":"assistant","content":"Hi"},"timestamp":"2026-01-01T00:01:00Z","cwd":"/tmp/project"}`
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	projects, err := DiscoverProjects(root)
	if err != nil {
		t.Fatalf("DiscoverProjects error: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
	project := projects[0]
	if project.Path != "/tmp/project" {
		t.Fatalf("unexpected project path: %q", project.Path)
	}
	if len(project.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(project.Sessions))
	}
	session := project.Sessions[0]
	if session.SessionID != "sess-1" {
		t.Fatalf("unexpected session id: %q", session.SessionID)
	}
	if session.FirstPrompt != "Hello there" {
		t.Fatalf("unexpected first prompt: %q", session.FirstPrompt)
	}
	if session.MessageCount != 2 {
		t.Fatalf("unexpected message count: %d", session.MessageCount)
	}
	if session.CreatedAt.IsZero() || session.ModifiedAt.IsZero() {
		t.Fatalf("expected non-zero timestamps")
	}
	if !session.ModifiedAt.After(session.CreatedAt) {
		t.Fatalf("expected modified after created")
	}
}

func TestDiscoverProjectsUsesHistoryFallback(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-2")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	sessionPath := filepath.Join(projectDir, "sess-2.jsonl")
	content := `{"type":"user","isMeta":true,"message":{"role":"user","content":"ignore"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/other"}`
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	historyPath := filepath.Join(root, "history.jsonl")
	history := `{"display":"History prompt","timestamp":1700000000000,"project":"/tmp/history-project","sessionId":"sess-2"}`
	if err := os.WriteFile(historyPath, []byte(history), 0o644); err != nil {
		t.Fatalf("write history: %v", err)
	}

	projects, err := DiscoverProjects(root)
	if err != nil {
		t.Fatalf("DiscoverProjects error: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
	project := projects[0]
	if project.Path != "/tmp/history-project" {
		t.Fatalf("unexpected project path: %q", project.Path)
	}
	if len(project.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(project.Sessions))
	}
	session := project.Sessions[0]
	if session.ProjectPath != "/tmp/history-project" {
		t.Fatalf("unexpected session project path: %q", session.ProjectPath)
	}
	if session.FirstPrompt != "History prompt" {
		t.Fatalf("unexpected first prompt: %q", session.FirstPrompt)
	}
}

func TestDiscoverProjectsFallsBackWhenIndexEmpty(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-3")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	indexPath := filepath.Join(projectDir, "sessions-index.json")
	index := `{"version":1,"entries":[],"originalPath":"/tmp/original"}`
	if err := os.WriteFile(indexPath, []byte(index), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	sessionPath := filepath.Join(projectDir, "sess-3.jsonl")
	content := `{"type":"user","message":{"role":"user","content":"Hello"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project"}`
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	projects, err := DiscoverProjects(root)
	if err != nil {
		t.Fatalf("DiscoverProjects error: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
	project := projects[0]
	if project.Path != "/tmp/original" {
		t.Fatalf("unexpected project path: %q", project.Path)
	}
	if len(project.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(project.Sessions))
	}
	session := project.Sessions[0]
	if session.SessionID != "sess-3" {
		t.Fatalf("unexpected session id: %q", session.SessionID)
	}
	if session.ProjectPath != "/tmp/project" {
		t.Fatalf("unexpected session project path: %q", session.ProjectPath)
	}
	if session.FirstPrompt != "Hello" {
		t.Fatalf("unexpected first prompt: %q", session.FirstPrompt)
	}
	if session.MessageCount != 1 {
		t.Fatalf("unexpected message count: %d", session.MessageCount)
	}
}

func TestDiscoverProjectsPrefersExistingProjectPath(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-4")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	validPath := filepath.Join(root, "workspace")
	if err := os.MkdirAll(validPath, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	indexPath := filepath.Join(projectDir, "sessions-index.json")
	index := `{"version":1,"entries":[{"sessionId":"sess-4","projectPath":"/missing","fullPath":""}],"originalPath":"/missing"}`
	if err := os.WriteFile(indexPath, []byte(index), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	sessionPath := filepath.Join(projectDir, "sess-4.jsonl")
	content := `{"type":"user","message":{"role":"user","content":"Hello"},"timestamp":"2026-01-01T00:00:00Z","cwd":"` + validPath + `"}`
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	projects, err := DiscoverProjects(root)
	if err != nil {
		t.Fatalf("DiscoverProjects error: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
	project := projects[0]
	if project.Path != validPath {
		t.Fatalf("unexpected project path: %q", project.Path)
	}
	if len(project.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(project.Sessions))
	}
	session := project.Sessions[0]
	if session.ProjectPath != validPath {
		t.Fatalf("unexpected session project path: %q", session.ProjectPath)
	}
	if SessionWorkingDir(session, project) != validPath {
		t.Fatalf("unexpected working dir: %q", SessionWorkingDir(session, project))
	}
}

func TestDiscoverProjectsRehydratesMissingFilePath(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-5")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	indexPath := filepath.Join(projectDir, "sessions-index.json")
	index := `{"version":1,"entries":[{"sessionId":"sess-5","fullPath":""}],"originalPath":""}`
	if err := os.WriteFile(indexPath, []byte(index), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	sessionPath := filepath.Join(projectDir, "sess-5.jsonl")
	content := `{"type":"user","message":{"role":"user","content":"Hello"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project"}
{"type":"assistant","message":{"role":"assistant","content":"Hi"},"timestamp":"2026-01-01T00:01:00Z","cwd":"/tmp/project"}`
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	projects, err := DiscoverProjects(root)
	if err != nil {
		t.Fatalf("DiscoverProjects error: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
	session := projects[0].Sessions[0]
	if session.FilePath != sessionPath {
		t.Fatalf("unexpected session file path: %q", session.FilePath)
	}
	if session.FirstPrompt != "Hello" {
		t.Fatalf("unexpected first prompt: %q", session.FirstPrompt)
	}
	if session.MessageCount != 2 {
		t.Fatalf("unexpected message count: %d", session.MessageCount)
	}
}

func TestDiscoverProjectsFallbackWhenIndexStale(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-6")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	validPath := filepath.Join(root, "workspace")
	if err := os.MkdirAll(validPath, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	indexPath := filepath.Join(projectDir, "sessions-index.json")
	index := `{"version":1,"entries":[{"sessionId":"sess-missing","fullPath":"/does/not/exist"}],"originalPath":"` + validPath + `"}`
	if err := os.WriteFile(indexPath, []byte(index), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	nestedDir := filepath.Join(projectDir, "nested")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	nestedPath := filepath.Join(nestedDir, "sess-6.jsonl")
	content := `{"type":"user","message":{"role":"user","content":"Nested"},"timestamp":"2026-01-01T00:00:00Z","cwd":"` + validPath + `"}`
	if err := os.WriteFile(nestedPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	projects, err := DiscoverProjects(root)
	if err != nil {
		t.Fatalf("DiscoverProjects error: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
	project := projects[0]
	if project.Path != validPath {
		t.Fatalf("unexpected project path: %q", project.Path)
	}
	if len(project.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(project.Sessions))
	}
	session := project.Sessions[0]
	if session.SessionID != "sess-6" {
		t.Fatalf("unexpected session id: %q", session.SessionID)
	}
	if session.FilePath != nestedPath {
		t.Fatalf("unexpected session file path: %q", session.FilePath)
	}
}

func TestSessionWorkingDirSkipsMissingSessionPath(t *testing.T) {
	projectPath := t.TempDir()
	session := Session{ProjectPath: filepath.Join(projectPath, "missing")}
	project := Project{Path: projectPath}
	got := SessionWorkingDir(session, project)
	if got != projectPath {
		t.Fatalf("unexpected working dir: %q", got)
	}
}
