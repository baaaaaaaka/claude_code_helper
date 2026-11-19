package claudehistory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func sessionIDFromFilePath(filePath string) string {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return ""
	}
	name := filepath.Base(filePath)
	if strings.HasSuffix(name, ".jsonl") {
		name = strings.TrimSuffix(name, ".jsonl")
	}
	return strings.TrimSpace(name)
}

func mergeSessionAliases(aliases []string, extra ...string) []string {
	out := make([]string, 0, len(aliases)+len(extra))
	out = append(out, aliases...)
	out = append(out, extra...)
	return out
}

func normalizeSessionAliases(aliases []string, canonical string) []string {
	canonical = strings.TrimSpace(canonical)
	if len(aliases) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(aliases))
	out := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" || alias == canonical || seen[alias] {
			continue
		}
		seen[alias] = true
		out = append(out, alias)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sessionHasAlias(session Session, sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	for _, alias := range session.Aliases {
		if strings.TrimSpace(alias) == sessionID {
			return true
		}
	}
	return false
}

func canonicalizeSessionIdentity(session Session) Session {
	sessionID := strings.TrimSpace(session.SessionID)
	canonical := sessionIDFromFilePath(session.FilePath)
	if canonical != "" && canonical != sessionID {
		if sessionID != "" {
			session.Aliases = mergeSessionAliases(session.Aliases, sessionID)
		}
		session.SessionID = canonical
	} else {
		session.SessionID = sessionID
	}
	session.Aliases = normalizeSessionAliases(session.Aliases, session.SessionID)
	return session
}

// ValidateSessionSource rejects a browser selection whose local source vanished
// or was replaced after discovery. Appends to the same file remain valid.
func ValidateSessionSource(session Session) error {
	if session.SourceVersion == "" {
		return nil // An index-only entry has no local file to check.
	}
	stale := func() error {
		return fmt.Errorf("session %q changed since history was loaded; refresh the list", session.SessionID)
	}
	if sessionIDFromFilePath(session.FilePath) != session.SessionID {
		return stale()
	}
	f, err := os.Open(session.FilePath)
	if err != nil {
		return stale()
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return stale()
	}
	key := fileCacheKey{}
	populateFileCacheKeyPlatform(session.FilePath, info, &key)
	if key.StrongIdentity {
		object := fmt.Sprintf("%d:%d:", key.Device, key.Inode)
		if !strings.HasPrefix(session.SourceVersion, object) {
			return stale()
		}
	}
	return nil
}
