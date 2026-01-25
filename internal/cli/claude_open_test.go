package cli

import (
	"path/filepath"
	"testing"

	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/claudehistory"
)

func TestBuildClaudeResumeCommandUsesSessionPath(t *testing.T) {
	dir := t.TempDir()
	session := claudehistory.Session{SessionID: "abc", ProjectPath: dir}
	project := claudehistory.Project{Path: "/tmp/other"}

	path, args, cwd, err := buildClaudeResumeCommand("/bin/claude", session, project)
	if err != nil {
		t.Fatalf("buildClaudeResumeCommand error: %v", err)
	}
	if path != "/bin/claude" {
		t.Fatalf("expected path /bin/claude, got %s", path)
	}
	if len(args) != 2 || args[0] != "--resume" || args[1] != "abc" {
		t.Fatalf("unexpected args: %#v", args)
	}
	if cwd != dir {
		t.Fatalf("expected cwd %s, got %s", dir, cwd)
	}
}

func TestBuildClaudeResumeCommandUsesProjectPath(t *testing.T) {
	dir := t.TempDir()
	session := claudehistory.Session{SessionID: "abc"}
	project := claudehistory.Project{Path: dir}

	_, _, cwd, err := buildClaudeResumeCommand("/bin/claude", session, project)
	if err != nil {
		t.Fatalf("buildClaudeResumeCommand error: %v", err)
	}
	if cwd != dir {
		t.Fatalf("expected cwd %s, got %s", dir, cwd)
	}
}

func TestBuildClaudeResumeCommandRejectsMissingSession(t *testing.T) {
	dir := t.TempDir()
	session := claudehistory.Session{}
	project := claudehistory.Project{Path: dir}

	_, _, _, err := buildClaudeResumeCommand("/bin/claude", session, project)
	if err == nil {
		t.Fatalf("expected error for missing session id")
	}
}

func TestBuildClaudeResumeCommandRejectsMissingCwd(t *testing.T) {
	session := claudehistory.Session{SessionID: "abc", ProjectPath: filepath.Join(t.TempDir(), "missing")}
	project := claudehistory.Project{}

	_, _, _, err := buildClaudeResumeCommand("/bin/claude", session, project)
	if err == nil {
		t.Fatalf("expected error for missing cwd")
	}
}
