package claudehistory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDiscoverProjectsModesMatchExactly(t *testing.T) {
	useTempClaudeHistoryCacheDir(t)

	root := writeDiscoverProjectsModesFixture(t)

	baselineProjects, baselineErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{})
	parallelProjects, parallelErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: false,
	})
	cacheColdProjects, cacheColdErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: true,
	})

	resetSessionFileCache()
	resetSessionPersistentCacheForTest()
	resetHistoryIndexPersistentCacheForTest()
	resetProjectPersistentCacheForTest()

	cacheWarmProjects, cacheWarmErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: true,
	})

	if len(baselineProjects) != 2 || len(baselineProjects[0].Sessions) < 2 || len(baselineProjects[0].Sessions[1].Subagents) != 1 {
		t.Fatalf("fixture should exercise subagent attachment, got %#v", baselineProjects)
	}
	if len(baselineProjects[1].Sessions) != 1 || !reflect.DeepEqual(baselineProjects[1].Sessions[0].Aliases, []string{"legacy-2"}) {
		t.Fatalf("fixture should exercise alias preservation, got %#v", baselineProjects)
	}

	assertDiscoverProjectsResultEqual(t, "parallel", baselineProjects, baselineErr, parallelProjects, parallelErr)
	assertDiscoverProjectsResultEqual(t, "cache-cold", baselineProjects, baselineErr, cacheColdProjects, cacheColdErr)
	assertDiscoverProjectsResultEqual(t, "cache-warm", baselineProjects, baselineErr, cacheWarmProjects, cacheWarmErr)
}

func TestDiscoverProjectsModesMatchExactlyWithIndexedExternalFullPath(t *testing.T) {
	useTempClaudeHistoryCacheDir(t)

	root, externalPath, workspace := writeIndexedExternalFullPathFixture(t)

	baselineProjects, baselineErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{})
	parallelProjects, parallelErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: false,
	})
	cacheColdProjects, cacheColdErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: true,
	})

	resetSessionFileCache()
	resetSessionPersistentCacheForTest()
	resetHistoryIndexPersistentCacheForTest()
	resetProjectPersistentCacheForTest()

	cacheWarmProjects, cacheWarmErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: true,
	})

	if len(baselineProjects) != 1 || len(baselineProjects[0].Sessions) != 1 {
		t.Fatalf("expected one external session project, got %#v", baselineProjects)
	}
	session := baselineProjects[0].Sessions[0]
	if session.FilePath != externalPath {
		t.Fatalf("expected external fullPath to be preserved, got %q", session.FilePath)
	}
	if session.FirstPrompt != "External prompt" || session.MessageCount != 2 {
		t.Fatalf("expected external metadata to be rehydrated, got %#v", session)
	}
	if baselineProjects[0].Path != workspace {
		t.Fatalf("expected project path from external file, got %#v", baselineProjects[0])
	}

	assertDiscoverProjectsResultEqual(t, "external-fullpath-parallel", baselineProjects, baselineErr, parallelProjects, parallelErr)
	assertDiscoverProjectsResultEqual(t, "external-fullpath-cache-cold", baselineProjects, baselineErr, cacheColdProjects, cacheColdErr)
	assertDiscoverProjectsResultEqual(t, "external-fullpath-cache-warm", baselineProjects, baselineErr, cacheWarmProjects, cacheWarmErr)
}

func TestDiscoverProjectsProjectCacheWarmHitAvoidsSessionMetaReads(t *testing.T) {
	useTempClaudeHistoryCacheDir(t)

	root := writeDiscoverProjectsModesFixture(t)
	if _, err := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: true,
	}); err != nil {
		t.Fatalf("prime discover error: %v", err)
	}

	resetSessionFileCache()
	resetSessionPersistentCacheForTest()
	resetHistoryIndexPersistentCacheForTest()
	resetProjectPersistentCacheForTest()

	projects, err := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: true,
	})
	if err != nil {
		t.Fatalf("warm discover error: %v", err)
	}
	if len(projects) == 0 {
		t.Fatalf("expected warm discover to return projects")
	}
	metaReads, _ := sessionFileCacheStatsSnapshot()
	if metaReads != 0 {
		t.Fatalf("expected project cache warm hit to avoid session meta parsing, got %d reads", metaReads)
	}
}

