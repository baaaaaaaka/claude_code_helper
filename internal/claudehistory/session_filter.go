package claudehistory

import "strings"

func filterEmptySessions(sessions []Session) []Session {
	if len(sessions) == 0 {
		return sessions
	}
	out := make([]Session, 0, len(sessions))
	for _, sess := range sessions {
		if isEmptySession(sess) {
			continue
		}
		out = append(out, sess)
	}
	return out
}

func isEmptySession(session Session) bool {
	if session.MessageCount > 0 {
		return false
	}
	if strings.TrimSpace(session.FirstPrompt) != "" {
		return false
	}
	if strings.TrimSpace(session.Summary) != "" {
		return false
	}
	if len(session.Subagents) > 0 {
		return false
	}
	return true
}
