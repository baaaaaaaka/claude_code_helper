package claudehistory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gofrs/flock"
)

const (
	sessionMetaPersistentCacheVersion  = 2
	historyIndexPersistentCacheVersion = 1
	sessionMetaPersistentShardCount    = 64
)

var claudeHistoryUserCacheDirFn = os.UserCacheDir

type sessionMetaPersistentBatchKey struct{}

type sessionMetaPersistentBatch struct {
	mu      sync.Mutex
	writes  map[string]persistentSessionMetaCacheEntry
	deletes map[string]struct{}
}

type persistentSessionMetaCacheEntry struct {
	Key  fileCacheKey    `json:"key"`
	Meta sessionFileMeta `json:"meta"`
}

type persistentSessionMetaCacheFile struct {
	Version int                                        `json:"version"`
	Entries map[string]persistentSessionMetaCacheEntry `json:"entries"`
}

type persistentSessionMetaCacheShardState struct {
	loaded  bool
	path    string
	entries map[string]persistentSessionMetaCacheEntry
}

type persistentSessionMetaCacheState struct {
	mu     sync.Mutex
	shards map[uint8]*persistentSessionMetaCacheShardState
}

type persistentHistoryIndexCacheEntry struct {
	Key      fileCacheKey                  `json:"key"`
	Sessions map[string]historySessionInfo `json:"sessions"`
}

type persistentHistoryIndexCacheFile struct {
	Version int                                         `json:"version"`
	Entries map[string]persistentHistoryIndexCacheEntry `json:"entries"`
}

type persistentHistoryIndexCacheState struct {
	mu      sync.Mutex
	loaded  bool
	path    string
	entries map[string]persistentHistoryIndexCacheEntry
}

type sessionMetaShardUpdates struct {
	writes  map[string]persistentSessionMetaCacheEntry
	deletes map[string]struct{}
}

var persistentSessionMetaCache persistentSessionMetaCacheState
var persistentHistoryIndexCache persistentHistoryIndexCacheState

func ensureSessionMetaPersistentBatch(ctx context.Context) (context.Context, bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Value(sessionMetaPersistentBatchKey{}) != nil {
		return ctx, false
	}
	return context.WithValue(ctx, sessionMetaPersistentBatchKey{}, &sessionMetaPersistentBatch{
		writes:  map[string]persistentSessionMetaCacheEntry{},
		deletes: map[string]struct{}{},
	}), true
}

func withSessionMetaPersistentBatch(ctx context.Context) context.Context {
	ctx, _ = ensureSessionMetaPersistentBatch(ctx)
	return ctx
}

func flushSessionMetaPersistentBatchContext(ctx context.Context) {
	batch := sessionMetaPersistentBatchFromContext(ctx)
	if batch == nil || ctx == nil {
		return
	}
	if err := ctx.Err(); err != nil {
		return
	}
	writes, deletes := batch.drain()
	_ = writePersistentSessionMetaUpdates(writes, deletes)
}

func (b *sessionMetaPersistentBatch) drain() (map[string]persistentSessionMetaCacheEntry, map[string]struct{}) {
	if b == nil {
		return nil, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.writes) == 0 && len(b.deletes) == 0 {
		return nil, nil
	}
	writes := clonePersistentSessionMetaEntries(b.writes)
	deletes := cloneStringSet(b.deletes)
	b.writes = map[string]persistentSessionMetaCacheEntry{}
	b.deletes = map[string]struct{}{}
	return writes, deletes
}

func stagePersistentSessionMetaWrite(ctx context.Context, filePath string, key fileCacheKey, meta sessionFileMeta) {
	if !key.usableForPersistentCache() {
		return
	}
	batch := sessionMetaPersistentBatchFromContext(ctx)
	if batch == nil {
		return
	}
	batch.mu.Lock()
	defer batch.mu.Unlock()
	delete(batch.deletes, filePath)
	batch.writes[filePath] = persistentSessionMetaCacheEntry{
		Key:  key,
		Meta: cloneSessionFileMeta(meta),
	}
}

func stagePersistentSessionMetaDelete(ctx context.Context, filePath string) {
	batch := sessionMetaPersistentBatchFromContext(ctx)
	if batch == nil {
		return
	}
	batch.mu.Lock()
	defer batch.mu.Unlock()
	delete(batch.writes, filePath)
	batch.deletes[filePath] = struct{}{}
}

func sessionMetaPersistentBatchFromContext(ctx context.Context) *sessionMetaPersistentBatch {
	if ctx == nil {
		return nil
	}
	batch, _ := ctx.Value(sessionMetaPersistentBatchKey{}).(*sessionMetaPersistentBatch)
	return batch
}