func TestDiscoverProjectsParallelSessionMetaPersistentCacheWarmHitAcrossShards(t *testing.T) {
	useTempClaudeHistoryCacheDir(t)

	root := t.TempDir()
	projectsDir := filepath.Join(root, "projects")
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(projectsDir, 0o755); err != nil {
		t.Fatalf("mkdir projects: %v", err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	projectKeys := make([]string, 0, 3)
	seenShards := map[uint8]struct{}{}
	for i := 0; len(projectKeys) < 3 && i < 256; i++ {
		key := fmt.Sprintf("proj-shard-%03d", i)
		sessionPath := filepath.Join(projectsDir, key, "sess-main.jsonl")
		shard := sessionMetaPersistentShardForPath(sessionPath)
		if _, ok := seenShards[shard]; ok {
			continue
		}
		seenShards[shard] = struct{}{}
		projectKeys = append(projectKeys, key)
	}
	if len(projectKeys) < 3 {
		t.Fatalf("expected to find at least 3 distinct shard targets, got %d", len(projectKeys))
	}

	for _, key := range projectKeys {
		projectDir := filepath.Join(projectsDir, key)
		if err := os.MkdirAll(projectDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", key, err)
		}
		content := fmt.Sprintf(
			`{"type":"user","message":{"role":"user","content":%s},"timestamp":"2026-01-01T00:00:00Z","cwd":%s}`,
			jsonString(t, "Prompt for "+key),
			jsonString(t, workspace),
		)
		if err := os.WriteFile(filepath.Join(projectDir, "sess-main.jsonl"), []byte(content), 0o644); err != nil {
			t.Fatalf("write session for %s: %v", key, err)
		}
	}

	coldProjects, coldErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: false,
	})
	if coldErr != nil {
		t.Fatalf("cold discover error: %v", coldErr)
	}

	resetSessionFileCache()
	resetSessionPersistentCacheForTest()
	resetHistoryIndexPersistentCacheForTest()

	warmProjects, warmErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: false,
	})
	if warmErr != nil {
		t.Fatalf("warm discover error: %v", warmErr)
	}

	assertDiscoverProjectsResultEqual(t, "parallel-session-meta-warm", coldProjects, coldErr, warmProjects, warmErr)

	metaReads, _ := sessionFileCacheStatsSnapshot()
	if metaReads != 0 {
		t.Fatalf("expected warm persistent session-meta cache hit across shards, got %d meta reads", metaReads)
	}
}

func TestDiscoverProjectsProjectCacheInvalidatesOnHistoryChange(t *testing.T) {
	useTempClaudeHistoryCacheDir(t)

	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-history")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	sessionPath := filepath.Join(projectDir, "sess-1.jsonl")
	content := `{"type":"user","isMeta":true,"message":{"role":"user","content":"ignore"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project-history"}`
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	historyPath := filepath.Join(root, "history.jsonl")
	writeHistory := func(prompt string) {
		t.Helper()
		history := `{"display":"` + prompt + `","timestamp":1700000000000,"project":"/tmp/project-history","sessionId":"sess-1"}`
		if err := os.WriteFile(historyPath, []byte(history), 0o644); err != nil {
			t.Fatalf("write history: %v", err)
		}
	}

	writeHistory("Initial prompt")
	firstProjects, firstErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: true,
	})
	if firstErr != nil {
		t.Fatalf("first discover error: %v", firstErr)
	}

	resetSessionFileCache()
	resetSessionPersistentCacheForTest()
	resetHistoryIndexPersistentCacheForTest()
	resetProjectPersistentCacheForTest()

	writeHistory("Updated prompt")
	gotProjects, gotErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: true,
	})
	wantProjects, wantErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{})

	assertDiscoverProjectsResultEqual(t, "history-change", wantProjects, wantErr, gotProjects, gotErr)
	if reflect.DeepEqual(firstProjects, gotProjects) {
		t.Fatalf("expected history change to invalidate project cache result")
	}
}

func TestDiscoverProjectsDisablesProjectCacheWhenHistoryIndexReadFails(t *testing.T) {
	useTempClaudeHistoryCacheDir(t)

	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-history-error")
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	sessionPath := filepath.Join(projectDir, "sess-1.jsonl")
	content := fmt.Sprintf(`{"type":"assistant","message":{"role":"assistant","content":"reply only"},"timestamp":"2026-01-01T00:00:00Z","cwd":%s}`,
		jsonString(t, workspace),
	)
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	historyPath := filepath.Join(root, "history.jsonl")
	historyContent := fmt.Sprintf(`{"display":"History prompt","timestamp":1700000000000,"project":%s,"sessionId":"sess-1"}`,
		jsonString(t, workspace),
	)
	if err := os.WriteFile(historyPath, []byte(historyContent), 0o644); err != nil {
		t.Fatalf("write history: %v", err)
	}

	firstProjects, firstErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: true,
	})
	if firstErr != nil {
		t.Fatalf("first discover error: %v", firstErr)
	}
	if len(firstProjects) != 1 || len(firstProjects[0].Sessions) != 1 || firstProjects[0].Sessions[0].FirstPrompt != "History prompt" {
		t.Fatalf("expected primed project cache to include history prompt, got %#v", firstProjects)
	}

	resetSessionFileCache()
	resetSessionPersistentCacheForTest()
	resetHistoryIndexPersistentCacheForTest()
	resetProjectPersistentCacheForTest()

	if err := os.Remove(historyPath); err != nil {
		t.Fatalf("remove history file: %v", err)
	}
	if err := os.Mkdir(historyPath, 0o755); err != nil {
		t.Fatalf("mkdir history path: %v", err)
	}

	gotProjects, gotErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: true,
	})
	wantProjects, wantErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{})

	assertDiscoverProjectsResultEqual(t, "history-read-error-disables-project-cache", wantProjects, wantErr, gotProjects, gotErr)
	if gotErr == nil || !strings.Contains(gotErr.Error(), "read history index") {
		t.Fatalf("expected history index read error, got %v", gotErr)
	}
	if reflect.DeepEqual(firstProjects, gotProjects) {
		t.Fatalf("expected broken history to bypass stale project cache result")
	}
	if len(gotProjects) != 1 || len(gotProjects[0].Sessions) != 1 || gotProjects[0].Sessions[0].FirstPrompt != "" {
		t.Fatalf("expected fallback load without stale history prompt, got %#v", gotProjects)
	}
}

