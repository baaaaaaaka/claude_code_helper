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

	history, historyErr := loadHistoryIndex(root)

	var firstErr error
	var projects []Project
	if historyErr != nil {
		firstErr = fmt.Errorf("read history index: %w", historyErr)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		key := entry.Name()
		project, err := loadProject(projectsDir, key, history)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if len(project.Sessions) == 0 && strings.TrimSpace(project.Path) == "" {
			continue
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

func loadProject(projectsDir string, key string, history historyIndex) (Project, error) {
	dir := filepath.Join(projectsDir, key)
	indexPath := filepath.Join(dir, "sessions-index.json")
	data, err := os.ReadFile(indexPath)
	if err == nil {
		var parsed sessionsIndex
		if err := json.Unmarshal(data, &parsed); err == nil {
			sessions := parseSessions(parsed.Entries)
			for i := range sessions {
				if info, ok := history.lookup(sessions[i].SessionID); ok {
					if sessions[i].ProjectPath == "" && info.ProjectPath != "" {
						sessions[i].ProjectPath = info.ProjectPath
					}
					if sessions[i].FirstPrompt == "" && info.FirstPrompt != "" {
						sessions[i].FirstPrompt = info.FirstPrompt
					}
					if sessions[i].CreatedAt.IsZero() && !info.FirstPromptTime.IsZero() {
						sessions[i].CreatedAt = info.FirstPromptTime
					}
				}
			}
			projectPath := strings.TrimSpace(parsed.OriginalPath)
			if projectPath == "" {
				projectPath = parsed.EntriesProjectPath()
			}
			if projectPath == "" {
				projectPath = selectProjectPath(sessions)
			}
			sessions, validFiles, rehydrateErr := rehydrateSessionsFromFiles(dir, sessions, true)
			projectPath = resolveProjectPath(projectPath, sessions)
			if projectPath != "" {
				for i := range sessions {
					if strings.TrimSpace(sessions[i].ProjectPath) == "" {
						sessions[i].ProjectPath = projectPath
					}
				}
			}
			if len(sessions) == 0 || validFiles == 0 {
				scanned, scanErr := loadProjectFromSessionFilesWithOptions(dir, key, history, true)
				if scanErr == nil && len(scanned.Sessions) > 0 {
					scanned.Path = resolveProjectPath(projectPath, scanned.Sessions)
					if scanned.Path != "" {
						for i := range scanned.Sessions {
							if strings.TrimSpace(scanned.Sessions[i].ProjectPath) == "" {
								scanned.Sessions[i].ProjectPath = scanned.Path
							}
						}
					}
					return scanned, nil
				}
				if scanErr != nil {
					return Project{
						Key:      key,
						Path:     projectPath,
						Sessions: sessions,
					}, scanErr
				}
			}
			return Project{
				Key:      key,
				Path:     projectPath,
				Sessions: sessions,
			}, rehydrateErr
		}
	}

	project, scanErr := loadProjectFromSessionFilesWithOptions(dir, key, history, true)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		if scanErr != nil {
			return project, fmt.Errorf("read sessions index %s: %w", indexPath, err)
		}
		return project, fmt.Errorf("read sessions index %s: %w", indexPath, err)
	}
	return project, scanErr
}

func loadProjectFromSessionFiles(dir string, key string, history historyIndex) (Project, error) {
	return loadProjectFromSessionFilesWithOptions(dir, key, history, false)
}

func loadProjectFromSessionFilesWithOptions(dir string, key string, history historyIndex, recursive bool) (Project, error) {
	files, err := collectSessionFiles(dir, recursive)
	if err != nil {
		return Project{Key: key}, err
	}
	var firstErr error
	var sessions []Session
	seen := map[string]bool{}
	for _, filePath := range files {
		name := filepath.Base(filePath)
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		sessionID := strings.TrimSuffix(name, ".jsonl")
		if strings.TrimSpace(sessionID) == "" || seen[sessionID] {
			continue
		}
		seen[sessionID] = true
		meta, err := readSessionFileMeta(filePath)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("read session %s: %w", filePath, err)
			}
			continue
		}
		if info, ok := history.lookup(sessionID); ok {
			if info.ProjectPath != "" {
				meta.ProjectPath = info.ProjectPath
			}
			if meta.FirstPrompt == "" && info.FirstPrompt != "" {
				meta.FirstPrompt = info.FirstPrompt
			}
			if meta.CreatedAt.IsZero() && !info.FirstPromptTime.IsZero() {
				meta.CreatedAt = info.FirstPromptTime
			}
			if meta.ModifiedAt.IsZero() && !info.FirstPromptTime.IsZero() {
				meta.ModifiedAt = info.FirstPromptTime
			}
		}
		sessions = append(sessions, Session{
			SessionID:    sessionID,
			Summary:      "",
			FirstPrompt:  meta.FirstPrompt,
			MessageCount: meta.MessageCount,
			CreatedAt:    meta.CreatedAt,
			ModifiedAt:   meta.ModifiedAt,
			ProjectPath:  strings.TrimSpace(meta.ProjectPath),
			FilePath:     filePath,
		})
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].ModifiedAt.After(sessions[j].ModifiedAt)
	})
	projectPath := resolveProjectPath("", sessions)
	if projectPath != "" {
		for i := range sessions {
			if strings.TrimSpace(sessions[i].ProjectPath) == "" {
				sessions[i].ProjectPath = projectPath
			}
		}
	}

	return Project{
		Key:      key,
		Path:     projectPath,
		Sessions: sessions,
	}, firstErr
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
	sessionPath := strings.TrimSpace(session.ProjectPath)
	projectPath := strings.TrimSpace(project.Path)
	if isDir(sessionPath) {
		return sessionPath
	}
	if isDir(projectPath) {
		return projectPath
	}
	if sessionPath != "" {
		return sessionPath
	}
	if projectPath != "" {
		return projectPath
	}
	return ""
}
