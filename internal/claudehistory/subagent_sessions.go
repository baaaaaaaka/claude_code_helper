package claudehistory

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

func attachSubagents(dir string, sessions []Session, recursive bool) ([]Session, error) {
	files, err := collectAgentSessionFiles(dir, recursive)
	if err != nil {
		return sessions, err
	}
	if len(files) == 0 || len(sessions) == 0 {
		return sessions, nil
	}

	sessionIndex := make(map[string]int, len(sessions))
	for i := range sessions {
		if sessions[i].SessionID == "" {
			continue
		}
		sessionIndex[sessions[i].SessionID] = i
	}

	var firstErr error
	for _, filePath := range files {
		parentSessionID, err := parentSessionIDForAgentFile(filePath)
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("read session id %s: %w", filePath, err)
		}
		if parentSessionID == "" {
			continue
		}
		idx, ok := sessionIndex[parentSessionID]
		if !ok {
			continue
		}
		meta, err := readSessionFileMetaCached(filePath)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("read session %s: %w", filePath, err)
			}
			continue
		}
		agentID := strings.TrimSuffix(filepath.Base(filePath), ".jsonl")
		agentID = strings.TrimPrefix(agentID, "agent-")
		projectPath := strings.TrimSpace(meta.ProjectPath)
		if projectPath == "" {
			projectPath = sessions[idx].ProjectPath
		}
		sessions[idx].Subagents = append(sessions[idx].Subagents, SubagentSession{
			AgentID:         agentID,
			ParentSessionID: parentSessionID,
			FirstPrompt:     meta.FirstPrompt,
			MessageCount:    meta.MessageCount,
			CreatedAt:       meta.CreatedAt,
			ModifiedAt:      meta.ModifiedAt,
			ProjectPath:     projectPath,
			FilePath:        filePath,
		})
	}

	for i := range sessions {
		if len(sessions[i].Subagents) < 2 {
			continue
		}
		sort.Slice(sessions[i].Subagents, func(a, b int) bool {
			return sessions[i].Subagents[a].ModifiedAt.After(sessions[i].Subagents[b].ModifiedAt)
		})
	}
	return sessions, firstErr
}

func parentSessionIDForAgentFile(filePath string) (string, error) {
	dir := filepath.Dir(filePath)
	if strings.EqualFold(filepath.Base(dir), "subagents") {
		parent := strings.TrimSpace(filepath.Base(filepath.Dir(dir)))
		if parent != "" {
			if _, err := readSessionFileSessionIDCached(filePath); err != nil {
				return "", err
			}
			return parent, nil
		}
	}
	return readSessionFileSessionIDCached(filePath)
}
