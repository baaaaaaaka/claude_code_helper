package claudehistory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func jsonString(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	return string(encoded)
}

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
	if project.Path != "/tmp/project" {
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

func TestDiscoverProjectsMergesIndexAndScan(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-merge")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	indexPath := filepath.Join(projectDir, "sessions-index.json")
	index := `{"version":1,"entries":[{"sessionId":"sess-index","summary":"From index","messageCount":1,"fullPath":""}],"originalPath":"/tmp/original"}`
	if err := os.WriteFile(indexPath, []byte(index), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	indexSessionPath := filepath.Join(projectDir, "sess-index.jsonl")
	indexContent := `{"type":"user","message":{"role":"user","content":"Hello"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project1"}
{"type":"assistant","message":{"role":"assistant","content":"Hi"},"timestamp":"2026-01-01T00:01:00Z","cwd":"/tmp/project1"}`
	if err := os.WriteFile(indexSessionPath, []byte(indexContent), 0o644); err != nil {
		t.Fatalf("write index session: %v", err)
	}

	scanSessionPath := filepath.Join(projectDir, "sess-scan.jsonl")
	scanContent := `{"type":"user","message":{"role":"user","content":"Other"},"timestamp":"2026-01-02T00:00:00Z","cwd":"/tmp/project2"}`
	if err := os.WriteFile(scanSessionPath, []byte(scanContent), 0o644); err != nil {
		t.Fatalf("write scan session: %v", err)
	}

	projects, err := DiscoverProjects(root)
	if err != nil {
		t.Fatalf("DiscoverProjects error: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
	if len(projects[0].Sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(projects[0].Sessions))
	}
	sessionMap := map[string]Session{}
	for _, sess := range projects[0].Sessions {
		sessionMap[sess.SessionID] = sess
	}
	indexSession, ok := sessionMap["sess-index"]
	if !ok {
		t.Fatalf("missing merged index session")
	}
	if indexSession.Summary != "From index" {
		t.Fatalf("unexpected summary: %q", indexSession.Summary)
	}
	if indexSession.MessageCount != 2 {
		t.Fatalf("unexpected message count: %d", indexSession.MessageCount)
	}
	if indexSession.FilePath != indexSessionPath {
		t.Fatalf("unexpected file path: %q", indexSession.FilePath)
	}
	scanSession, ok := sessionMap["sess-scan"]
	if !ok {
		t.Fatalf("missing scanned session")
	}
	if scanSession.FirstPrompt != "Other" {
		t.Fatalf("unexpected first prompt: %q", scanSession.FirstPrompt)
	}
}

func TestDiscoverProjectsUsesFilenameCanonicalSessionID(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-session-id")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	sessionPath := filepath.Join(projectDir, "file-name.jsonl")
	content := `{"type":"user","message":{"role":"user","content":"Hello"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project","sessionId":"sess-actual"}`
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	projects, err := DiscoverProjects(root)
	if err != nil {
		t.Fatalf("DiscoverProjects error: %v", err)
	}
	if len(projects) != 1 || len(projects[0].Sessions) != 1 {
		t.Fatalf("expected 1 project with 1 session, got %#v", projects)
	}
	session := projects[0].Sessions[0]
	if session.SessionID != "file-name" {
		t.Fatalf("unexpected session id: %q", projects[0].Sessions[0].SessionID)
	}
	if !sessionHasAlias(session, "sess-actual") {
		t.Fatalf("expected embedded session id to be captured as alias, got %#v", session.Aliases)
	}
}

func TestDiscoverProjectsPrefersCanonicalWhenAliasConflicts(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-dedupe")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	firstPath := filepath.Join(projectDir, "file-one.jsonl")
	firstContent := `{"type":"user","message":{"role":"user","content":"First"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project","sessionId":"sess-dup"}`
	if err := os.WriteFile(firstPath, []byte(firstContent), 0o644); err != nil {
		t.Fatalf("write first session: %v", err)
	}

	secondPath := filepath.Join(projectDir, "sess-dup.jsonl")
	secondContent := `{"type":"user","message":{"role":"user","content":"Second"},"timestamp":"2026-01-02T00:00:00Z","cwd":"/tmp/project","sessionId":"sess-dup"}`
	if err := os.WriteFile(secondPath, []byte(secondContent), 0o644); err != nil {
		t.Fatalf("write second session: %v", err)
	}

	projects, err := DiscoverProjects(root)
	if err != nil {
		t.Fatalf("DiscoverProjects error: %v", err)
	}
	if len(projects) != 1 || len(projects[0].Sessions) != 2 {
		t.Fatalf("expected 1 project with 2 sessions, got %#v", projects)
	}
	sessions := projects[0].Sessions
	if sessions[0].SessionID != "sess-dup" {
		t.Fatalf("unexpected first session id: %q", sessions[0].SessionID)
	}
	if sessions[0].FilePath != secondPath {
		t.Fatalf("unexpected first session file path: %q", sessions[0].FilePath)
	}
	if sessions[1].SessionID != "file-one" {
		t.Fatalf("unexpected second session id: %q", sessions[1].SessionID)
	}
	if !sessionHasAlias(sessions[1], "sess-dup") {
		t.Fatalf("expected second session to keep embedded id as alias: %#v", sessions[1].Aliases)
	}

	resolved, ok := FindSessionByID(projects, "sess-dup")
	if !ok {
		t.Fatalf("expected canonical lookup to succeed")
	}
	if resolved.FilePath != secondPath {
		t.Fatalf("expected canonical session to win lookup, got %q", resolved.FilePath)
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
	index := fmt.Sprintf(
		`{"version":1,"entries":[{"sessionId":"sess-4","projectPath":%s,"fullPath":""}],"originalPath":%s}`,
		jsonString(t, "/missing"),
		jsonString(t, "/missing"),
	)
	if err := os.WriteFile(indexPath, []byte(index), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	sessionPath := filepath.Join(projectDir, "sess-4.jsonl")
	content := fmt.Sprintf(
		`{"type":"user","message":{"role":"user","content":"Hello"},"timestamp":"2026-01-01T00:00:00Z","cwd":%s}`,
		jsonString(t, validPath),
	)
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
	index := fmt.Sprintf(
		`{"version":1,"entries":[{"sessionId":"sess-missing","fullPath":%s}],"originalPath":%s}`,
		jsonString(t, "/does/not/exist"),
		jsonString(t, validPath),
	)
	if err := os.WriteFile(indexPath, []byte(index), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	nestedDir := filepath.Join(projectDir, "nested")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	nestedPath := filepath.Join(nestedDir, "sess-6.jsonl")
	content := fmt.Sprintf(
		`{"type":"user","message":{"role":"user","content":"Nested"},"timestamp":"2026-01-01T00:00:00Z","cwd":%s}`,
		jsonString(t, validPath),
	)
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

func TestDiscoverProjectsAttachesSubagents(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-subagents")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	mainPath := filepath.Join(projectDir, "sess-main.jsonl")
	mainContent := `{"type":"user","message":{"role":"user","content":"Hello"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project"}
{"type":"assistant","message":{"role":"assistant","content":"Hi"},"timestamp":"2026-01-01T00:01:00Z","cwd":"/tmp/project"}`
	if err := os.WriteFile(mainPath, []byte(mainContent), 0o644); err != nil {
		t.Fatalf("write main session: %v", err)
	}

	agentPath := filepath.Join(projectDir, "agent-abc.jsonl")
	agentContent := `{"type":"user","message":{"role":"user","content":"Sub task"},"timestamp":"2026-01-01T00:02:00Z","cwd":"/tmp/project","sessionId":"sess-main","isSidechain":true}`
	if err := os.WriteFile(agentPath, []byte(agentContent), 0o644); err != nil {
		t.Fatalf("write agent session: %v", err)
	}

	subagentsDir := filepath.Join(projectDir, "sess-main", "subagents")
	if err := os.MkdirAll(subagentsDir, 0o755); err != nil {
		t.Fatalf("mkdir subagents: %v", err)
	}
	nestedAgentPath := filepath.Join(subagentsDir, "agent-xyz.jsonl")
	nestedContent := `{"type":"user","message":{"role":"user","content":"Nested task"},"timestamp":"2026-01-01T00:03:00Z","cwd":"/tmp/project","sessionId":"agent-session-xyz","isSidechain":true}`
	if err := os.WriteFile(nestedAgentPath, []byte(nestedContent), 0o644); err != nil {
		t.Fatalf("write nested agent session: %v", err)
	}

	projects, err := DiscoverProjects(root)
	if err != nil {
		t.Fatalf("DiscoverProjects error: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
	project := projects[0]
	if len(project.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(project.Sessions))
	}
	session := project.Sessions[0]
	if session.SessionID != "sess-main" {
		t.Fatalf("unexpected session id: %q", session.SessionID)
	}
	if session.MessageCount != 2 {
		t.Fatalf("unexpected message count: %d", session.MessageCount)
	}
	if len(session.Subagents) != 2 {
		t.Fatalf("expected 2 subagents, got %d", len(session.Subagents))
	}
	subagentIDs := map[string]bool{}
	for _, sub := range session.Subagents {
		subagentIDs[sub.AgentID] = true
		if sub.ParentSessionID != "sess-main" {
			t.Fatalf("unexpected parent session id: %q", sub.ParentSessionID)
		}
	}
	if !subagentIDs["abc"] || !subagentIDs["xyz"] {
		t.Fatalf("unexpected subagent IDs: %#v", subagentIDs)
	}
}

func TestDiscoverProjectsSkipsSnapshotOnlySessions(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-snapshot")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	sessionPath := filepath.Join(projectDir, "sess-snapshot.jsonl")
	content := `{"type":"file-history-snapshot","messageId":"snap-1","snapshot":{"messageId":"snap-1","trackedFileBackups":{},"timestamp":"2026-01-01T00:00:00Z"},"isSnapshotUpdate":false}`
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	projects, err := DiscoverProjects(root)
	if err != nil {
		t.Fatalf("DiscoverProjects error: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("expected 0 projects, got %d", len(projects))
	}
}

func TestDiscoverProjectsKeepsIndexSidechainEntries(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-index")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	indexPath := filepath.Join(projectDir, "sessions-index.json")
	index := `{"version":1,"entries":[{"sessionId":"sess-main","fullPath":"","messageCount":1},{"sessionId":"agent-ignored","fullPath":"","isSidechain":true,"messageCount":5}],"originalPath":"/tmp/original"}`
	if err := os.WriteFile(indexPath, []byte(index), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	sessionPath := filepath.Join(projectDir, "sess-main.jsonl")
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
	if len(projects[0].Sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(projects[0].Sessions))
	}
	ids := map[string]bool{}
	for _, sess := range projects[0].Sessions {
		ids[sess.SessionID] = true
	}
	if !ids["sess-main"] || !ids["agent-ignored"] {
		t.Fatalf("unexpected session ids: %#v", ids)
	}
}

func TestDiscoverProjectsDropsSidechainWhenAttachedSubagent(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-sidechain-drop")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	indexPath := filepath.Join(projectDir, "sessions-index.json")
	index := `{"version":1,"entries":[{"sessionId":"sess-main","fullPath":"","messageCount":1},{"sessionId":"agent-abc","fullPath":"","isSidechain":true,"messageCount":2}],"originalPath":"/tmp/original"}`
	if err := os.WriteFile(indexPath, []byte(index), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	sessionPath := filepath.Join(projectDir, "sess-main.jsonl")
	content := `{"type":"user","message":{"role":"user","content":"Hello"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project"}`
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	agentPath := filepath.Join(projectDir, "agent-abc.jsonl")
	agentContent := `{"type":"user","message":{"role":"user","content":"Sub task"},"timestamp":"2026-01-01T00:02:00Z","cwd":"/tmp/project","sessionId":"sess-main","isSidechain":true}`
	if err := os.WriteFile(agentPath, []byte(agentContent), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}

	projects, err := DiscoverProjects(root)
	if err != nil {
		t.Fatalf("DiscoverProjects error: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
	if len(projects[0].Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(projects[0].Sessions))
	}
	if projects[0].Sessions[0].SessionID != "sess-main" {
		t.Fatalf("unexpected session id: %q", projects[0].Sessions[0].SessionID)
	}
	if len(projects[0].Sessions[0].Subagents) != 1 {
		t.Fatalf("expected 1 subagent, got %d", len(projects[0].Sessions[0].Subagents))
	}
}

func TestDiscoverProjectsIgnoresAgentOnlyProject(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-agent-only")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	agentPath := filepath.Join(projectDir, "agent-abc.jsonl")
	content := `{"type":"user","message":{"role":"user","content":"Sub task"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project","sessionId":"sess-main","isSidechain":true}`
	if err := os.WriteFile(agentPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}

	projects, err := DiscoverProjects(root)
	if err != nil {
		t.Fatalf("DiscoverProjects error: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("expected 0 projects, got %d", len(projects))
	}
}
