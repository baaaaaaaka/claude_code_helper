package claudehistory

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type historyIndex struct {
	sessions map[string]*historySessionInfo
}

type historySessionInfo struct {
	ProjectPath     string
	FirstPrompt     string
	FirstPromptTime time.Time
}

type historyEntry struct {
	Display   string          `json:"display"`
	Project   string          `json:"project"`
	SessionID string          `json:"sessionId"`
	Timestamp json.RawMessage `json:"timestamp"`
}

func loadHistoryIndex(root string) (historyIndex, error) {
	idx := historyIndex{sessions: map[string]*historySessionInfo{}}
	path := filepath.Join(root, "history.jsonl")
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return idx, nil
		}
		return idx, err
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return idx, err
		}
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			var entry historyEntry
			if json.Unmarshal(line, &entry) == nil && entry.SessionID != "" {
				info := idx.sessions[entry.SessionID]
				if info == nil {
					info = &historySessionInfo{}
					idx.sessions[entry.SessionID] = info
				}
				project := strings.TrimSpace(entry.Project)
				if project != "" {
					info.ProjectPath = project
				}
				display := strings.TrimSpace(entry.Display)
				if display != "" && !isHistoryCommandDisplay(display) {
					ts := historyTimestamp(entry.Timestamp)
					if info.FirstPrompt == "" || (!ts.IsZero() && (info.FirstPromptTime.IsZero() || ts.Before(info.FirstPromptTime))) {
						info.FirstPrompt = display
						info.FirstPromptTime = ts
					}
				}
			}
		}
		if err == io.EOF {
			break
		}
	}
	return idx, nil
}

func (idx historyIndex) lookup(sessionID string) (historySessionInfo, bool) {
	if sessionID == "" || idx.sessions == nil {
		return historySessionInfo{}, false
	}
	info, ok := idx.sessions[sessionID]
	if !ok || info == nil {
		return historySessionInfo{}, false
	}
	return *info, true
}

func historyTimestamp(raw json.RawMessage) time.Time {
	if len(raw) == 0 {
		return time.Time{}
	}
	var num json.Number
	if err := json.Unmarshal(raw, &num); err == nil {
		if ms, err := num.Int64(); err == nil {
			return time.Unix(0, ms*int64(time.Millisecond))
		}
		if f, err := num.Float64(); err == nil {
			return time.Unix(0, int64(f*float64(time.Millisecond)))
		}
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if t := parseTime(s); !t.IsZero() {
			return t
		}
		if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
			return time.Unix(0, ms*int64(time.Millisecond))
		}
	}
	return time.Time{}
}

func isHistoryCommandDisplay(display string) bool {
	return strings.HasPrefix(strings.TrimSpace(display), "/")
}
