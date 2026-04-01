package claudehistory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
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

type SessionMatch struct {
	Session Session
	Project Project
}

type discoverProjectsOptions struct {
	parallel     bool
	projectCache bool
}

type discoverProjectResult struct {
	project Project
	err     error
}

var defaultDiscoverProjectsOptions = discoverProjectsOptions{
	parallel:     true,
	projectCache: true,
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
	return DiscoverProjectsContext(context.Background(), claudeDir)
}

func DiscoverProjectsContext(ctx context.Context, claudeDir string) ([]Project, error) {
	root, err := ResolveClaudeDir(claudeDir)
	if err != nil {
		return nil, err
	}
	return discoverProjectsResolvedContext(ctx, root, defaultDiscoverProjectsOptions)
}

func discoverProjectsResolvedContext(ctx context.Context, root string, opts discoverProjectsOptions) ([]Project, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, ownsBatch := ensureSessionMetaPersistentBatch(ctx)
	if ownsBatch {
		defer flushSessionMetaPersistentBatchContext(ctx)
	}
	projectsDir := filepath.Join(root, "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("Claude data dir not found: %s", root)
		}
		return nil, fmt.Errorf("read projects dir: %w", err)
	}

	history, historyDep, historyErr := loadHistoryIndexStateContext(ctx, root)
	effectiveOpts := opts
	if historyErr != nil {
		effectiveOpts.projectCache = false
	}

	projectKeys := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		projectKeys = append(projectKeys, entry.Name())
	}

	results := make([]discoverProjectResult, len(projectKeys))
	if effectiveOpts.parallel && len(projectKeys) > 1 {
		loadProjectsParallelContext(ctx, projectsDir, projectKeys, history, historyDep, effectiveOpts, results)
	} else {
		loadProjectsSerialContext(ctx, projectsDir, projectKeys, history, historyDep, effectiveOpts, results)
	}

	var firstErr error
	var projects []Project
	if historyErr != nil {
		firstErr = fmt.Errorf("read history index: %w", historyErr)
	}
	for _, result := range results {
		if result.err != nil && firstErr == nil {
			firstErr = result.err
		}
		if len(result.project.Sessions) == 0 && strings.TrimSpace(result.project.Path) == "" {
			continue
		}
		projects = append(projects, result.project)
	}

	sort.Slice(projects, func(i, j int) bool {
		return strings.ToLower(projects[i].Path) < strings.ToLower(projects[j].Path)
	})
	return projects, firstErr
}

func loadProjectsSerialContext(ctx context.Context, projectsDir string, projectKeys []string, history historyIndex, historyDep historyDependency, opts discoverProjectsOptions, results []discoverProjectResult) {
	for i, key := range projectKeys {
		if err := ctx.Err(); err != nil {
			results[i].err = err
			return
		}
		results[i].project, results[i].err = loadProjectContextWithOptions(ctx, projectsDir, key, history, historyDep, opts)
	}
}

func loadProjectsParallelContext(ctx context.Context, projectsDir string, projectKeys []string, history historyIndex, historyDep historyDependency, opts discoverProjectsOptions, results []discoverProjectResult) {
	workerCount := runtime.GOMAXPROCS(0)
	if workerCount < 2 {
		workerCount = 2
	}
	if workerCount > len(projectKeys) {
		workerCount = len(projectKeys)
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				if err := ctx.Err(); err != nil {
					results[idx].err = err
					continue
				}
				results[idx].project, results[idx].err = loadProjectContextWithOptions(ctx, projectsDir, projectKeys[idx], history, historyDep, opts)
			}
		}()
	}

	for i := range projectKeys {
		if err := ctx.Err(); err != nil {
			for j := i; j < len(projectKeys); j++ {
				results[j].err = err
			}
			break
		}
		jobs <- i
	}
	close(jobs)
	wg.Wait()
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
		sessionID := strings.TrimSpace(entry.SessionID)
		aliases := []string{}
		if canonicalID := sessionIDFromFilePath(entry.FullPath); canonicalID != "" && canonicalID != sessionID {
			if sessionID != "" {
				aliases = append(aliases, sessionID)
			}
			sessionID = canonicalID
		}
		summary := entry.Summary
		firstPrompt := entry.FirstPrompt
		if entry.IsSidechain && strings.TrimSpace(summary) == "" && strings.TrimSpace(firstPrompt) == "" {
			if strings.HasPrefix(sessionID, "agent-") {
				summary = sessionID
			} else {
				summary = "Subagent session"
			}
		}
		created := parseTime(entry.Created)
		modified := parseTime(entry.Modified)
		sessions = append(sessions, canonicalizeSessionIdentity(Session{
			SessionID:    sessionID,
			Aliases:      aliases,
			Summary:      summary,
			FirstPrompt:  firstPrompt,
			MessageCount: entry.MessageCount,
			CreatedAt:    created,
			ModifiedAt:   modified,
			ProjectPath:  entry.ProjectPath,
			FilePath:     entry.FullPath,
		}))
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

