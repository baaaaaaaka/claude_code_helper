package claudehistory

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"
)

type sessionFileMeta struct {
	ProjectPath  string
	FirstPrompt  string
	MessageCount int
	CreatedAt    time.Time
	ModifiedAt   time.Time
}

func readSessionFileMeta(filePath string) (sessionFileMeta, error) {
	var meta sessionFileMeta
	f, err := os.Open(filePath)
	if err != nil {
		return meta, err
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return meta, err
		}
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			var env sessionEnvelopeMeta
			if json.Unmarshal(line, &env) == nil {
				if meta.ProjectPath == "" {
					if cwd := strings.TrimSpace(env.Cwd); cwd != "" {
						meta.ProjectPath = cwd
					}
				}
				if msg, ok := parseEnvelopeMessage(env.sessionEnvelope); ok {
					meta.MessageCount++
					if msg.Role == "user" && meta.FirstPrompt == "" {
						meta.FirstPrompt = msg.Content
					}
					if !msg.Timestamp.IsZero() {
						if meta.CreatedAt.IsZero() || msg.Timestamp.Before(meta.CreatedAt) {
							meta.CreatedAt = msg.Timestamp
						}
						if meta.ModifiedAt.IsZero() || msg.Timestamp.After(meta.ModifiedAt) {
							meta.ModifiedAt = msg.Timestamp
						}
					}
				}
			}
		}
		if err == io.EOF {
			break
		}
	}

	if meta.CreatedAt.IsZero() || meta.ModifiedAt.IsZero() {
		if st, err := os.Stat(filePath); err == nil {
			if meta.CreatedAt.IsZero() {
				meta.CreatedAt = st.ModTime()
			}
			if meta.ModifiedAt.IsZero() {
				meta.ModifiedAt = st.ModTime()
			}
		}
	}
	return meta, nil
}

type sessionIDEnvelope struct {
	SessionID string `json:"sessionId"`
}

func readSessionFileSessionID(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return "", err
		}
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			var env sessionIDEnvelope
			if json.Unmarshal(line, &env) == nil {
				if sessionID := strings.TrimSpace(env.SessionID); sessionID != "" {
					return sessionID, nil
				}
			}
		}
		if err == io.EOF {
			break
		}
	}
	return "", nil
}

func selectProjectPath(sessions []Session) string {
	counts := map[string]int{}
	for _, sess := range sessions {
		path := strings.TrimSpace(sess.ProjectPath)
		if path == "" {
			continue
		}
		counts[path]++
	}
	if len(counts) == 0 {
		return ""
	}
	best := ""
	bestCount := -1
	for path, count := range counts {
		if count > bestCount || (count == bestCount && strings.ToLower(path) < strings.ToLower(best)) {
			best = path
			bestCount = count
		}
	}
	return best
}

func selectProjectPathExisting(sessions []Session) string {
	counts := map[string]int{}
	for _, sess := range sessions {
		path := strings.TrimSpace(sess.ProjectPath)
		if path == "" || !isDir(path) {
			continue
		}
		counts[path]++
	}
	if len(counts) == 0 {
		return ""
	}
	best := ""
	bestCount := -1
	for path, count := range counts {
		if count > bestCount || (count == bestCount && strings.ToLower(path) < strings.ToLower(best)) {
			best = path
			bestCount = count
		}
	}
	return best
}

func resolveProjectPath(preferred string, sessions []Session) string {
	preferred = strings.TrimSpace(preferred)
	if preferred != "" && isDir(preferred) {
		return preferred
	}
	if existing := selectProjectPathExisting(sessions); existing != "" {
		return existing
	}
	if preferred != "" {
		return preferred
	}
	return selectProjectPath(sessions)
}