func TestDiscoverProjectsProjectCacheInvalidatesOnNewSessionFile(t *testing.T) {
	useTempClaudeHistoryCacheDir(t)

	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-new-file")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	session1 := filepath.Join(projectDir, "sess-1.jsonl")
	content1 := `{"type":"user","message":{"role":"user","content":"one"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project-new-file"}`
	if err := os.WriteFile(session1, []byte(content1), 0o644); err != nil {
		t.Fatalf("write session1: %v", err)
	}

	if _, err := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{parallel: true, projectCache: true}); err != nil {
		t.Fatalf("prime discover error: %v", err)
	}

	resetSessionFileCache()
	resetSessionPersistentCacheForTest()
	resetHistoryIndexPersistentCacheForTest()
	resetProjectPersistentCacheForTest()

	session2 := filepath.Join(projectDir, "sess-2.jsonl")
	content2 := `{"type":"user","message":{"role":"user","content":"two"},"timestamp":"2026-01-02T00:00:00Z","cwd":"/tmp/project-new-file"}`
	if err := os.WriteFile(session2, []byte(content2), 0o644); err != nil {
		t.Fatalf("write session2: %v", err)
	}

	gotProjects, gotErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{parallel: true, projectCache: true})
	wantProjects, wantErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{})
	assertDiscoverProjectsResultEqual(t, "new-session-file", wantProjects, wantErr, gotProjects, gotErr)

	if len(gotProjects) != 1 || len(gotProjects[0].Sessions) != 2 {
		t.Fatalf("expected new session file to appear, got %#v", gotProjects)
	}
}

func TestDiscoverProjectsProjectCacheInvalidatesOnIndexChange(t *testing.T) {
	useTempClaudeHistoryCacheDir(t)

	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-index")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	sessionPath := filepath.Join(projectDir, "sess-1.jsonl")
	content := `{"type":"user","message":{"role":"user","content":"Hello"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project-index"}`
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	indexPath := filepath.Join(projectDir, "sessions-index.json")
	writeIndex := func(summary string) {
		t.Helper()
		index := fmt.Sprintf(`{"version":1,"entries":[{"sessionId":"sess-1","fullPath":%s,"summary":%s,"projectPath":"/tmp/project-index"}],"originalPath":"/tmp/project-index"}`,
			jsonString(t, sessionPath),
			jsonString(t, summary),
		)
		if err := os.WriteFile(indexPath, []byte(index), 0o644); err != nil {
			t.Fatalf("write index: %v", err)
		}
	}

	writeIndex("From index v1")
	firstProjects, firstErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{parallel: true, projectCache: true})
	if firstErr != nil {
		t.Fatalf("first discover error: %v", firstErr)
	}

	resetSessionFileCache()
	resetSessionPersistentCacheForTest()
	resetHistoryIndexPersistentCacheForTest()
	resetProjectPersistentCacheForTest()

	writeIndex("From index v2")
	gotProjects, gotErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{parallel: true, projectCache: true})
	wantProjects, wantErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{})
	assertDiscoverProjectsResultEqual(t, "index-change", wantProjects, wantErr, gotProjects, gotErr)

	if reflect.DeepEqual(firstProjects, gotProjects) {
		t.Fatalf("expected index change to invalidate project cache result")
	}
}

func TestDiscoverProjectsProjectCacheMissesWhenIndexBecomesUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based readability checks are Unix-only")
	}

	useTempClaudeHistoryCacheDir(t)

	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-index-unreadable")
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	sessionPath := filepath.Join(projectDir, "sess-1.jsonl")
	sessionContent := fmt.Sprintf(`{"type":"user","message":{"role":"user","content":"from-scan"},"timestamp":"2026-01-01T00:00:00Z","cwd":%s}`,
		jsonString(t, workspace),
	)
	if err := os.WriteFile(sessionPath, []byte(sessionContent), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	indexPath := filepath.Join(projectDir, "sessions-index.json")
	indexContent := fmt.Sprintf(`{"version":1,"entries":[{"sessionId":"sess-1","fullPath":%s,"summary":"From index","projectPath":%s}],"originalPath":%s}`,
		jsonString(t, sessionPath),
		jsonString(t, workspace),
		jsonString(t, workspace),
	)
	if err := os.WriteFile(indexPath, []byte(indexContent), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	firstProjects, firstErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: true,
	})
	if firstErr != nil {
		t.Fatalf("first discover error: %v", firstErr)
	}
	if len(firstProjects) != 1 || len(firstProjects[0].Sessions) != 1 || firstProjects[0].Sessions[0].Summary != "From index" {
		t.Fatalf("expected primed cache to include index summary, got %#v", firstProjects)
	}

	resetSessionFileCache()
	resetSessionPersistentCacheForTest()
	resetHistoryIndexPersistentCacheForTest()
	resetProjectPersistentCacheForTest()

	if err := os.Chmod(indexPath, 0o000); err != nil {
		t.Fatalf("chmod index unreadable: %v", err)
	}
	defer func() { _ = os.Chmod(indexPath, 0o644) }()

	gotProjects, gotErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: true,
	})
	wantProjects, wantErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{})
	assertDiscoverProjectsResultEqual(t, "index-unreadable", wantProjects, wantErr, gotProjects, gotErr)

	if gotErr == nil || !strings.Contains(gotErr.Error(), "read sessions index") {
		t.Fatalf("expected unreadable index error, got %v", gotErr)
	}
	if reflect.DeepEqual(firstProjects, gotProjects) {
		t.Fatalf("expected unreadable index to bypass stale project cache result")
	}
}