func scanSessionsFromFiles(dir string, history historyIndex, recursive bool) ([]Session, string, error) {
	return scanSessionsFromFilesContext(context.Background(), dir, history, recursive)
}

func scanSessionsFromFilesContext(ctx context.Context, dir string, history historyIndex, recursive bool) ([]Session, string, error) {
	files, err := collectSessionFilesContext(ctx, dir, recursive)
	if err != nil {
		return nil, "", err
	}
	var firstErr error
	sessions := make([]Session, 0, len(files))
	sessionIndex := map[string]int{}
	for _, filePath := range files {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		name := filepath.Base(filePath)
		if !strings.HasSuffix(name, ".jsonl") {
			continue
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
		meta, err := readSessionFileMetaCachedContext(ctx, filePath)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("read session %s: %w", filePath, err)
			}
			continue
		}
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

func mergeSessionMetadata(base Session, other Session) Session {
	base = canonicalizeSessionIdentity(base)
	other = canonicalizeSessionIdentity(other)

	basePath := strings.TrimSpace(base.FilePath)
	otherPath := strings.TrimSpace(other.FilePath)
	baseFileOK := isFile(basePath)
	otherFileOK := isFile(otherPath)
	if !baseFileOK && otherFileOK {
		base.FilePath = otherPath
	} else if basePath == "" && otherPath != "" {
		base.FilePath = otherPath
	}

	if base.Summary == "" && other.Summary != "" {
		base.Summary = other.Summary
	}
	if base.FirstPrompt == "" && other.FirstPrompt != "" {
		base.FirstPrompt = other.FirstPrompt
	}
	if other.MessageCount > base.MessageCount {
		base.MessageCount = other.MessageCount
	}

	if base.CreatedAt.IsZero() {
		base.CreatedAt = other.CreatedAt
	} else if !other.CreatedAt.IsZero() && other.CreatedAt.Before(base.CreatedAt) {
		base.CreatedAt = other.CreatedAt
	}

	if base.ModifiedAt.IsZero() {
		base.ModifiedAt = other.ModifiedAt
	} else if !other.ModifiedAt.IsZero() && other.ModifiedAt.After(base.ModifiedAt) {
		base.ModifiedAt = other.ModifiedAt
	}

	baseProjectPath := strings.TrimSpace(base.ProjectPath)
	otherProjectPath := strings.TrimSpace(other.ProjectPath)
	if baseProjectPath == "" && otherProjectPath != "" {
		base.ProjectPath = other.ProjectPath
	} else if baseProjectPath != "" && !isDir(baseProjectPath) && isDir(otherProjectPath) {
		base.ProjectPath = other.ProjectPath
	}

	if base.SessionID == "" && other.SessionID != "" {
		base.SessionID = other.SessionID
	}
	if other.SessionID != "" && other.SessionID != base.SessionID {
		base.Aliases = mergeSessionAliases(base.Aliases, other.SessionID)
	}
	base.Aliases = mergeSessionAliases(base.Aliases, other.Aliases...)
	return canonicalizeSessionIdentity(base)
}

func mergeSessions(indexSessions []Session, scannedSessions []Session) []Session {
	merged := make(map[string]Session, len(indexSessions)+len(scannedSessions))
	for _, sess := range indexSessions {
		sess = canonicalizeSessionIdentity(sess)
		sessionID := strings.TrimSpace(sess.SessionID)
		if sessionID == "" {
			continue
		}
		merged[sessionID] = sess
	}
	for _, sess := range scannedSessions {
		sess = canonicalizeSessionIdentity(sess)
		sessionID := strings.TrimSpace(sess.SessionID)
		if sessionID == "" {
			continue
		}
		if existing, ok := merged[sessionID]; ok {
			merged[sessionID] = mergeSessionMetadata(existing, sess)
			continue
		}
		mergedIntoStaleEntry := false
		for _, alias := range sess.Aliases {
			alias = strings.TrimSpace(alias)
			if alias == "" {
				continue
			}
			existing, ok := merged[alias]
			if !ok {
				continue
			}
			// Alias-based merge is only safe when the index entry has no valid file backing.
			if isFile(strings.TrimSpace(existing.FilePath)) {
				continue
			}
			merged[alias] = mergeSessionMetadata(existing, sess)
			mergedIntoStaleEntry = true
			break
		}
		if mergedIntoStaleEntry {
			continue
		}
		merged[sessionID] = sess
	}

	deduped := make(map[string]Session, len(merged))
	for _, sess := range merged {
		sess = canonicalizeSessionIdentity(sess)
		sessionID := strings.TrimSpace(sess.SessionID)
		if sessionID == "" {
			continue
		}
		if existing, ok := deduped[sessionID]; ok {
			deduped[sessionID] = mergeSessionMetadata(existing, sess)
			continue
		}
		deduped[sessionID] = sess
	}

	out := make([]Session, 0, len(deduped))
	for _, sess := range deduped {
		out = append(out, sess)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ModifiedAt.After(out[j].ModifiedAt)
	})
	return out
}

