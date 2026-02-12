package claudehistory

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestIndexHelpers(t *testing.T) {
	t.Run("ResolveClaudeDir prefers override and env", func(t *testing.T) {
		t.Setenv("HOME", filepath.Join(t.TempDir(), "home"))
		override, err := ResolveClaudeDir("$HOME/custom")
		if err != nil {
			t.Fatalf("ResolveClaudeDir override error: %v", err)
		}
		if override != filepath.Join(os.Getenv("HOME"), "custom") {
			t.Fatalf("expected override expansion, got %q", override)
		}

		t.Setenv(EnvClaudeDir, filepath.Join(t.TempDir(), "env-claude"))
		envPath, err := ResolveClaudeDir("")
		if err != nil {
			t.Fatalf("ResolveClaudeDir env error: %v", err)
		}
		if envPath != os.Getenv(EnvClaudeDir) {
			t.Fatalf("expected env path %q, got %q", os.Getenv(EnvClaudeDir), envPath)
		}
	})

	t.Run("parseTime supports RFC3339 variants", func(t *testing.T) {
		if ts := parseTime("2026-01-01T00:00:00.123Z"); ts.IsZero() {
			t.Fatalf("expected RFC3339Nano to parse")
		}
		if ts := parseTime("2026-01-01T00:00:00Z"); ts.IsZero() {
			t.Fatalf("expected RFC3339 to parse")
		}
		if ts := parseTime("invalid"); !ts.IsZero() {
			t.Fatalf("expected invalid time to be zero")
		}
	})

	t.Run("parseSessions includes sidechain and sorts", func(t *testing.T) {
		entries := []sessionIndexEntry{
			{SessionID: "skip", IsSidechain: true, Modified: "2026-01-02T00:00:00Z"},
			{SessionID: "first", Modified: "2026-01-01T00:00:00Z"},
			{SessionID: "second", Modified: "2026-01-03T00:00:00Z"},
		}
		sessions := parseSessions(entries)
		if len(sessions) != 3 {
			t.Fatalf("expected 3 sessions, got %d", len(sessions))
		}
		if sessions[0].SessionID != "second" || sessions[1].SessionID != "skip" || sessions[2].SessionID != "first" {
			t.Fatalf("expected sorted by modified desc, got %#v", sessions)
		}
	})

	t.Run("EntriesProjectPath returns first non-empty", func(t *testing.T) {
		idx := sessionsIndex{Entries: []sessionIndexEntry{{}, {ProjectPath: "/tmp/project"}}}
		if got := idx.EntriesProjectPath(); got != "/tmp/project" {
			t.Fatalf("expected project path, got %q", got)
		}
		if got := (sessionsIndex{}).EntriesProjectPath(); got != "" {
			t.Fatalf("expected empty path, got %q", got)
		}
	})

	t.Run("FindSessionByID and FindSessionWithProject", func(t *testing.T) {
		projects := []Project{
			{
				Key:  "p1",
				Path: "/tmp/a",
				Sessions: []Session{
					{SessionID: "sess-1", Aliases: []string{"sess-old"}},
					{SessionID: "sess-2", Aliases: []string{"dup-alias"}},
				},
			},
			{
				Key:  "p2",
				Path: "/tmp/b",
				Sessions: []Session{
					{SessionID: "sess-3", Aliases: []string{"dup-alias"}},
				},
			},
		}
		if _, ok := FindSessionByID(projects, "missing"); ok {
			t.Fatalf("expected missing session to return false")
		}
		if sess, ok := FindSessionByID(projects, "sess-1"); !ok || sess.SessionID != "sess-1" {
			t.Fatalf("expected to find session, got %#v ok=%v", sess, ok)
		}
		if sess, ok := FindSessionByID(projects, "sess-old"); !ok || sess.SessionID != "sess-1" {
			t.Fatalf("expected alias lookup to find canonical session, got %#v ok=%v", sess, ok)
		}
		if _, ok, ambiguous := FindSessionByIDMatch(projects, "dup-alias"); ok || !ambiguous {
			t.Fatalf("expected duplicate alias to be ambiguous")
		}
		if _, _, ok := FindSessionWithProject(projects, "missing"); ok {
			t.Fatalf("expected missing session to return false")
		}
		if sess, proj, ok := FindSessionWithProject(projects, "sess-1"); !ok || sess.SessionID != "sess-1" || proj.Key != "p1" {
			t.Fatalf("expected to find session and project, got %#v %#v ok=%v", sess, proj, ok)
		}
		if sess, proj, ok, ambiguous := FindSessionWithProjectMatch(projects, "sess-old"); !ok || ambiguous || sess.SessionID != "sess-1" || proj.Key != "p1" {
			t.Fatalf("expected alias lookup to resolve uniquely, got %#v %#v ok=%v ambiguous=%v", sess, proj, ok, ambiguous)
		}
		if _, _, ok, ambiguous := FindSessionWithProjectMatch(projects, "dup-alias"); ok || !ambiguous {
			t.Fatalf("expected duplicate alias to be ambiguous")
		}
		matches := FindSessionAliasMatches(projects, "dup-alias")
		if len(matches) != 2 {
			t.Fatalf("expected 2 alias matches, got %d", len(matches))
		}
		if matches[0].Session.SessionID != "sess-2" || matches[1].Session.SessionID != "sess-3" {
			t.Fatalf("unexpected alias matches order/content: %#v", matches)
		}
	})

	t.Run("SessionWorkingDir falls back to project path", func(t *testing.T) {
		dir := t.TempDir()
		session := Session{ProjectPath: filepath.Join(dir, "missing")}
		project := Project{Path: dir}
		if got := SessionWorkingDir(session, project); got != dir {
			t.Fatalf("expected project path fallback, got %q", got)
		}
		if got := SessionWorkingDir(Session{}, Project{}); got != "" {
			t.Fatalf("expected empty working dir, got %q", got)
		}
	})
}

func TestResolveClaudeDirUsesHome(t *testing.T) {
	home := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	} else {
		t.Setenv("HOME", home)
	}
	t.Setenv(EnvClaudeDir, "")
	got, err := ResolveClaudeDir("")
	if err != nil {
		t.Fatalf("ResolveClaudeDir error: %v", err)
	}
	want := filepath.Join(home, ".claude")
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
