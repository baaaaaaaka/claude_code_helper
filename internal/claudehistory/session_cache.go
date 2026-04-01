package claudehistory

import (
	"context"
	"os"
	"sync"
)

type sessionFileCacheEntry struct {
	key          fileCacheKey
	sessionID    string
	hasSessionID bool
	meta         sessionFileMeta
	hasMeta      bool
}

var sessionFileCache = struct {
	mu      sync.Mutex
	entries map[string]sessionFileCacheEntry
}{
	entries: map[string]sessionFileCacheEntry{},
}

var sessionFileCacheStats = struct {
	mu             sync.Mutex
	metaReads      int
	sessionIDReads int
}{}

func sessionFileCacheStatsSnapshot() (int, int) {
	sessionFileCacheStats.mu.Lock()
	defer sessionFileCacheStats.mu.Unlock()
	return sessionFileCacheStats.metaReads, sessionFileCacheStats.sessionIDReads
}

func resetSessionFileCache() {
	sessionFileCache.mu.Lock()
	sessionFileCache.entries = map[string]sessionFileCacheEntry{}
	sessionFileCache.mu.Unlock()
	sessionFileCacheStats.mu.Lock()
	sessionFileCacheStats.metaReads = 0
	sessionFileCacheStats.sessionIDReads = 0
	sessionFileCacheStats.mu.Unlock()
}

func getSessionFileCacheEntry(filePath string) (sessionFileCacheEntry, bool, fileCacheKey, error) {
	key, err := currentFileCacheKey(filePath)
	if err != nil {
		sessionFileCache.mu.Lock()
		delete(sessionFileCache.entries, filePath)
		sessionFileCache.mu.Unlock()
		return sessionFileCacheEntry{}, false, fileCacheKey{}, err
	}
	if !key.usableForPersistentCache() {
		return sessionFileCacheEntry{key: key}, false, key, nil
	}
	sessionFileCache.mu.Lock()
	entry, ok := sessionFileCache.entries[filePath]
	sessionFileCache.mu.Unlock()
	if ok && entry.key == key {
		return entry, true, key, nil
	}
	return sessionFileCacheEntry{key: key}, false, key, nil
}

func getSessionFileCacheEntryWithKey(filePath string, key fileCacheKey) (sessionFileCacheEntry, bool) {
	if !key.usableForPersistentCache() {
		return sessionFileCacheEntry{key: key}, false
	}
	sessionFileCache.mu.Lock()
	entry, ok := sessionFileCache.entries[filePath]
	sessionFileCache.mu.Unlock()
	if ok && entry.key == key {
		return entry, true
	}
	return sessionFileCacheEntry{key: key}, false
}

func setSessionFileCacheEntry(filePath string, entry sessionFileCacheEntry) {
	sessionFileCache.mu.Lock()
	sessionFileCache.entries[filePath] = entry
	sessionFileCache.mu.Unlock()
}

func readSessionFileMetaCached(filePath string) (sessionFileMeta, error) {
	return readSessionFileMetaCachedContext(context.Background(), filePath)
}

func readSessionFileMetaCachedContext(ctx context.Context, filePath string) (sessionFileMeta, error) {
	entry, ok, key, err := getSessionFileCacheEntry(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			stagePersistentSessionMetaDelete(ctx, filePath)
		}
		return sessionFileMeta{}, err
	}
	return readSessionFileMetaCachedFromState(ctx, filePath, entry, ok, key)
}

func readSessionFileMetaCachedWithKeyContext(ctx context.Context, filePath string, key fileCacheKey) (sessionFileMeta, error) {
	entry, ok := getSessionFileCacheEntryWithKey(filePath, key)
	return readSessionFileMetaCachedFromState(ctx, filePath, entry, ok, key)
}

func readSessionFileMetaCachedFromState(ctx context.Context, filePath string, entry sessionFileCacheEntry, ok bool, key fileCacheKey) (sessionFileMeta, error) {
	if ok && entry.hasMeta {
		return cloneSessionFileMeta(entry.meta), nil
	}
	if meta, ok := lookupPersistentSessionMeta(filePath, key); ok {
		entry.key = key
		entry.meta = cloneSessionFileMeta(meta)
		entry.hasMeta = true
		setSessionFileCacheEntry(filePath, entry)
		return cloneSessionFileMeta(meta), nil
	}
	meta, err := readSessionFileMeta(filePath)
	if err != nil {
		return meta, err
	}
	stableKey, stable := verifyStableFileCacheKey(filePath, key)
	if stable && stableKey.usableForPersistentCache() {
		entry.key = stableKey
		entry.meta = cloneSessionFileMeta(meta)
		entry.hasMeta = true
		setSessionFileCacheEntry(filePath, entry)
		stagePersistentSessionMetaWrite(ctx, filePath, stableKey, meta)
	}
	sessionFileCacheStats.mu.Lock()
	sessionFileCacheStats.metaReads++
	sessionFileCacheStats.mu.Unlock()
	return cloneSessionFileMeta(meta), nil
}

func readSessionFileSessionIDCached(filePath string) (string, error) {
	entry, ok, key, err := getSessionFileCacheEntry(filePath)
	if err != nil {
		return "", err
	}
	return readSessionFileSessionIDCachedFromState(filePath, entry, ok, key)
}

func readSessionFileSessionIDCachedWithKey(filePath string, key fileCacheKey) (string, error) {
	entry, ok := getSessionFileCacheEntryWithKey(filePath, key)
	return readSessionFileSessionIDCachedFromState(filePath, entry, ok, key)
}

func readSessionFileSessionIDCachedFromState(filePath string, entry sessionFileCacheEntry, ok bool, key fileCacheKey) (string, error) {
	if ok && entry.hasSessionID {
		return entry.sessionID, nil
	}
	sessionID, err := readSessionFileSessionID(filePath)
	if err != nil {
		return "", err
	}
	stableKey, stable := verifyStableFileCacheKey(filePath, key)
	if stable && stableKey.usableForPersistentCache() {
		entry.key = stableKey
		entry.sessionID = sessionID
		entry.hasSessionID = true
		setSessionFileCacheEntry(filePath, entry)
	}
	sessionFileCacheStats.mu.Lock()
	sessionFileCacheStats.sessionIDReads++
	sessionFileCacheStats.mu.Unlock()
	return sessionID, nil
}

func resolveSessionIDFromFileCached(filePath string) (string, error) {
	return sessionIDFromFilePath(filePath), nil
}