func dropAttachedSubagentSessions(sessions []Session) []Session {
	if len(sessions) == 0 {
		return sessions
	}
	subagentFiles := map[string]bool{}
	subagentIDs := map[string]bool{}
	for _, sess := range sessions {
		for _, sub := range sess.Subagents {
			if path := strings.TrimSpace(sub.FilePath); path != "" {
				subagentFiles[path] = true
			}
			if id := strings.TrimSpace(sub.AgentID); id != "" {
				subagentIDs["agent-"+id] = true
			}
		}
	}
	if len(subagentFiles) == 0 && len(subagentIDs) == 0 {
		return sessions
	}
	out := make([]Session, 0, len(sessions))
	for _, sess := range sessions {
		if path := strings.TrimSpace(sess.FilePath); path != "" && subagentFiles[path] {
			continue
		}
		if subagentIDs[strings.TrimSpace(sess.SessionID)] {
			continue
		}
		out = append(out, sess)
	}
	return out
}

func loadProject(projectsDir string, key string, history historyIndex) (Project, error) {
	return loadProjectContext(context.Background(), projectsDir, key, history)
}

func loadProjectContext(ctx context.Context, projectsDir string, key string, history historyIndex) (Project, error) {
	return loadProjectContextWithOptions(ctx, projectsDir, key, history, historyDependency{}, discoverProjectsOptions{})
}

