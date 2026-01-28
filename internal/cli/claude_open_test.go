package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/baaaaaaaka/claude_code_helper/internal/claudehistory"
	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

func TestBuildClaudeResumeCommandUsesSessionPath(t *testing.T) {
	dir := t.TempDir()
	session := claudehistory.Session{SessionID: "abc", ProjectPath: dir}
	project := claudehistory.Project{Path: "/tmp/other"}

	path, args, cwd, err := buildClaudeResumeCommand("/bin/claude", session, project, false)
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

	_, _, cwd, err := buildClaudeResumeCommand("/bin/claude", session, project, false)
	if err != nil {
		t.Fatalf("buildClaudeResumeCommand error: %v", err)
	}
	if cwd != dir {
		t.Fatalf("expected cwd %s, got %s", dir, cwd)
	}
}

func TestBuildClaudeResumeCommandAddsYoloArgs(t *testing.T) {
	dir := t.TempDir()
	session := claudehistory.Session{SessionID: "abc"}
	project := claudehistory.Project{Path: dir}

	_, args, _, err := buildClaudeResumeCommand("/bin/claude", session, project, true)
	if err != nil {
		t.Fatalf("buildClaudeResumeCommand error: %v", err)
	}
	want := []string{"--permission-mode", "bypassPermissions", "--resume", "abc"}
	if len(args) != len(want) {
		t.Fatalf("expected args %v, got %v", want, args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("expected args %v, got %v", want, args)
		}
	}
}

func TestBuildClaudeResumeCommandRejectsMissingSession(t *testing.T) {
	dir := t.TempDir()
	session := claudehistory.Session{}
	project := claudehistory.Project{Path: dir}

	_, _, _, err := buildClaudeResumeCommand("/bin/claude", session, project, false)
	if err == nil {
		t.Fatalf("expected error for missing session id")
	}
}

func TestBuildClaudeResumeCommandRejectsMissingCwd(t *testing.T) {
	session := claudehistory.Session{SessionID: "abc", ProjectPath: filepath.Join(t.TempDir(), "missing")}
	project := claudehistory.Project{}

	_, _, _, err := buildClaudeResumeCommand("/bin/claude", session, project, false)
	if err == nil {
		t.Fatalf("expected error for missing cwd")
	}
}

func TestNormalizeWorkingDirResolvesRelative(t *testing.T) {
	dir := t.TempDir()
	rel := filepath.Base(dir)
	abs := filepath.Dir(dir)
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(old) }()
	if err := os.Chdir(abs); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	got, err := normalizeWorkingDir(rel)
	if err != nil {
		t.Fatalf("normalizeWorkingDir error: %v", err)
	}
	if canonicalPath(t, got) != canonicalPath(t, dir) {
		t.Fatalf("expected %s, got %s", dir, got)
	}
}

func TestNormalizeWorkingDirRejectsMissing(t *testing.T) {
	_, err := normalizeWorkingDir(filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatalf("expected error for missing cwd")
	}
}

func TestRunClaudeNewSessionUsesCwdDirect(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	dir := t.TempDir()
	outFile := filepath.Join(t.TempDir(), "pwd.txt")
	scriptPath := filepath.Join(t.TempDir(), "claude")
	script := fmt.Sprintf("#!/bin/sh\npwd > %q\n", outFile)
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}

	store, err := config.NewStore(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	err = runClaudeNewSession(
		context.Background(),
		&rootOptions{},
		store,
		nil,
		nil,
		dir,
		scriptPath,
		"",
		false,
		false,
		io.Discard,
	)
	if err != nil {
		t.Fatalf("runClaudeNewSession error: %v", err)
	}
	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if strings.TrimSpace(string(got)) != dir {
		if canonicalPath(t, strings.TrimSpace(string(got))) != canonicalPath(t, dir) {
			t.Fatalf("expected cwd %s, got %q", dir, strings.TrimSpace(string(got)))
		}
	}
}

func TestRunClaudeNewSessionAddsYoloArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	dir := t.TempDir()
	outFile := filepath.Join(t.TempDir(), "args.txt")
	scriptPath := filepath.Join(t.TempDir(), "claude")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\n", outFile)
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}

	store, err := config.NewStore(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	err = runClaudeNewSession(
		context.Background(),
		&rootOptions{},
		store,
		nil,
		nil,
		dir,
		scriptPath,
		"",
		false,
		true,
		io.Discard,
	)
	if err != nil {
		t.Fatalf("runClaudeNewSession error: %v", err)
	}
	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	want := []string{"--permission-mode", "bypassPermissions"}
	if len(lines) < len(want) {
		t.Fatalf("expected args %v, got %v", want, lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("expected args %v, got %v", want, lines)
		}
	}
}

func TestRunClaudeNewSessionRejectsProxyWithoutProfile(t *testing.T) {
	dir := t.TempDir()
	store, err := config.NewStore(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	err = runClaudeNewSession(
		context.Background(),
		&rootOptions{},
		store,
		nil,
		nil,
		dir,
		"/bin/claude",
		"",
		true,
		false,
		io.Discard,
	)
	if err == nil {
		t.Fatalf("expected error when proxy enabled without profile")
	}
}

func canonicalPath(t *testing.T, path string) string {
	t.Helper()
	if path == "" {
		return path
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	return filepath.Clean(path)
}
