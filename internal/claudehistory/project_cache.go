package claudehistory

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

const projectPersistentCacheVersion = 5

var revalidateProjectCacheInventoryContext = buildProjectInventoryContext

type historyDependency struct {
	Path   string
	Exists bool
	Key    fileCacheKey
}

type projectCacheFileState struct {
	Path string       `json:"path"`
	Key  fileCacheKey `json:"key"`
}

type projectCachePathState struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

type projectCacheManifest struct {
	HistoryExists bool                    `json:"historyExists"`
	HistoryKey    fileCacheKey            `json:"historyKey"`
	IndexExists   bool                    `json:"indexExists"`
	IndexKey      fileCacheKey            `json:"indexKey"`
	SessionFiles  []projectCacheFileState `json:"sessionFiles"`
	AgentFiles    []projectCacheFileState `json:"agentFiles"`
	ExternalFiles []projectCacheFileState `json:"externalFiles,omitempty"`
	PathStates    []projectCachePathState `json:"pathStates"`
}

type persistentProject struct {
	Key      string                     `json:"key"`
	Path     string                     `json:"path"`
	Sessions []persistentProjectSession `json:"sessions"`
}

type persistentProjectSession struct {
	SessionID    string                      `json:"sessionId"`
	Aliases      []string                    `json:"aliases,omitempty"`
	Summary      string                      `json:"summary"`
	FirstPrompt  string                      `json:"firstPrompt"`
	MessageCount int                         `json:"messageCount"`
	CreatedAt    time.Time                   `json:"createdAt"`
	ModifiedAt   time.Time                   `json:"modifiedAt"`
	ProjectPath  string                      `json:"projectPath"`
	FilePath     string                      `json:"filePath"`
	Subagents    []persistentSubagentSession `json:"subagents,omitempty"`
}

type persistentSubagentSession struct {
	AgentID         string    `json:"agentId"`
	ParentSessionID string    `json:"parentSessionId"`
	FirstPrompt     string    `json:"firstPrompt"`
	MessageCount    int       `json:"messageCount"`
	CreatedAt       time.Time `json:"createdAt"`
	ModifiedAt      time.Time `json:"modifiedAt"`
	ProjectPath     string    `json:"projectPath"`
	FilePath        string    `json:"filePath"`
}

type persistentProjectCacheEntry struct {
	Manifest projectCacheManifest `json:"manifest"`
	Project  persistentProject    `json:"project"`
}

type persistentProjectCacheFile struct {
	Version int                         `json:"version"`
	Dir     string                      `json:"dir"`
	Entry   persistentProjectCacheEntry `json:"entry"`
}

func lookupPersistentProjectContext(ctx context.Context, dir string, history historyDependency, inv projectInventory) (Project, projectInventory, bool, error) {
	path, err := projectPersistentCachePathForDir(dir)
	if err != nil {
		return Project{}, inv, false, nil
	}
	entry, ok := readPersistentProjectCacheEntry(path, dir)
	if !ok || !entry.Manifest.matchesCurrent(history, inv) {
		return Project{}, inv, false, nil
	}

	revalidatedInv, err := revalidateProjectCacheInventoryContext(ctx, dir, inv.recursive)
	if err != nil {
		return Project{}, inv, false, err
	}
	if !sameProjectInventorySnapshot(inv, revalidatedInv) {
		return Project{}, revalidatedInv, false, nil
	}
	return projectFromPersistent(entry.Project), revalidatedInv, true, nil
}