func TestDiscoverProjectsProjectCacheInvalidatesOnProjectPathDirStateChange(t *testing.T) {
	useTempClaudeHistoryCacheDir(t)

	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-path-state")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	validPath := filepath.Join(root, "workspace")
	if err := os.MkdirAll(validPath, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	indexPath := filepath.Join(projectDir, "sessions-index.json")
	index := `{"version":1,"entries":[{"sessionId":"sess-1","fullPath":"","projectPath":"/missing/project"}],"originalPath":"/missing/project"}`
	if err := os.WriteFile(indexPath, []byte(index), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	sessionPath := filepath.Join(projectDir, "sess-1.jsonl")
	content := fmt.Sprintf(`{"type":"user","message":{"role":"user","content":"Hello"},"timestamp":"2026-01-01T00:00:00Z","cwd":%s}`, jsonString(t, validPath))
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	firstProjects, firstErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{parallel: true, projectCache: true})
	if firstErr != nil {
		t.Fatalf("first discover error: %v", firstErr)
	}
	if len(firstProjects) != 1 || firstProjects[0].Path != validPath {
		t.Fatalf("unexpected initial project path: %#v", firstProjects)
	}

	resetSessionFileCache()
	resetSessionPersistentCacheForTest()
	resetHistoryIndexPersistentCacheForTest()
	resetProjectPersistentCacheForTest()

	if err := os.RemoveAll(validPath); err != nil {
		t.Fatalf("remove valid path: %v", err)
	}

	gotProjects, gotErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{parallel: true, projectCache: true})
	wantProjects, wantErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{})
	assertDiscoverProjectsResultEqual(t, "path-dir-state", wantProjects, wantErr, gotProjects, gotErr)

	if reflect.DeepEqual(firstProjects, gotProjects) {
		t.Fatalf("expected project path dir-state change to invalidate project cache result")
	}
	if len(gotProjects) != 1 || gotProjects[0].Path != "/missing/project" {
		t.Fatalf("expected fallback project path after dir removal, got %#v", gotProjects)
	}
}

func TestDiscoverProjectsProjectCacheMissesWhenSessionBecomesUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based readability checks are Unix-only")
	}

	useTempClaudeHistoryCacheDir(t)

	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-session-unreadable")
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	sessionPath := filepath.Join(projectDir, "sess-1.jsonl")
	content := fmt.Sprintf(`{"type":"user","message":{"role":"user","content":"from-session"},"timestamp":"2026-01-01T00:00:00Z","cwd":%s}`,
		jsonString(t, workspace),
	)
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	firstProjects, firstErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: true,
	})
	if firstErr != nil {
		t.Fatalf("first discover error: %v", firstErr)
	}
	if len(firstProjects) != 1 || len(firstProjects[0].Sessions) != 1 {
		t.Fatalf("expected primed cache with one session, got %#v", firstProjects)
	}

	resetSessionFileCache()
	resetSessionPersistentCacheForTest()
	resetHistoryIndexPersistentCacheForTest()
	resetProjectPersistentCacheForTest()

	if err := os.Chmod(sessionPath, 0o000); err != nil {
		t.Fatalf("chmod session unreadable: %v", err)
	}
	defer func() { _ = os.Chmod(sessionPath, 0o644) }()

	gotProjects, gotErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: true,
	})
	wantProjects, wantErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{})
	assertDiscoverProjectsResultEqual(t, "session-unreadable", wantProjects, wantErr, gotProjects, gotErr)

	if gotErr == nil || !strings.Contains(gotErr.Error(), "read session") {
		t.Fatalf("expected unreadable session error, got %v", gotErr)
	}
	if reflect.DeepEqual(firstProjects, gotProjects) {
		t.Fatalf("expected unreadable session to bypass stale project cache result")
	}
}

func TestDiscoverProjectsProjectCacheWarmHitOnlyRereadsChangedProject(t *testing.T) {
	useTempClaudeHistoryCacheDir(t)

	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	writeProject := func(key, prompt string) string {
		t.Helper()
		projectDir := filepath.Join(root, "projects", key)
		if err := os.MkdirAll(projectDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", key, err)
		}
		sessionPath := filepath.Join(projectDir, "sess-main.jsonl")
		content := fmt.Sprintf(`{"type":"user","message":{"role":"user","content":%s},"timestamp":"2026-01-01T00:00:00Z","cwd":%s}`,
			jsonString(t, prompt),
			jsonString(t, workspace),
		)
		if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", key, err)
		}
		return sessionPath
	}

	sessionA := writeProject("proj-a", "Alpha")
	_ = writeProject("proj-b", "Beta")

	firstProjects, firstErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: true,
	})
	if firstErr != nil {
		t.Fatalf("first discover error: %v", firstErr)
	}

	resetSessionFileCache()
	resetSessionPersistentCacheForTest()
	resetHistoryIndexPersistentCacheForTest()
	resetProjectPersistentCacheForTest()

	updatedContent := fmt.Sprintf(`{"type":"user","message":{"role":"user","content":%s},"timestamp":"2026-01-02T00:00:00Z","cwd":%s}`,
		jsonString(t, "Alpha updated"),
		jsonString(t, workspace),
	)
	if err := os.WriteFile(sessionA, []byte(updatedContent), 0o644); err != nil {
		t.Fatalf("rewrite proj-a session: %v", err)
	}

	gotProjects, gotErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: true,
	})
	wantProjects, wantErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{})
	assertDiscoverProjectsResultEqual(t, "project-cache-partial-invalidation", wantProjects, wantErr, gotProjects, gotErr)

	if reflect.DeepEqual(firstProjects, gotProjects) {
		t.Fatalf("expected changed project to invalidate only its own cache result")
	}
	metaReads, _ := sessionFileCacheStatsSnapshot()
	if metaReads != 1 {
		t.Fatalf("expected only changed project to reparse session metadata, got %d reads", metaReads)
	}
}

