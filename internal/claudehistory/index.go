package claudehistory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const EnvClaudeDir = "CLAUDE_DIR"

type sessionsIndex struct {
	Version      int                 `json:"version"`
	Entries      []sessionIndexEntry `json:"entries"`
	OriginalPath string              `json:"originalPath"`
}

type sessionIndexEntry struct {
	SessionID    string `json:"sessionId"`
	FullPath     string `json:"fullPath"`
	FileMtime    int64  `json:"fileMtime"`
	FirstPrompt  string `json:"firstPrompt"`
	Summary      string `json:"summary"`
	MessageCount int    `json:"messageCount"`
	Created      string `json:"created"`
	Modified     string `json:"modified"`
	GitBranch    string `json:"gitBranch"`
	ProjectPath  string `json:"projectPath"`
	IsSidechain  bool   `json:"isSidechain"`
}

func ResolveClaudeDir(override string) (string, error) {
	if v := strings.TrimSpace(override); v != "" {
		return filepath.Clean(os.ExpandEnv(v)), nil
	}
	if v := strings.TrimSpace(os.Getenv(EnvClaudeDir)); v != "" {
		return filepath.Clean(os.ExpandEnv(v)), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

func DiscoverProjects(claudeDir string) ([]Project, error) {
	root, err := ResolveClaudeDir(claudeDir)
	if err != nil {
		return nil, err
	}
	projectsDir := filepath.Join(root, "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("Claude data dir not found: %s", root)
		}
		return nil, fmt.Errorf("read projects dir: %w", err)
	}

	var firstErr error
	var projects []Project
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		key := entry.Name()
		indexPath := filepath.Join(projectsDir, key, "sessions-index.json")
		data, err := os.ReadFile(indexPath)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("read sessions index %s: %w", indexPath, err)
			}
			continue
		}
		var parsed sessionsIndex
		if err := json.Unmarshal(data, &parsed); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("parse sessions index %s: %w", indexPath, err)
			}
			continue
		}
		projectPath := strings.TrimSpace(parsed.OriginalPath)
		if projectPath == "" {
			projectPath = parsed.EntriesProjectPath()
		}
		project := Project{
			Key:      key,
			Path:     projectPath,
			Sessions: parseSessions(parsed.Entries),
		}
		projects = append(projects, project)
	}

	sort.Slice(projects, func(i, j int) bool {
		return strings.ToLower(projects[i].Path) < strings.ToLower(projects[j].Path)
	})
	return projects, firstErr
}

func (idx sessionsIndex) EntriesProjectPath() string {
	for _, entry := range idx.Entries {
		if entry.ProjectPath != "" {
			return entry.ProjectPath
		}
	}
	return ""
}

func parseSessions(entries []sessionIndexEntry) []Session {
	sessions := make([]Session, 0, len(entries))
	for _, entry := range entries {
		created := parseTime(entry.Created)
		modified := parseTime(entry.Modified)
		sessions = append(sessions, Session{
			SessionID:    entry.SessionID,
			Summary:      entry.Summary,
			FirstPrompt:  entry.FirstPrompt,
			MessageCount: entry.MessageCount,
			CreatedAt:    created,
			ModifiedAt:   modified,
			ProjectPath:  entry.ProjectPath,
			FilePath:     entry.FullPath,
		})
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].ModifiedAt.After(sessions[j].ModifiedAt)
	})
	return sessions
}

func parseTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t
	}
	return time.Time{}
}

func FindSessionByID(projects []Project, sessionID string) (Session, bool) {
	for _, project := range projects {
		for _, sess := range project.Sessions {
			if sess.SessionID == sessionID {
				return sess, true
			}
		}
	}
	return Session{}, false
}

func FindSessionWithProject(projects []Project, sessionID string) (Session, Project, bool) {
	for _, project := range projects {
		for _, sess := range project.Sessions {
			if sess.SessionID == sessionID {
				return sess, project, true
			}
		}
	}
	return Session{}, Project{}, false
}

func SessionWorkingDir(session Session, project Project) string {
	if strings.TrimSpace(session.ProjectPath) != "" {
		return strings.TrimSpace(session.ProjectPath)
	}
	if strings.TrimSpace(project.Path) != "" {
		return strings.TrimSpace(project.Path)
	}
	return ""
}