func storePersistentProject(dir string, manifest projectCacheManifest, project Project) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return
	}
	path, err := projectPersistentCachePathForDir(dir)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}

	lock := flock.New(path + ".lock")
	locked, err := lock.TryLock()
	if err != nil || !locked {
		return
	}
	defer func() { _ = lock.Unlock() }()

	payload := persistentProjectCacheFile{
		Version: projectPersistentCacheVersion,
		Dir:     filepath.Clean(dir),
		Entry: persistentProjectCacheEntry{
			Manifest: cloneProjectCacheManifest(manifest),
			Project:  projectToPersistent(project),
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	data = append(data, '\n')
	_ = atomicWriteFile(path, data, 0o600)
}

func deletePersistentProject(dir string) {
	path, err := projectPersistentCachePathForDir(dir)
	if err != nil {
		return
	}
	_ = os.Remove(path)
}

func buildProjectCacheManifest(history historyDependency, inv projectInventory, externalPaths []string, pathDeps []string) (projectCacheManifest, bool) {
	manifest := projectCacheManifest{
		HistoryExists: history.Exists,
		HistoryKey:    history.Key,
		IndexExists:   inv.indexExists,
		IndexKey:      inv.indexKey,
		SessionFiles:  make([]projectCacheFileState, 0, len(inv.sessionFiles)),
		AgentFiles:    make([]projectCacheFileState, 0, len(inv.agentFiles)),
		PathStates:    snapshotProjectCachePathStates(pathDeps),
	}
	for _, path := range inv.sessionFiles {
		key, ok := inv.fileKey(path)
		if !ok {
			continue
		}
		manifest.SessionFiles = append(manifest.SessionFiles, projectCacheFileState{Path: path, Key: key})
	}
	for _, path := range inv.agentFiles {
		key, ok := inv.fileKey(path)
		if !ok {
			continue
		}
		manifest.AgentFiles = append(manifest.AgentFiles, projectCacheFileState{Path: path, Key: key})
	}
	externalFiles, ok := snapshotProjectExternalCacheFiles(externalPaths)
	if !ok {
		return projectCacheManifest{}, false
	}
	manifest.ExternalFiles = externalFiles
	return manifest, true
}

func collectProjectPathDependencies(indexPaths []string, inv *projectInventory, project Project) []string {
	seen := map[string]struct{}{}
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
	}

	for _, path := range indexPaths {
		add(path)
	}
	for path := range inv.sessionMetaProjectPaths {
		add(path)
	}
	add(project.Path)
	for _, sess := range project.Sessions {
		add(sess.ProjectPath)
	}

	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func snapshotProjectCachePathStates(paths []string) []projectCachePathState {
	if len(paths) == 0 {
		return nil
	}
	states := make([]projectCachePathState, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		states = append(states, projectCachePathState{
			Path:   path,
			Exists: isDir(path),
		})
	}
	sort.Slice(states, func(i, j int) bool { return states[i].Path < states[j].Path })
	return states
}

func (m projectCacheManifest) matchesCurrent(history historyDependency, inv projectInventory) bool {
	if m.HistoryExists != history.Exists || m.IndexExists != inv.indexExists {
		return false
	}
	if m.HistoryExists && m.HistoryKey != history.Key {
		return false
	}
	if m.IndexExists && m.IndexKey != inv.indexKey {
		return false
	}
	if !inv.indexReadable() {
		return false
	}
	if !equalProjectCacheFiles(m.SessionFiles, snapshotProjectCacheFiles(inv.sessionFiles, inv.fileKeys)) {
		return false
	}
	if !equalProjectCacheFiles(m.AgentFiles, snapshotProjectCacheFiles(inv.agentFiles, inv.fileKeys)) {
		return false
	}
	currentExternalFiles, ok := snapshotProjectExternalCacheFiles(projectCacheFilePaths(m.ExternalFiles))
	if !ok || !equalProjectCacheFiles(m.ExternalFiles, currentExternalFiles) {
		return false
	}
	for _, file := range m.SessionFiles {
		if !inv.fileReadable(file.Path) {
			return false
		}
	}
	for _, file := range m.AgentFiles {
		if !inv.fileReadable(file.Path) {
			return false
		}
	}
	for _, state := range m.PathStates {
		if isDir(state.Path) != state.Exists {
			return false
		}
	}
	return true
}

type projectCacheReadableState struct {
	Path     string
	Readable bool
}

type projectInventorySnapshot struct {
	IndexExists   bool
	IndexReadable bool
	IndexKey      fileCacheKey
	SessionFiles  []projectCacheFileState
	AgentFiles    []projectCacheFileState
	ReadableFiles []projectCacheReadableState
}

func sameProjectInventorySnapshot(a, b projectInventory) bool {
	left := snapshotProjectInventory(a)
	right := snapshotProjectInventory(b)
	if left.IndexExists != right.IndexExists || left.IndexReadable != right.IndexReadable || left.IndexKey != right.IndexKey {
		return false
	}
	if !equalProjectCacheFiles(left.SessionFiles, right.SessionFiles) {
		return false
	}
	if !equalProjectCacheFiles(left.AgentFiles, right.AgentFiles) {
		return false
	}
	if len(left.ReadableFiles) != len(right.ReadableFiles) {
		return false
	}
	for i := range left.ReadableFiles {
		if left.ReadableFiles[i] != right.ReadableFiles[i] {
			return false
		}
	}
	return true
}

func snapshotProjectInventory(inv projectInventory) projectInventorySnapshot {
	return projectInventorySnapshot{
		IndexExists:   inv.indexExists,
		IndexReadable: inv.indexReadable(),
		IndexKey:      inv.indexKey,
		SessionFiles:  snapshotProjectCacheFiles(inv.sessionFiles, inv.fileKeys),
		AgentFiles:    snapshotProjectCacheFiles(inv.agentFiles, inv.fileKeys),
		ReadableFiles: snapshotProjectReadableStates(inv),
	}
}

func snapshotProjectReadableStates(inv projectInventory) []projectCacheReadableState {
	paths := make([]string, 0, len(inv.sessionFiles)+len(inv.agentFiles))
	paths = append(paths, inv.sessionFiles...)
	paths = append(paths, inv.agentFiles...)
	sort.Strings(paths)

	states := make([]projectCacheReadableState, 0, len(paths))
	for _, path := range paths {
		states = append(states, projectCacheReadableState{
			Path:     path,
			Readable: inv.fileReadable(path),
		})
	}
	return states
}

func snapshotProjectCacheFiles(paths []string, keys map[string]fileCacheKey) []projectCacheFileState {
	if len(paths) == 0 {
		return nil
	}
	out := make([]projectCacheFileState, 0, len(paths))
	for _, path := range paths {
		key, ok := keys[path]
		if !ok {
			continue
		}
		out = append(out, projectCacheFileState{Path: path, Key: key})
	}
	return out
}

func collectSessionExternalFilePaths(sessions []Session, known map[string]fileCacheKey) []string {
	seen := map[string]struct{}{}
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		if _, ok := known[path]; ok {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
	}

	for _, session := range sessions {
		add(session.FilePath)
		for _, sub := range session.Subagents {
			add(sub.FilePath)
		}
	}

	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func snapshotProjectExternalCacheFiles(paths []string) ([]projectCacheFileState, bool) {
	if len(paths) == 0 {
		return nil, true
	}
	out := make([]projectCacheFileState, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		key, err := currentFileCacheKey(path)
		if err != nil || !key.usableForPersistentCache() || validateFileReadable(path) != nil {
			return nil, false
		}
		out = append(out, projectCacheFileState{Path: path, Key: key})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, true
}

func projectCacheFilePaths(files []projectCacheFileState) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	return paths
}

func equalProjectCacheFiles(a, b []projectCacheFileState) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func readPersistentProjectCacheEntry(path string, dir string) (persistentProjectCacheEntry, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return persistentProjectCacheEntry{}, false
	}
	var payload persistentProjectCacheFile
	if err := json.Unmarshal(data, &payload); err != nil || payload.Version != projectPersistentCacheVersion {
		return persistentProjectCacheEntry{}, false
	}
	if filepath.Clean(strings.TrimSpace(payload.Dir)) != filepath.Clean(strings.TrimSpace(dir)) {
		return persistentProjectCacheEntry{}, false
	}
	return persistentProjectCacheEntry{
		Manifest: cloneProjectCacheManifest(payload.Entry.Manifest),
		Project:  clonePersistentProject(payload.Entry.Project),
	}, true
}

func cloneProjectCacheManifest(manifest projectCacheManifest) projectCacheManifest {
	out := manifest
	out.SessionFiles = append([]projectCacheFileState(nil), manifest.SessionFiles...)
	out.AgentFiles = append([]projectCacheFileState(nil), manifest.AgentFiles...)
	out.ExternalFiles = append([]projectCacheFileState(nil), manifest.ExternalFiles...)
	out.PathStates = append([]projectCachePathState(nil), manifest.PathStates...)
	return out
}

func clonePersistentProject(project persistentProject) persistentProject {
	out := project
	out.Sessions = make([]persistentProjectSession, len(project.Sessions))
	for i, session := range project.Sessions {
		out.Sessions[i] = clonePersistentProjectSession(session)
	}
	return out
}

func clonePersistentProjectSession(session persistentProjectSession) persistentProjectSession {
	out := session
	out.Aliases = append([]string(nil), session.Aliases...)
	out.Subagents = append([]persistentSubagentSession(nil), session.Subagents...)
	return out
}

func projectToPersistent(project Project) persistentProject {
	out := persistentProject{
		Key:      project.Key,
		Path:     project.Path,
		Sessions: make([]persistentProjectSession, len(project.Sessions)),
	}
	for i, session := range project.Sessions {
		subagents := make([]persistentSubagentSession, len(session.Subagents))
		for j, sub := range session.Subagents {
			subagents[j] = persistentSubagentSession{
				AgentID:         sub.AgentID,
				ParentSessionID: sub.ParentSessionID,
				FirstPrompt:     sub.FirstPrompt,
				MessageCount:    sub.MessageCount,
				CreatedAt:       sub.CreatedAt,
				ModifiedAt:      sub.ModifiedAt,
				ProjectPath:     sub.ProjectPath,
				FilePath:        sub.FilePath,
			}
		}
		out.Sessions[i] = persistentProjectSession{
			SessionID:    session.SessionID,
			Aliases:      append([]string(nil), session.Aliases...),
			Summary:      session.Summary,
			FirstPrompt:  session.FirstPrompt,
			MessageCount: session.MessageCount,
			CreatedAt:    session.CreatedAt,
			ModifiedAt:   session.ModifiedAt,
			ProjectPath:  session.ProjectPath,
			FilePath:     session.FilePath,
			Subagents:    subagents,
		}
	}
	return out
}

func projectFromPersistent(project persistentProject) Project {
	out := Project{
		Key:      project.Key,
		Path:     project.Path,
		Sessions: make([]Session, len(project.Sessions)),
	}
	for i, session := range project.Sessions {
		var subagents []SubagentSession
		if len(session.Subagents) > 0 {
			subagents = make([]SubagentSession, len(session.Subagents))
			for j, sub := range session.Subagents {
				subagents[j] = SubagentSession{
					AgentID:         sub.AgentID,
					ParentSessionID: sub.ParentSessionID,
					FirstPrompt:     sub.FirstPrompt,
					MessageCount:    sub.MessageCount,
					CreatedAt:       sub.CreatedAt,
					ModifiedAt:      sub.ModifiedAt,
					ProjectPath:     sub.ProjectPath,
					FilePath:        sub.FilePath,
				}
			}
		}
		out.Sessions[i] = Session{
			SessionID:    session.SessionID,
			Aliases:      append([]string(nil), session.Aliases...),
			Summary:      session.Summary,
			FirstPrompt:  session.FirstPrompt,
			MessageCount: session.MessageCount,
			CreatedAt:    session.CreatedAt,
			ModifiedAt:   session.ModifiedAt,
			ProjectPath:  session.ProjectPath,
			FilePath:     session.FilePath,
			Subagents:    subagents,
		}
	}
	return out
}

func projectPersistentCacheDir() (string, error) {
	dir, err := claudeHistoryCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "project_cache"), nil
}

func projectPersistentCachePathForDir(dir string) (string, error) {
	base, err := projectPersistentCacheDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(filepath.Clean(strings.TrimSpace(dir))))
	name := fmt.Sprintf("%x", sum[:])
	return filepath.Join(base, name[:2], name+".json"), nil
}

func resetProjectPersistentCacheForTest() {}
