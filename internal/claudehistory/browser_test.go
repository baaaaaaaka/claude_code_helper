package claudehistory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBrowserSummariesKeepFullMetadataSeparate(t *testing.T) {
	useTempClaudeHistoryCacheDir(t)
	root := t.TempDir()
	dir := filepath.Join(root, "projects", "project")
	agents := filepath.Join(dir, "main", "subagents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "main.jsonl")
	agent := filepath.Join(agents, "agent-child.jsonl")
	snapshot := filepath.Join(dir, "only-snapshot.jsonl")
	longRecord := jsonlUserMessage("main", strings.Repeat("x", sessionSummaryMaxBytes))
	for path, content := range map[string]string{
		main:     jsonlUserMessage("main", "Main prompt") + longRecord,
		agent:    jsonlUserMessage("main", "Child prompt") + longRecord,
		snapshot: "{\"type\":\"file-history-snapshot\"}\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		projects, err := DiscoverProjectSummariesContext(ctx, root)
		if err != nil {
			t.Fatal(err)
		}
		if len(projects) != 1 || len(projects[0].Sessions) != 1 {
			t.Fatalf("unexpected sessions: %#v", projects)
		}
		session := projects[0].Sessions[0]
		if session.FirstPrompt != "Main prompt" || session.MessageCount != -1 || session.SourceVersion == "" || len(session.Subagents) != 1 {
			t.Fatalf("unexpected browser session: %#v", session)
		}
		sub := session.Subagents[0]
		if sub.FirstPrompt != "" || sub.SourceVersion == "" {
			t.Fatalf("subagent should be discovered without loading its title: %#v", sub)
		}
		detail, err := LoadSubagentSummary(ctx, sub)
		if err != nil || detail.FirstPrompt != "Child prompt" || detail.MessageCount != -1 {
			t.Fatalf("subagent detail: %s, messages=%d, err=%v", detail.FirstPrompt, detail.MessageCount, err)
		}
		if err := ValidateSessionSource(session); err != nil {
			t.Fatal(err)
		}
	}
	exact, err := DiscoverProjectsContext(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	session := exact[0].Sessions[0]
	if session.MessageCount != 2 || len(session.Subagents) != 1 || session.Subagents[0].MessageCount != 2 {
		t.Fatalf("summary contaminated exact metadata: main=%d, subagents=%v", session.MessageCount, session.Subagents)
	}
}

func TestBrowserHistoryAppendRewritesOnlyAffectedProject(t *testing.T) {
	useTempClaudeHistoryCacheDir(t)
	root := t.TempDir()
	for _, id := range []string{"one", "two"} {
		dir := filepath.Join(root, "projects", id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte("{\"type\":\"system\"}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	history := filepath.Join(root, "history.jsonl")
	if err := os.WriteFile(history, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := DiscoverProjectSummariesContext(ctx, root); err != nil {
		t.Fatal(err)
	}
	readState := func(id string) fileCacheKey {
		t.Helper()
		path, err := projectPersistentCachePathForMode(filepath.Join(root, "projects", id), true)
		if err != nil {
			t.Fatal(err)
		}
		info, err := currentFileCacheKey(path)
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	first, other := readState("one"), readState("two")
	check := func(want string) {
		t.Helper()
		projects, err := DiscoverProjectSummariesContext(ctx, root)
		if err != nil || len(projects) != 2 || projects[0].Sessions[0].FirstPrompt != want {
			t.Fatalf("expected %q from current history, got %#v (%v)", want, projects, err)
		}
		if readState("two") != other {
			t.Fatal("unrelated project cache was rewritten")
		}
	}
	check("")
	if readState("one") != first {
		t.Fatal("steady-state refresh rewrote a project cache")
	}
	f, err := os.OpenFile(history, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	entry := "{\"display\":\"first\",\"timestamp\":1700000000000,\"sessionId\":\"one\"}\n"
	if _, err := f.WriteString(entry); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	check("first")
	if readState("one") == first {
		t.Fatal("affected project cache was not updated")
	}
	if err := os.WriteFile(history, []byte(strings.Replace(entry, "first", "other", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	check("other")
	if err := os.Truncate(history, 0); err != nil {
		t.Fatal(err)
	}
	check("")
}

func TestValidateBrowserSessionAllowsAppendAndRejectsReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "session.jsonl")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := Session{SessionID: "session", FilePath: path, SourceVersion: (projectInventory{}).sourceVersion(path)}
	if err := os.WriteFile(path, []byte("old plus new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSessionSource(session); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, filepath.Join(root, "retired.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new file"), 0o644); err != nil {
		t.Fatal(err)
	}
	key, err := currentFileCacheKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if key.StrongIdentity && ValidateSessionSource(session) == nil {
		t.Fatal("replacement was accepted as the original selection")
	}
}

func TestReadSessionPreviewMatchesCompleteMessages(t *testing.T) {
	const count = 20
	for _, tc := range []struct {
		name, content string
		limit         int
	}{
		{"blocks", strings.Repeat(jsonlUserMessage("id", strings.Repeat("中", 8000)), 25), count},
		{"no trailing newline", jsonlUserMessage("id", "before") + strings.TrimSuffix(jsonlUserMessage("id", "last"), "\n"), count},
		{"fallback order", `{"message":{"role":"assistant","content":[{"type":"thinking","thinking":"first"},{"type":"tool_use","name":"second"}]}}` + "\n", 2},
		{"primary beats fallback", jsonlUserMessage("id", "primary") + strings.Repeat("{\"isMeta\":true}\n", 7), 3},
		{"long record crosses several blocks", jsonlUserMessage("id", strings.Repeat("a", 5*previewBlockSize)) + jsonlUserMessage("id", "last"), 2},
		{"blank and malformed records", "bad\n\n" + jsonlUserMessage("id", "valid") + "bad", count},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			want, err := ReadSessionMessages(path, tc.limit)
			if err != nil {
				t.Fatal(err)
			}
			got, limited, err := ReadSessionPreview(context.Background(), path, tc.limit)
			if err != nil || limited || !reflect.DeepEqual(got, want) {
				t.Fatalf("preview parity failed: got %d, want %d, limited=%v, err=%v", len(got), len(want), limited, err)
			}
		})
	}
}

func TestReadSessionPreviewLimitsOversizeAndHonorsCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := jsonlUserMessage("id", strings.Repeat("x", previewMaxBytes+previewBlockSize)) + jsonlUserMessage("id", "recent")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, limited, err := ReadSessionPreview(context.Background(), path, 20)
	if err != nil || !limited || len(got) != 1 || got[0].Content != "recent" {
		t.Fatalf("expected a bounded partial preview, got %d, limited=%v, err=%v", len(got), limited, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := ReadSessionPreview(ctx, path, 20); err != context.Canceled {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func jsonlUserMessage(id, message string) string {
	return fmt.Sprintf("{\"type\":\"user\",\"sessionId\":%q,\"timestamp\":%q,\"message\":{\"role\":\"user\",\"content\":%q}}\n", id, time.Unix(1700000000, 0).UTC().Format(time.RFC3339), message)
}
