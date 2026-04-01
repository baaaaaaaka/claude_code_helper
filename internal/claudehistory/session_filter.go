package claudehistory

import (
	"context"
	"strings"
)

func filterEmptySessions(sessions []Session) []Session {
	return filterEmptySessionsContext(context.Background(), sessions)
}

func filterEmptySessionsContext(ctx context.Context, sessions []Session) []Session {
	if len(sessions) == 0 {
		return sessions
	}
	out := make([]Session, 0, len(sessions))
	for _, sess := range sessions {
		if isEmptySessionContext(ctx, sess) {
			continue
		}
		out = append(out, sess)
	}
	return out
}

func isEmptySession(session Session) bool {
	return isEmptySessionContext(context.Background(), session)
}

func isEmptySessionContext(ctx context.Context, session Session) bool {
	if isFile(session.FilePath) {
		meta, err := readSessionFileMetaCachedContext(ctx, session.FilePath)
		if err == nil && meta.SnapshotOnly {
			return true
		}
		return false
	}
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
