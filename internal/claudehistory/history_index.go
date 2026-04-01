package claudehistory

import (
	"bufio"
	"bytes"
	"context"
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
	return loadHistoryIndexContext(context.Background(), root)
}

func loadHistoryIndexContext(ctx context.Context, root string) (historyIndex, error) {
	idx, _, err := loadHistoryIndexStateContext(ctx, root)
	return idx, err
}

func loadHistoryIndexStateContext(ctx context.Context, root string) (historyIndex, historyDependency, error) {
	idx := historyIndex{sessions: map[string]*historySessionInfo{}}
	dep := historyDependency{Path: filepath.Join(root, "history.jsonl")}
	if err := ctx.Err(); err != nil {
		return idx, dep, err
	}
	key, err := currentFileCacheKey(dep.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			deletePersistentHistoryIndex(dep.Path)
			return idx, dep, nil
		}
		return idx, dep, err
	}
	dep.Exists = true
	dep.Key = key
	if cached, ok := lookupPersistentHistoryIndex(dep.Path, key); ok {
		return cached, dep, nil
	}

	f, err := os.Open(dep.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			deletePersistentHistoryIndex(dep.Path)
			dep.Exists = false
			dep.Key = fileCacheKey{}
			return idx, dep, nil
		}
		return idx, dep, err
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	for {
		if err := ctx.Err(); err != nil {
			return idx, dep, err
		}
		line, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return idx, dep, err
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
				if display != "" && !shouldSkipFirstPrompt(display) {
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
	if stableKey, stable := verifyStableFileCacheKey(dep.Path, key); stable {
		dep.Key = stableKey
		storePersistentHistoryIndex(dep.Path, stableKey, idx)
	}
	return idx, dep, nil
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
