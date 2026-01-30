package claudehistory

import "testing"

func TestDisplayTitleFallbacks(t *testing.T) {
	t.Run("subagent display title", func(t *testing.T) {
		if got := (SubagentSession{FirstPrompt: "hello"}).DisplayTitle(); got != "hello" {
			t.Fatalf("expected first prompt, got %q", got)
		}
		if got := (SubagentSession{AgentID: "abc"}).DisplayTitle(); got != "agent-abc" {
			t.Fatalf("expected agent id fallback, got %q", got)
		}
		if got := (SubagentSession{}).DisplayTitle(); got != "Subagent session" {
			t.Fatalf("expected default title, got %q", got)
		}
	})

	t.Run("session display title", func(t *testing.T) {
		if got := (Session{Summary: "sum"}).DisplayTitle(); got != "sum" {
			t.Fatalf("expected summary, got %q", got)
		}
		if got := (Session{FirstPrompt: "first"}).DisplayTitle(); got != "first" {
			t.Fatalf("expected first prompt, got %q", got)
		}
		if got := (Session{SessionID: "sess-1"}).DisplayTitle(); got != "sess-1" {
			t.Fatalf("expected session id fallback, got %q", got)
		}
		if got := (Session{}).DisplayTitle(); got != "Untitled session" {
			t.Fatalf("expected default title, got %q", got)
		}
	})
}
