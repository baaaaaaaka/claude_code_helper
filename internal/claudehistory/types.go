package claudehistory

import "time"

type Project struct {
	Key      string
	Path     string
	Sessions []Session
}

type SubagentSession struct {
	AgentID         string
	ParentSessionID string
	FirstPrompt     string
	MessageCount    int
	CreatedAt       time.Time
	ModifiedAt      time.Time
	ProjectPath     string
	FilePath        string
}

func (s SubagentSession) DisplayTitle() string {
	if s.FirstPrompt != "" {
		return s.FirstPrompt
	}
	if s.AgentID != "" {
		return "agent-" + s.AgentID
	}
	return "Subagent session"
}

type Session struct {
	SessionID    string
	Aliases      []string `json:"-"`
	Summary      string
	FirstPrompt  string
	MessageCount int
	CreatedAt    time.Time
	ModifiedAt   time.Time
	ProjectPath  string
	FilePath     string
	Subagents    []SubagentSession `json:"-"`
}

func (s Session) DisplayTitle() string {
	if s.Summary != "" {
		return s.Summary
	}
	if s.FirstPrompt != "" {
		return s.FirstPrompt
	}
	if s.SessionID != "" {
		return s.SessionID
	}
	return "Untitled session"
}