func loadProjectContextWithOptions(ctx context.Context, projectsDir string, key string, history historyIndex, historyDep historyDependency, opts discoverProjectsOptions) (Project, error) {
	projectCtx, ownsBatch := ensureSessionMetaPersistentBatch(ctx)
	if ownsBatch {
		defer flushSessionMetaPersistentBatchContext(projectCtx)
	}

	dir := filepath.Join(projectsDir, key)
	inv, invErr := buildProjectInventoryContext(projectCtx, dir, true)
	if invErr != nil {
		return Project{}, invErr
	}
	useProjectCache := opts.projectCache && inv.projectCacheEligible(historyDep)
	if useProjectCache {
		if project, refreshedInv, ok, err := lookupPersistentProjectContext(projectCtx, dir, historyDep, inv); err != nil {
			return Project{}, err
		} else {
			inv = refreshedInv
			if ok {
				return project, nil
			}
		}
	}

	indexPath := filepath.Join(dir, "sessions-index.json")
	if err := projectCtx.Err(); err != nil {
		return Project{}, err
	}
	data, err := os.ReadFile(indexPath)
	if err == nil {
		var parsed sessionsIndex
		if err := json.Unmarshal(data, &parsed); err == nil {
			sessions := parseSessions(parsed.Entries)
			for i := range sessions {
				if err := projectCtx.Err(); err != nil {
					return Project{}, err
				}
				for _, lookupID := range append([]string{sessions[i].SessionID}, sessions[i].Aliases...) {
					info, ok := history.lookup(lookupID)
					if !ok {
						continue
					}
					if sessions[i].ProjectPath == "" && info.ProjectPath != "" {
						sessions[i].ProjectPath = info.ProjectPath
					}
					if sessions[i].FirstPrompt == "" && info.FirstPrompt != "" {
						sessions[i].FirstPrompt = info.FirstPrompt
					}
					if sessions[i].CreatedAt.IsZero() && !info.FirstPromptTime.IsZero() {
						sessions[i].CreatedAt = info.FirstPromptTime
					}
					break
				}
			}
			projectPath := strings.TrimSpace(parsed.OriginalPath)
			if projectPath == "" {
				projectPath = parsed.EntriesProjectPath()
			}
			if projectPath == "" {
				projectPath = selectProjectPath(sessions)
			}
			sessions, validFiles, rehydrateErr := rehydrateSessionsFromInventoryContext(projectCtx, &inv, sessions)
			scannedSessions, scannedPath, scanErr := scanSessionsFromInventoryContext(projectCtx, &inv, history)
			if validFiles == 0 && len(scannedSessions) > 0 {
				sessions = nil
			}
			sessions = mergeSessions(sessions, scannedSessions)
			if strings.TrimSpace(projectPath) == "" {
				projectPath = strings.TrimSpace(scannedPath)
			}
			projectPath = resolveProjectPath(projectPath, sessions)
			if projectPath != "" {
				for i := range sessions {
					if strings.TrimSpace(sessions[i].ProjectPath) == "" {
						sessions[i].ProjectPath = projectPath
					}
				}
			}
			sessions, attachErr := attachSubagentsFromInventoryContext(projectCtx, &inv, sessions)
			if rehydrateErr == nil && attachErr != nil {
				rehydrateErr = attachErr
			}
			if rehydrateErr == nil && scanErr != nil {
				rehydrateErr = scanErr
			}
			externalPaths := collectSessionExternalFilePaths(sessions, inv.fileKeys)
			sessions = dropAttachedSubagentSessions(sessions)
			sessions = filterEmptySessionsContext(projectCtx, sessions)
			project := Project{
				Key:      key,
				Path:     projectPath,
				Sessions: sessions,
			}
			if rehydrateErr == nil && useProjectCache {
				pathDeps := collectProjectPathDependencies(indexProjectPathCandidates(parsed), &inv, project)
				if manifest, ok := buildProjectCacheManifest(historyDep, inv, externalPaths, pathDeps); ok {
					storePersistentProject(dir, manifest, project)
				}
			}
			return project, rehydrateErr
		}
	}

	project, scanErr := loadProjectFromInventoryContext(projectCtx, &inv, key, history)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		if scanErr != nil {
			return project, fmt.Errorf("read sessions index %s: %w", indexPath, err)
		}
		return project, fmt.Errorf("read sessions index %s: %w", indexPath, err)
	}
	if scanErr == nil && useProjectCache {
		pathDeps := collectProjectPathDependencies(nil, &inv, project)
		if manifest, ok := buildProjectCacheManifest(historyDep, inv, collectSessionExternalFilePaths(project.Sessions, inv.fileKeys), pathDeps); ok {
			storePersistentProject(dir, manifest, project)
		}
	}
	return project, scanErr
}

func loadProjectFromSessionFiles(dir string, key string, history historyIndex) (Project, error) {
	return loadProjectFromSessionFilesWithOptionsContext(context.Background(), dir, key, history, false)
}

func loadProjectFromSessionFilesWithOptions(dir string, key string, history historyIndex, recursive bool) (Project, error) {
	return loadProjectFromSessionFilesWithOptionsContext(context.Background(), dir, key, history, recursive)
}

