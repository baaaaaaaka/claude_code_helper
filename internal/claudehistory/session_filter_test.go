package claudehistory

import "testing"

func TestFilterEmptySessionsDropsEmpty(t *testing.T) {
	sessions := []Session{{SessionID: "empty"}}
	filtered := filterEmptySessions(sessions)
	if len(filtered) != 0 {
		t.Fatalf("expected empty sessions to be dropped, got %d", len(filtered))
	}
}

func TestFilterEmptySessionsKeepsSummary(t *testing.T) {
	sessions := []Session{{SessionID: "summary", Summary: "keep me"}}
	filtered := filterEmptySessions(sessions)
	if len(filtered) != 1 {
		t.Fatalf("expected summary session to remain, got %d", len(filtered))
	}
}

func TestFilterEmptySessionsKeepsFirstPrompt(t *testing.T) {
	sessions := []Session{{SessionID: "prompt", FirstPrompt: "hello"}}
	filtered := filterEmptySessions(sessions)
	if len(filtered) != 1 {
		t.Fatalf("expected prompt session to remain, got %d", len(filtered))
	}
}

func TestFilterEmptySessionsKeepsSubagents(t *testing.T) {
	sessions := []Session{{
		SessionID: "subagent",
		Subagents: []SubagentSession{{AgentID: "agent-1"}},
	}}
	filtered := filterEmptySessions(sessions)
	if len(filtered) != 1 {
		t.Fatalf("expected subagent session to remain, got %d", len(filtered))
	}
}

func TestIsEmptySessionAndFilterOrder(t *testing.T) {
	t.Run("isEmptySession respects message count and trimmed fields", func(t *testing.T) {
		if isEmptySession(Session{MessageCount: 1}) {
			t.Fatalf("expected message count to mark session as non-empty")
		}
		if isEmptySession(Session{FirstPrompt: "  hi  "}) {
			t.Fatalf("expected first prompt to mark session as non-empty")
		}
		if isEmptySession(Session{Summary: "  summary "}) {
			t.Fatalf("expected summary to mark session as non-empty")
		}
		if !isEmptySession(Session{Summary: "  "}) {
			t.Fatalf("expected whitespace summary to be treated as empty")
		}
	})

	t.Run("filterEmptySessions preserves order", func(t *testing.T) {
		sessions := []Session{
			{SessionID: "empty-1"},
			{SessionID: "keep-1", FirstPrompt: "hi"},
			{SessionID: "empty-2"},
			{SessionID: "keep-2", Summary: "sum"},
		}
		filtered := filterEmptySessions(sessions)
		if len(filtered) != 2 {
			t.Fatalf("expected 2 sessions, got %d", len(filtered))
		}
		if filtered[0].SessionID != "keep-1" || filtered[1].SessionID != "keep-2" {
			t.Fatalf("unexpected order: %#v", filtered)
		}
	})
}
