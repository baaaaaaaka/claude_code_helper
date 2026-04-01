package claudehistory

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

func scanSessionsFromInventoryContext(ctx context.Context, inv *projectInventory, history historyIndex) ([]Session, string, error) {
	var firstErr error
	sessions := make([]Session, 0, len(inv.sessionFiles))
	sessionIndex := map[string]int{}
	for _, filePath := range inv.sessionFiles {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		sessionID, err := resolveSessionIDFromFileCached(filePath)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("read session id %s: %w", filePath, err)
			}
			continue
		}
		if strings.TrimSpace(sessionID) == "" {
			continue
		}
		key, ok := inv.fileKey(filePath)
		var meta sessionFileMeta
		if ok {
			meta, err = readSessionFileMetaCachedWithKeyContext(ctx, filePath, key)
		} else {
			meta, err = readSessionFileMetaCachedContext(ctx, filePath)
		}
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("read session %s: %w", filePath, err)
			}
			continue
		}
		inv.rememberSessionMetaProjectPath(meta.ProjectPath)
		aliases := sessionAliasesFromMeta(meta, sessionID)
		for _, lookupID := range append([]string{sessionID}, aliases...) {
			info, ok := history.lookup(lookupID)
			if !ok {
				continue
			}
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
			break
		}
		session := canonicalizeSessionIdentity(Session{
			SessionID:    sessionID,
			Aliases:      aliases,
			Summary:      "",
			FirstPrompt:  meta.FirstPrompt,
			MessageCount: meta.MessageCount,
			CreatedAt:    meta.CreatedAt,
			ModifiedAt:   meta.ModifiedAt,
			ProjectPath:  strings.TrimSpace(meta.ProjectPath),
			FilePath:     filePath,
		})
		if existingIdx, ok := sessionIndex[sessionID]; ok {
			sessions[existingIdx] = mergeSessionMetadata(sessions[existingIdx], session)
			continue
		}
		sessionIndex[sessionID] = len(sessions)
		sessions = append(sessions, session)
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
	return sessions, projectPath, firstErr
}

func rehydrateSessionsFromInventoryContext(ctx context.Context, inv *projectInventory, sessions []Session) ([]Session, int, error) {
	var firstErr error
	validFiles := 0
	updated := false

	for i := range sessions {
		if err := ctx.Err(); err != nil {
			return sessions, validFiles, err
		}
		sessionID := strings.TrimSpace(sessions[i].SessionID)
		if sessionID == "" {
			continue
		}

		filePath := strings.TrimSpace(sessions[i].FilePath)
		if filePath != "" {
			if _, ok := inv.fileKey(filePath); !ok && !isFile(filePath) {
				filePath = ""
			}
		}
		if filePath == "" {
			resolved := inv.sessionPath(sessionID)
			if resolved != "" {
				filePath = resolved
				sessions[i].FilePath = resolved
				updated = true
			}
		}
		if filePath == "" {
			continue
		}
		key, ok := inv.fileKey(filePath)
		if !ok && !isFile(filePath) {
			continue
		}

		validFiles++
		var meta sessionFileMeta
		var err error
		if ok {
			meta, err = readSessionFileMetaCachedWithKeyContext(ctx, filePath, key)
		} else {
			meta, err = readSessionFileMetaCachedContext(ctx, filePath)
		}
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("read session %s: %w", filePath, err)
			}
			continue
		}
		inv.rememberSessionMetaProjectPath(meta.ProjectPath)

		if sessions[i].FirstPrompt == "" && meta.FirstPrompt != "" {
			sessions[i].FirstPrompt = meta.FirstPrompt
			updated = true
		}
		if sessions[i].MessageCount == 0 && meta.MessageCount > 0 {
			sessions[i].MessageCount = meta.MessageCount
			updated = true
		}
		if sessions[i].CreatedAt.IsZero() && !meta.CreatedAt.IsZero() {
			sessions[i].CreatedAt = meta.CreatedAt
			updated = true
		}
		if sessions[i].ModifiedAt.IsZero() && !meta.ModifiedAt.IsZero() {
			sessions[i].ModifiedAt = meta.ModifiedAt
			updated = true
		}
		if sessions[i].ProjectPath == "" && meta.ProjectPath != "" {
			sessions[i].ProjectPath = meta.ProjectPath
			updated = true
		} else if sessions[i].ProjectPath != "" && !isDir(sessions[i].ProjectPath) && meta.ProjectPath != "" && isDir(meta.ProjectPath) {
			sessions[i].ProjectPath = meta.ProjectPath
			updated = true
		}
	}

	if updated {
		sort.Slice(sessions, func(i, j int) bool {
			return sessions[i].ModifiedAt.After(sessions[j].ModifiedAt)
		})
	}
	return sessions, validFiles, firstErr
}

func attachSubagentsFromInventoryContext(ctx context.Context, inv *projectInventory, sessions []Session) ([]Session, error) {
	if len(inv.agentFiles) == 0 || len(sessions) == 0 {
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
	for _, filePath := range inv.agentFiles {
		if err := ctx.Err(); err != nil {
			return sessions, err
		}
		key, ok := inv.fileKey(filePath)
		var parentSessionID string
		var err error
		if ok {
			parentSessionID, err = parentSessionIDForAgentFileWithKey(filePath, key)
		} else {
			parentSessionID, err = parentSessionIDForAgentFile(filePath)
		}
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
		var meta sessionFileMeta
		if ok {
			meta, err = readSessionFileMetaCachedWithKeyContext(ctx, filePath, key)
		} else {
			meta, err = readSessionFileMetaCachedContext(ctx, filePath)
		}
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

func parentSessionIDForAgentFileWithKey(filePath string, key fileCacheKey) (string, error) {
	dir := filepath.Dir(filePath)
	if strings.EqualFold(filepath.Base(dir), "subagents") {
		parent := strings.TrimSpace(filepath.Base(filepath.Dir(dir)))
		if parent != "" {
			if _, err := readSessionFileSessionIDCachedWithKey(filePath, key); err != nil {
				return "", err
			}
			return parent, nil
		}
	}
	return readSessionFileSessionIDCachedWithKey(filePath, key)
}
