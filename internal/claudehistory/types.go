package claudehistory

import "time"

type Project struct {
	Key      string
	Path     string
	Sessions []Session
}

type Session struct {
	SessionID    string
	Summary      string
	FirstPrompt  string
	MessageCount int
	CreatedAt    time.Time
	ModifiedAt   time.Time
	ProjectPath  string
	FilePath     string
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