func TestDiscoverProjectsProjectCacheInvalidatesOnIndexedExternalFullPathChange(t *testing.T) {
	useTempClaudeHistoryCacheDir(t)

	root, externalPath, _ := writeIndexedExternalFullPathFixture(t)

	firstProjects, firstErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: true,
	})
	if firstErr != nil {
		t.Fatalf("first discover error: %v", firstErr)
	}

	resetSessionFileCache()
	resetSessionPersistentCacheForTest()
	resetHistoryIndexPersistentCacheForTest()
	resetProjectPersistentCacheForTest()

	updatedContent := `{"type":"user","message":{"role":"user","content":"Updated external prompt"},"timestamp":"2026-01-08T00:00:00Z","cwd":"/tmp/project-external"}
{"type":"assistant","message":{"role":"assistant","content":"Reply"},"timestamp":"2026-01-08T00:01:00Z","cwd":"/tmp/project-external"}
{"type":"user","message":{"role":"user","content":"Followup"},"timestamp":"2026-01-08T00:02:00Z","cwd":"/tmp/project-external"}`
	if err := os.WriteFile(externalPath, []byte(updatedContent), 0o644); err != nil {
		t.Fatalf("rewrite external session: %v", err)
	}

	gotProjects, gotErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: true,
	})
	wantProjects, wantErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{})
	assertDiscoverProjectsResultEqual(t, "external-fullpath-change", wantProjects, wantErr, gotProjects, gotErr)

	if reflect.DeepEqual(firstProjects, gotProjects) {
		t.Fatalf("expected external fullPath change to invalidate project cache result")
	}
}

func TestDiscoverProjectsProjectCacheInvalidatesWhenFilteredIndexedExternalFullPathBecomesVisible(t *testing.T) {
	useTempClaudeHistoryCacheDir(t)

	root, externalPath, workspace := writeFilteredIndexedExternalFullPathFixture(t)

	firstProjects, firstErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: true,
	})
	if firstErr != nil {
		t.Fatalf("first discover error: %v", firstErr)
	}
	if len(firstProjects) != 1 || firstProjects[0].Path != workspace || len(firstProjects[0].Sessions) != 0 {
		t.Fatalf("expected filtered external session to produce an empty project placeholder, got %#v", firstProjects)
	}

	resetSessionFileCache()
	resetSessionPersistentCacheForTest()
	resetHistoryIndexPersistentCacheForTest()
	resetProjectPersistentCacheForTest()

	updatedContent := fmt.Sprintf(
		`{"type":"user","message":{"role":"user","content":"Visible external prompt"},"timestamp":"2026-01-09T00:00:00Z","cwd":%s}
{"type":"assistant","message":{"role":"assistant","content":"Reply"},"timestamp":"2026-01-09T00:01:00Z","cwd":%s}`,
		jsonString(t, workspace),
		jsonString(t, workspace),
	)
	if err := os.WriteFile(externalPath, []byte(updatedContent), 0o644); err != nil {
		t.Fatalf("rewrite filtered external session: %v", err)
	}

	gotProjects, gotErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: true,
	})
	wantProjects, wantErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{})
	assertDiscoverProjectsResultEqual(t, "filtered-external-fullpath-visible", wantProjects, wantErr, gotProjects, gotErr)

	if len(gotProjects) != 1 || len(gotProjects[0].Sessions) != 1 {
		t.Fatalf("expected visible external session after rewrite, got %#v", gotProjects)
	}
	if gotProjects[0].Sessions[0].FirstPrompt != "Visible external prompt" {
		t.Fatalf("expected rewritten external prompt, got %#v", gotProjects[0].Sessions[0])
	}
	if reflect.DeepEqual(firstProjects, gotProjects) {
		t.Fatalf("expected filtered external session rewrite to invalidate project cache result")
	}
}