func lookupPersistentSessionMeta(filePath string, key fileCacheKey) (sessionFileMeta, bool) {
	if !key.usableForPersistentCache() {
		return sessionFileMeta{}, false
	}

	shard := sessionMetaPersistentShardForPath(filePath)
	persistentSessionMetaCache.mu.Lock()
	defer persistentSessionMetaCache.mu.Unlock()

	entries, ok := persistentSessionMetaCache.loadShardLocked(shard)
	if !ok {
		return sessionFileMeta{}, false
	}
	entry, ok := entries[filePath]
	if !ok || entry.Key != key {
		return sessionFileMeta{}, false
	}
	if err := validateFileReadable(filePath); err != nil {
		return sessionFileMeta{}, false
	}
	return cloneSessionFileMeta(entry.Meta), true
}

func lookupPersistentHistoryIndex(path string, key fileCacheKey) (historyIndex, bool) {
	if !key.usableForPersistentCache() {
		return historyIndex{sessions: map[string]*historySessionInfo{}}, false
	}

	persistentHistoryIndexCache.mu.Lock()
	defer persistentHistoryIndexCache.mu.Unlock()

	entries, ok := persistentHistoryIndexCache.loadLocked()
	if !ok {
		return historyIndex{sessions: map[string]*historySessionInfo{}}, false
	}
	entry, ok := entries[path]
	if !ok || entry.Key != key {
		return historyIndex{sessions: map[string]*historySessionInfo{}}, false
	}
	if err := validateFileReadable(path); err != nil {
		return historyIndex{sessions: map[string]*historySessionInfo{}}, false
	}
	return historyIndexFromPersistent(entry.Sessions), true
}

func storePersistentHistoryIndex(path string, key fileCacheKey, idx historyIndex) {
	if !key.usableForPersistentCache() {
		return
	}
	_ = writePersistentHistoryIndexUpdates(map[string]persistentHistoryIndexCacheEntry{
		path: {
			Key:      key,
			Sessions: historyIndexToPersistent(idx),
		},
	}, nil)
}

func deletePersistentHistoryIndex(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	_ = writePersistentHistoryIndexUpdates(nil, map[string]struct{}{path: {}})
}

func writePersistentSessionMetaUpdates(writes map[string]persistentSessionMetaCacheEntry, deletes map[string]struct{}) error {
	if len(writes) == 0 && len(deletes) == 0 {
		return nil
	}
	updates := groupSessionMetaUpdatesByShard(writes, deletes)
	for shard, update := range updates {
		path, err := sessionMetaPersistentCachePathForShard(shard)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}

		lock := flock.New(path + ".lock")
		locked, err := lock.TryLock()
		if err != nil || !locked {
			return err
		}

		entries := readPersistentSessionMetaCacheFile(path)
		for filePath := range update.deletes {
			delete(entries, filePath)
		}
		for filePath, entry := range update.writes {
			entries[filePath] = persistentSessionMetaCacheEntry{
				Key:  entry.Key,
				Meta: cloneSessionFileMeta(entry.Meta),
			}
		}

		if len(entries) == 0 {
			_ = os.Remove(path)
		} else {
			payload := persistentSessionMetaCacheFile{
				Version: sessionMetaPersistentCacheVersion,
				Entries: entries,
			}
			data, err := json.Marshal(payload)
			if err != nil {
				_ = lock.Unlock()
				return err
			}
			data = append(data, '\n')
			if err := atomicWriteFile(path, data, 0o600); err != nil {
				_ = lock.Unlock()
				return err
			}
		}

		persistentSessionMetaCache.mu.Lock()
		persistentSessionMetaCache.setShardLocked(shard, path, entries)
		persistentSessionMetaCache.mu.Unlock()

		_ = lock.Unlock()
	}
	return nil
}

func groupSessionMetaUpdatesByShard(writes map[string]persistentSessionMetaCacheEntry, deletes map[string]struct{}) map[uint8]*sessionMetaShardUpdates {
	grouped := map[uint8]*sessionMetaShardUpdates{}
	ensure := func(shard uint8) *sessionMetaShardUpdates {
		update := grouped[shard]
		if update != nil {
			return update
		}
		update = &sessionMetaShardUpdates{
			writes:  map[string]persistentSessionMetaCacheEntry{},
			deletes: map[string]struct{}{},
		}
		grouped[shard] = update
		return update
	}
	for filePath, entry := range writes {
		update := ensure(sessionMetaPersistentShardForPath(filePath))
		update.writes[filePath] = persistentSessionMetaCacheEntry{
			Key:  entry.Key,
			Meta: cloneSessionFileMeta(entry.Meta),
		}
	}
	for filePath := range deletes {
		update := ensure(sessionMetaPersistentShardForPath(filePath))
		update.deletes[filePath] = struct{}{}
	}
	return grouped
}