func loadProjectFromSessionFilesWithOptionsContext(ctx context.Context, dir string, key string, history historyIndex, recursive bool) (Project, error) {
	sessions, projectPath, firstErr := scanSessionsFromFilesContext(ctx, dir, history, recursive)
	sessions, attachErr := attachSubagentsContext(ctx, dir, sessions, recursive)
	if firstErr == nil && attachErr != nil {
		firstErr = attachErr
	}
	sessions = dropAttachedSubagentSessions(sessions)
	sessions = filterEmptySessionsContext(ctx, sessions)
	return Project{
		Key:      key,
		Path:     projectPath,
		Sessions: sessions,
	}, firstErr
}

func loadProjectFromInventoryContext(ctx context.Context, inv *projectInventory, key string, history historyIndex) (Project, error) {
	sessions, projectPath, firstErr := scanSessionsFromInventoryContext(ctx, inv, history)
	sessions, attachErr := attachSubagentsFromInventoryContext(ctx, inv, sessions)
	if firstErr == nil && attachErr != nil {
		firstErr = attachErr
	}
	sessions = dropAttachedSubagentSessions(sessions)
	sessions = filterEmptySessionsContext(ctx, sessions)
	return Project{
		Key:      key,
		Path:     projectPath,
		Sessions: sessions,
	}, firstErr
}

func indexProjectPathCandidates(parsed sessionsIndex) []string {
	seen := map[string]struct{}{}
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		seen[path] = struct{}{}
	}
	add(parsed.OriginalPath)
	for _, entry := range parsed.Entries {
		add(entry.ProjectPath)
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func FindSessionByIDMatch(projects []Project, sessionID string) (Session, bool, bool) {
	sess, _, ok, ambiguous := FindSessionWithProjectMatch(projects, sessionID)
	return sess, ok, ambiguous
}

func FindSessionByID(projects []Project, sessionID string) (Session, bool) {
	sess, ok, _ := FindSessionByIDMatch(projects, sessionID)
	return sess, ok
}

func FindSessionWithProjectMatch(projects []Project, sessionID string) (Session, Project, bool, bool) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return Session{}, Project{}, false, false
	}

	// Canonical ID always wins over alias matches.
	for _, project := range projects {
		for _, sess := range project.Sessions {
			if strings.TrimSpace(sess.SessionID) == sessionID {
				return sess, project, true, false
			}
		}
	}

	var matchedSession Session
	var matchedProject Project
	foundAliasMatch := false
	for _, project := range projects {
		for _, sess := range project.Sessions {
			if !sessionHasAlias(sess, sessionID) {
				continue
			}
			if foundAliasMatch {
				return Session{}, Project{}, false, true
			}
			matchedSession = sess
			matchedProject = project
			foundAliasMatch = true
		}
	}
	if foundAliasMatch {
		return matchedSession, matchedProject, true, false
	}
	return Session{}, Project{}, false, false
}

func FindSessionWithProject(projects []Project, sessionID string) (Session, Project, bool) {
	sess, project, ok, _ := FindSessionWithProjectMatch(projects, sessionID)
	return sess, project, ok
}

func FindSessionAliasMatches(projects []Project, sessionID string) []SessionMatch {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	matches := make([]SessionMatch, 0)
	seen := map[string]bool{}
	for _, project := range projects {
		for _, sess := range project.Sessions {
			if !sessionHasAlias(sess, sessionID) {
				continue
			}
			key := strings.TrimSpace(sess.SessionID) + "|" + strings.TrimSpace(sess.FilePath) + "|" + strings.TrimSpace(project.Path)
			if seen[key] {
				continue
			}
			seen[key] = true
			matches = append(matches, SessionMatch{
				Session: sess,
				Project: project,
			})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		aSession := strings.ToLower(strings.TrimSpace(matches[i].Session.SessionID))
		bSession := strings.ToLower(strings.TrimSpace(matches[j].Session.SessionID))
		if aSession != bSession {
			return aSession < bSession
		}
		aProject := strings.ToLower(strings.TrimSpace(matches[i].Project.Path))
		bProject := strings.ToLower(strings.TrimSpace(matches[j].Project.Path))
		if aProject != bProject {
			return aProject < bProject
		}
		aFile := strings.ToLower(strings.TrimSpace(matches[i].Session.FilePath))
		bFile := strings.ToLower(strings.TrimSpace(matches[j].Session.FilePath))
		return aFile < bFile
	})
	return matches
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