func TestDiscoverProjectsProjectCacheWarmHitRevalidatesInventoryBeforeReturn(t *testing.T) {
	useTempClaudeHistoryCacheDir(t)

	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-revalidate")
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	writeSession := func(name, prompt string) {
		t.Helper()
		content := fmt.Sprintf(`{"type":"user","message":{"role":"user","content":%s},"timestamp":"2026-01-01T00:00:00Z","cwd":%s}`,
			jsonString(t, prompt),
			jsonString(t, workspace),
		)
		if err := os.WriteFile(filepath.Join(projectDir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	writeSession("sess-1.jsonl", "one")
	firstProjects, firstErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: true,
	})
	if firstErr != nil {
		t.Fatalf("first discover error: %v", firstErr)
	}
	if len(firstProjects) != 1 || len(firstProjects[0].Sessions) != 1 {
		t.Fatalf("expected primed cache with one session, got %#v", firstProjects)
	}

	resetSessionFileCache()
	resetSessionPersistentCacheForTest()
	resetHistoryIndexPersistentCacheForTest()
	resetProjectPersistentCacheForTest()

	prevRevalidate := revalidateProjectCacheInventoryContext
	t.Cleanup(func() { revalidateProjectCacheInventoryContext = prevRevalidate })

	injected := false
	revalidateProjectCacheInventoryContext = func(ctx context.Context, dir string, recursive bool) (projectInventory, error) {
		if !injected {
			injected = true
			writeSession("sess-2.jsonl", "two")
		}
		return buildProjectInventoryContext(ctx, dir, recursive)
	}

	gotProjects, gotErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: true,
	})
	wantProjects, wantErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{})
	assertDiscoverProjectsResultEqual(t, "project-cache-revalidate-before-return", wantProjects, wantErr, gotProjects, gotErr)

	if len(gotProjects) != 1 || len(gotProjects[0].Sessions) != 2 {
		t.Fatalf("expected revalidated load to include new session, got %#v", gotProjects)
	}
	metaReads, _ := sessionFileCacheStatsSnapshot()
	if metaReads == 0 {
		t.Fatalf("expected stale warm hit to fall back to full loader")
	}
}

func TestDiscoverProjectsProjectCacheInvalidatesOnAgentFileChange(t *testing.T) {
	useTempClaudeHistoryCacheDir(t)

	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-agent")
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	sessionPath := filepath.Join(projectDir, "sess-main.jsonl")
	sessionContent := fmt.Sprintf(`{"type":"user","message":{"role":"user","content":"Main"},"timestamp":"2026-01-01T00:00:00Z","cwd":%s}`, jsonString(t, workspace))
	if err := os.WriteFile(sessionPath, []byte(sessionContent), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	agentPath := filepath.Join(projectDir, "agent-abc.jsonl")
	writeAgent := func(prompt string) {
		t.Helper()
		content := fmt.Sprintf(`{"type":"user","message":{"role":"user","content":%s},"timestamp":"2026-01-02T00:00:00Z","cwd":%s,"sessionId":"sess-main","isSidechain":true}`,
			jsonString(t, prompt),
			jsonString(t, workspace),
		)
		if err := os.WriteFile(agentPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write agent: %v", err)
		}
	}

	writeAgent("First subtask")
	firstProjects, firstErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{parallel: true, projectCache: true})
	if firstErr != nil {
		t.Fatalf("first discover error: %v", firstErr)
	}

	resetSessionFileCache()
	resetSessionPersistentCacheForTest()
	resetHistoryIndexPersistentCacheForTest()
	resetProjectPersistentCacheForTest()

	writeAgent("Updated task")
	gotProjects, gotErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{parallel: true, projectCache: true})
	wantProjects, wantErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{})
	assertDiscoverProjectsResultEqual(t, "agent-change", wantProjects, wantErr, gotProjects, gotErr)

	if reflect.DeepEqual(firstProjects, gotProjects) {
		t.Fatalf("expected agent change to invalidate project cache result")
	}
}

func TestDiscoverProjectsProjectCacheMissesAfterSessionReplacementWithSameMtime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("replacement semantics differ on Windows")
	}

	useTempClaudeHistoryCacheDir(t)

	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "proj-replace")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	path := filepath.Join(projectDir, "sess-1.jsonl")
	originalContent := `{"type":"user","message":{"role":"user","content":"Alpha"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project-replace"}`
	if err := os.WriteFile(path, []byte(originalContent), 0o644); err != nil {
		t.Fatalf("write original session: %v", err)
	}

	originalTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, originalTime, originalTime); err != nil {
		t.Fatalf("chtimes original: %v", err)
	}

	firstProjects, firstErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{parallel: true, projectCache: true})
	if firstErr != nil {
		t.Fatalf("first discover error: %v", firstErr)
	}

	resetSessionFileCache()
	resetSessionPersistentCacheForTest()
	resetHistoryIndexPersistentCacheForTest()
	resetProjectPersistentCacheForTest()

	replacementPath := filepath.Join(projectDir, "replacement.jsonl")
	replacementContent := `{"type":"user","message":{"role":"user","content":"Omega"},"timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/project-replace"}`
	if len(replacementContent) != len(originalContent) {
		t.Fatalf("test requires same-size replacement, got %d vs %d", len(replacementContent), len(originalContent))
	}
	if err := os.WriteFile(replacementPath, []byte(replacementContent), 0o644); err != nil {
		t.Fatalf("write replacement: %v", err)
	}
	if err := os.Chtimes(replacementPath, originalTime, originalTime); err != nil {
		t.Fatalf("chtimes replacement: %v", err)
	}
	if err := os.Rename(replacementPath, path); err != nil {
		t.Fatalf("rename replacement: %v", err)
	}
	if err := os.Chtimes(path, originalTime, originalTime); err != nil {
		t.Fatalf("chtimes final: %v", err)
	}

	gotProjects, gotErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{parallel: true, projectCache: true})
	wantProjects, wantErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{})
	assertDiscoverProjectsResultEqual(t, "same-mtime-replacement", wantProjects, wantErr, gotProjects, gotErr)

	if reflect.DeepEqual(firstProjects, gotProjects) {
		t.Fatalf("expected same-mtime replacement to invalidate project cache result")
	}
}