func writePersistentHistoryIndexUpdates(writes map[string]persistentHistoryIndexCacheEntry, deletes map[string]struct{}) error {
	if len(writes) == 0 && len(deletes) == 0 {
		return nil
	}
	path, err := historyIndexPersistentCachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	lock := flock.New(path + ".lock")
	locked, err := lock.TryLock()
	if err != nil || !locked {
		return err
	}
	defer func() { _ = lock.Unlock() }()

	entries := readPersistentHistoryIndexCacheFile(path)
	for filePath := range deletes {
		delete(entries, filePath)
	}
	for filePath, entry := range writes {
		entries[filePath] = persistentHistoryIndexCacheEntry{
			Key:      entry.Key,
			Sessions: cloneHistorySessionInfoMap(entry.Sessions),
		}
	}

	payload := persistentHistoryIndexCacheFile{
		Version: historyIndexPersistentCacheVersion,
		Entries: entries,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := atomicWriteFile(path, data, 0o600); err != nil {
		return err
	}

	persistentHistoryIndexCache.mu.Lock()
	persistentHistoryIndexCache.loaded = true
	persistentHistoryIndexCache.path = path
	persistentHistoryIndexCache.entries = clonePersistentHistoryIndexEntries(entries)
	persistentHistoryIndexCache.mu.Unlock()
	return nil
}

func (s *persistentSessionMetaCacheState) loadShardLocked(shard uint8) (map[string]persistentSessionMetaCacheEntry, bool) {
	if s.shards == nil {
		s.shards = map[uint8]*persistentSessionMetaCacheShardState{}
	}
	state := s.shards[shard]
	if state != nil && state.loaded {
		return state.entries, state.path != ""
	}
	path, err := sessionMetaPersistentCachePathForShard(shard)
	if err != nil {
		s.shards[shard] = &persistentSessionMetaCacheShardState{
			loaded:  true,
			path:    "",
			entries: map[string]persistentSessionMetaCacheEntry{},
		}
		return s.shards[shard].entries, false
	}
	state = &persistentSessionMetaCacheShardState{
		loaded:  true,
		path:    path,
		entries: readPersistentSessionMetaCacheFile(path),
	}
	s.shards[shard] = state
	return state.entries, true
}

func (s *persistentSessionMetaCacheState) setShardLocked(shard uint8, path string, entries map[string]persistentSessionMetaCacheEntry) {
	if s.shards == nil {
		s.shards = map[uint8]*persistentSessionMetaCacheShardState{}
	}
	s.shards[shard] = &persistentSessionMetaCacheShardState{
		loaded:  true,
		path:    path,
		entries: clonePersistentSessionMetaEntries(entries),
	}
}

func (s *persistentHistoryIndexCacheState) loadLocked() (map[string]persistentHistoryIndexCacheEntry, bool) {
	if s.loaded {
		return s.entries, s.path != ""
	}
	path, err := historyIndexPersistentCachePath()
	if err != nil {
		s.loaded = true
		s.path = ""
		s.entries = map[string]persistentHistoryIndexCacheEntry{}
		return s.entries, false
	}
	s.loaded = true
	s.path = path
	s.entries = readPersistentHistoryIndexCacheFile(path)
	return s.entries, true
}

func readPersistentSessionMetaCacheFile(path string) map[string]persistentSessionMetaCacheEntry {
	entries := map[string]persistentSessionMetaCacheEntry{}
	data, err := os.ReadFile(path)
	if err != nil {
		return entries
	}
	var payload persistentSessionMetaCacheFile
	if err := json.Unmarshal(data, &payload); err != nil || payload.Version != sessionMetaPersistentCacheVersion {
		return entries
	}
	return clonePersistentSessionMetaEntries(payload.Entries)
}

func readPersistentHistoryIndexCacheFile(path string) map[string]persistentHistoryIndexCacheEntry {
	entries := map[string]persistentHistoryIndexCacheEntry{}
	data, err := os.ReadFile(path)
	if err != nil {
		return entries
	}
	var payload persistentHistoryIndexCacheFile
	if err := json.Unmarshal(data, &payload); err != nil || payload.Version != historyIndexPersistentCacheVersion {
		return entries
	}
	return clonePersistentHistoryIndexEntries(payload.Entries)
}

func cloneSessionFileMeta(meta sessionFileMeta) sessionFileMeta {
	meta.SessionIDs = append([]string(nil), meta.SessionIDs...)
	return meta
}

func clonePersistentSessionMetaEntries(entries map[string]persistentSessionMetaCacheEntry) map[string]persistentSessionMetaCacheEntry {
	if len(entries) == 0 {
		return map[string]persistentSessionMetaCacheEntry{}
	}
	cloned := make(map[string]persistentSessionMetaCacheEntry, len(entries))
	for path, entry := range entries {
		cloned[path] = persistentSessionMetaCacheEntry{
			Key:  entry.Key,
			Meta: cloneSessionFileMeta(entry.Meta),
		}
	}
	return cloned
}

func cloneStringSet(values map[string]struct{}) map[string]struct{} {
	if len(values) == 0 {
		return map[string]struct{}{}
	}
	cloned := make(map[string]struct{}, len(values))
	for value := range values {
		cloned[value] = struct{}{}
	}
	return cloned
}

func cloneHistorySessionInfoMap(entries map[string]historySessionInfo) map[string]historySessionInfo {
	if len(entries) == 0 {
		return map[string]historySessionInfo{}
	}
	cloned := make(map[string]historySessionInfo, len(entries))
	for sessionID, info := range entries {
		cloned[sessionID] = info
	}
	return cloned
}

func clonePersistentHistoryIndexEntries(entries map[string]persistentHistoryIndexCacheEntry) map[string]persistentHistoryIndexCacheEntry {
	if len(entries) == 0 {
		return map[string]persistentHistoryIndexCacheEntry{}
	}
	cloned := make(map[string]persistentHistoryIndexCacheEntry, len(entries))
	for path, entry := range entries {
		cloned[path] = persistentHistoryIndexCacheEntry{
			Key:      entry.Key,
			Sessions: cloneHistorySessionInfoMap(entry.Sessions),
		}
	}
	return cloned
}

func historyIndexToPersistent(idx historyIndex) map[string]historySessionInfo {
	if len(idx.sessions) == 0 {
		return map[string]historySessionInfo{}
	}
	out := make(map[string]historySessionInfo, len(idx.sessions))
	for sessionID, info := range idx.sessions {
		if info == nil {
			continue
		}
		out[sessionID] = *info
	}
	return out
}

func historyIndexFromPersistent(entries map[string]historySessionInfo) historyIndex {
	idx := historyIndex{sessions: map[string]*historySessionInfo{}}
	for sessionID, info := range entries {
		infoCopy := info
		idx.sessions[sessionID] = &infoCopy
	}
	return idx
}

func validateFileReadable(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return f.Close()
}

func claudeHistoryCacheDir() (string, error) {
	base, err := claudeHistoryUserCacheDirFn()
	if err != nil {
		return "", err
	}
	base = strings.TrimSpace(base)
	if base == "" {
		return "", errors.New("empty user cache dir")
	}
	return filepath.Join(base, "claude-proxy", "claudehistory"), nil
}

func sessionMetaPersistentCacheDir() (string, error) {
	dir, err := claudeHistoryCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "session_meta_cache"), nil
}

func sessionMetaPersistentShardForPath(filePath string) uint8 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(filepath.Clean(filePath)))
	return uint8(h.Sum32() % sessionMetaPersistentShardCount)
}

func sessionMetaPersistentCachePathForFile(filePath string) (string, error) {
	return sessionMetaPersistentCachePathForShard(sessionMetaPersistentShardForPath(filePath))
}

func sessionMetaPersistentCachePathForShard(shard uint8) (string, error) {
	dir, err := sessionMetaPersistentCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("%02x.json", shard)), nil
}

func historyIndexPersistentCachePath() (string, error) {
	dir, err := claudeHistoryCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "history_index_cache.json"), nil
}

func resetSessionPersistentCacheForTest() {
	persistentSessionMetaCache.mu.Lock()
	persistentSessionMetaCache.shards = nil
	persistentSessionMetaCache.mu.Unlock()
}

func resetHistoryIndexPersistentCacheForTest() {
	persistentHistoryIndexCache.mu.Lock()
	persistentHistoryIndexCache.loaded = false
	persistentHistoryIndexCache.path = ""
	persistentHistoryIndexCache.entries = nil
	persistentHistoryIndexCache.mu.Unlock()
}
