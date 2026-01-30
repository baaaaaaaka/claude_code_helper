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