func TestDiscoverProjectsParallelPreservesFirstErrorOrdering(t *testing.T) {
	useTempClaudeHistoryCacheDir(t)

	root := t.TempDir()
	for _, key := range []string{"a-broken", "z-broken"} {
		projectDir := filepath.Join(root, "projects", key)
		if err := os.MkdirAll(projectDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", key, err)
		}
		if err := os.Mkdir(filepath.Join(projectDir, "sessions-index.json"), 0o755); err != nil {
			t.Fatalf("mkdir broken index path: %v", err)
		}
	}

	serialProjects, serialErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{})
	parallelProjects, parallelErr := discoverProjectsResolvedContext(context.Background(), root, discoverProjectsOptions{
		parallel:     true,
		projectCache: false,
	})

	assertDiscoverProjectsResultEqual(t, "error-order", serialProjects, serialErr, parallelProjects, parallelErr)
	if parallelErr == nil || !strings.Contains(parallelErr.Error(), "a-broken") {
		t.Fatalf("expected first error to come from first project, got %v", parallelErr)
	}
}

func writeDiscoverProjectsModesFixture(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	validPath := filepath.Join(root, "workspace")
	if err := os.MkdirAll(validPath, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	projectDir := filepath.Join(root, "projects", "proj-repeat")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir repeat project: %v", err)
	}

	indexPath := filepath.Join(projectDir, "sessions-index.json")
	index := fmt.Sprintf(
		`{"version":1,"entries":[{"sessionId":"sess-main","summary":"From index","fullPath":"","messageCount":0,"projectPath":%s},{"sessionId":"agent-ignored","summary":"Sidechain from index","fullPath":"","isSidechain":true,"messageCount":5},{"sessionId":"sess-snapshot","summary":"Snapshot from index","fullPath":"","messageCount":3,"projectPath":%s}],"originalPath":%s}`,
		jsonString(t, filepath.Join(root, "missing-project")),
		jsonString(t, validPath),
		jsonString(t, filepath.Join(root, "missing-project")),
	)
	if err := os.WriteFile(indexPath, []byte(index), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	mainPath := filepath.Join(projectDir, "sess-main.jsonl")
	mainContent := fmt.Sprintf(
		`{"type":"user","message":{"role":"user","content":"Hello"},"timestamp":"2026-01-01T00:00:00Z","cwd":%s}
{"type":"assistant","message":{"role":"assistant","content":"Hi"},"timestamp":"2026-01-01T00:01:00Z","cwd":%s}`,
		jsonString(t, validPath),
		jsonString(t, validPath),
	)
	if err := os.WriteFile(mainPath, []byte(mainContent), 0o644); err != nil {
		t.Fatalf("write main session: %v", err)
	}

	scanPath := filepath.Join(projectDir, "sess-scan.jsonl")
	scanContent := fmt.Sprintf(
		`{"type":"user","message":{"role":"user","content":"Scanned"},"timestamp":"2026-01-02T00:00:00Z","cwd":%s}`,
		jsonString(t, validPath),
	)
	if err := os.WriteFile(scanPath, []byte(scanContent), 0o644); err != nil {
		t.Fatalf("write scan session: %v", err)
	}

	agentPath := filepath.Join(projectDir, "agent-abc.jsonl")
	agentContent := fmt.Sprintf(
		`{"type":"user","message":{"role":"user","content":"Sub task"},"timestamp":"2026-01-03T00:00:00Z","cwd":%s,"sessionId":"sess-main","isSidechain":true}`,
		jsonString(t, validPath),
	)
	if err := os.WriteFile(agentPath, []byte(agentContent), 0o644); err != nil {
		t.Fatalf("write agent session: %v", err)
	}

	snapshotPath := filepath.Join(projectDir, "sess-snapshot.jsonl")
	snapshotContent := `{"type":"file-history-snapshot","messageId":"snap-1","snapshot":{"messageId":"snap-1","trackedFileBackups":{},"timestamp":"2026-01-04T00:00:00Z"},"isSnapshotUpdate":false}`
	if err := os.WriteFile(snapshotPath, []byte(snapshotContent), 0o644); err != nil {
		t.Fatalf("write snapshot session: %v", err)
	}

	project2Dir := filepath.Join(root, "projects", "proj-second")
	if err := os.MkdirAll(project2Dir, 0o755); err != nil {
		t.Fatalf("mkdir second project: %v", err)
	}
	secondPath := filepath.Join(project2Dir, "sess-2.jsonl")
	secondContent := fmt.Sprintf(
		`{"type":"user","message":{"role":"user","content":"Second project"},"timestamp":"2026-01-05T00:00:00Z","cwd":%s}`,
		jsonString(t, validPath),
	)
	if err := os.WriteFile(secondPath, []byte(secondContent), 0o644); err != nil {
		t.Fatalf("write second session: %v", err)
	}
	secondIndex := fmt.Sprintf(
		`{"version":1,"entries":[{"sessionId":"legacy-2","fullPath":%s,"summary":"Second summary from index","projectPath":%s}],"originalPath":%s}`,
		jsonString(t, secondPath),
		jsonString(t, validPath),
		jsonString(t, validPath),
	)
	if err := os.WriteFile(filepath.Join(project2Dir, "sessions-index.json"), []byte(secondIndex), 0o644); err != nil {
		t.Fatalf("write second index: %v", err)
	}

	return root
}

func writeIndexedExternalFullPathFixture(t *testing.T) (string, string, string) {
	t.Helper()

	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	projectDir := filepath.Join(root, "projects", "proj-external")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}

	externalDir := filepath.Join(root, "external-history")
	if err := os.MkdirAll(externalDir, 0o755); err != nil {
		t.Fatalf("mkdir external dir: %v", err)
	}
	externalPath := filepath.Join(externalDir, "sess-external.jsonl")
	externalContent := fmt.Sprintf(
		`{"type":"user","message":{"role":"user","content":"External prompt"},"timestamp":"2026-01-07T00:00:00Z","cwd":%s}
{"type":"assistant","message":{"role":"assistant","content":"Reply"},"timestamp":"2026-01-07T00:01:00Z","cwd":%s}`,
		jsonString(t, workspace),
		jsonString(t, workspace),
	)
	if err := os.WriteFile(externalPath, []byte(externalContent), 0o644); err != nil {
		t.Fatalf("write external session: %v", err)
	}

	indexContent := fmt.Sprintf(
		`{"version":1,"entries":[{"sessionId":"legacy-external","summary":"From index","messageCount":0,"projectPath":%s,"fullPath":%s}],"originalPath":%s}`,
		jsonString(t, filepath.Join(root, "missing-project")),
		jsonString(t, externalPath),
		jsonString(t, filepath.Join(root, "missing-project")),
	)
	if err := os.WriteFile(filepath.Join(projectDir, "sessions-index.json"), []byte(indexContent), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	return root, externalPath, workspace
}

func writeFilteredIndexedExternalFullPathFixture(t *testing.T) (string, string, string) {
	t.Helper()

	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	projectDir := filepath.Join(root, "projects", "proj-external-filtered")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}

	externalDir := filepath.Join(root, "external-history")
	if err := os.MkdirAll(externalDir, 0o755); err != nil {
		t.Fatalf("mkdir external dir: %v", err)
	}
	externalPath := filepath.Join(externalDir, "sess-filtered-external.jsonl")
	snapshotContent := `{"type":"file-history-snapshot","messageId":"snap-1","snapshot":{"messageId":"snap-1","trackedFileBackups":{},"timestamp":"2026-01-09T00:00:00Z"},"isSnapshotUpdate":false}`
	if err := os.WriteFile(externalPath, []byte(snapshotContent), 0o644); err != nil {
		t.Fatalf("write external snapshot session: %v", err)
	}

	indexContent := fmt.Sprintf(
		`{"version":1,"entries":[{"sessionId":"legacy-filtered-external","summary":"From index","messageCount":3,"projectPath":%s,"fullPath":%s}],"originalPath":%s}`,
		jsonString(t, workspace),
		jsonString(t, externalPath),
		jsonString(t, workspace),
	)
	if err := os.WriteFile(filepath.Join(projectDir, "sessions-index.json"), []byte(indexContent), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	return root, externalPath, workspace
}

func TestReadPersistentProjectCacheFileIgnoresOlderSchemaVersion(t *testing.T) {
	useTempClaudeHistoryCacheDir(t)

	dir := "/tmp/project"
	cachePath, err := projectPersistentCachePathForDir(dir)
	if err != nil {
		t.Fatalf("projectPersistentCachePathForDir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}

	payload, err := json.Marshal(map[string]any{
		"version": 1,
		"dir":     dir,
		"entry": map[string]any{
			"manifest": map[string]any{},
			"project": map[string]any{
				"key":  "proj",
				"path": dir,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := os.WriteFile(cachePath, append(payload, '\n'), 0o600); err != nil {
		t.Fatalf("write cache file: %v", err)
	}

	resetProjectPersistentCacheForTest()
	entry, ok := readPersistentProjectCacheEntry(cachePath, dir)
	if ok {
		t.Fatalf("expected old schema cache file to be ignored, got %#v", entry)
	}
}

func assertDiscoverProjectsResultEqual(t *testing.T, label string, wantProjects []Project, wantErr error, gotProjects []Project, gotErr error) {
	t.Helper()

	if (wantErr == nil) != (gotErr == nil) {
		t.Fatalf("%s: unexpected error mismatch\nwant=%v\ngot=%v", label, wantErr, gotErr)
	}
	if wantErr != nil && gotErr != nil && wantErr.Error() != gotErr.Error() {
		t.Fatalf("%s: unexpected error mismatch\nwant=%v\ngot=%v", label, wantErr, gotErr)
	}
	if !reflect.DeepEqual(wantProjects, gotProjects) {
		t.Fatalf("%s: unexpected projects mismatch\nwant=%#v\ngot=%#v", label, wantProjects, gotProjects)
	}
}
