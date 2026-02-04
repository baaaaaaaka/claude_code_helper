package claudehistory

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type sessionFileCacheEntry struct {
	mtime        time.Time
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

func getSessionFileCacheEntry(filePath string) (sessionFileCacheEntry, bool, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		sessionFileCache.mu.Lock()
		delete(sessionFileCache.entries, filePath)
		sessionFileCache.mu.Unlock()
		return sessionFileCacheEntry{}, false, err
	}
	mtime := info.ModTime()
	sessionFileCache.mu.Lock()
	entry, ok := sessionFileCache.entries[filePath]
	sessionFileCache.mu.Unlock()
	if ok && entry.mtime.Equal(mtime) {
		return entry, true, nil
	}
	return sessionFileCacheEntry{mtime: mtime}, false, nil
}

func setSessionFileCacheEntry(filePath string, entry sessionFileCacheEntry) {
	sessionFileCache.mu.Lock()
	sessionFileCache.entries[filePath] = entry
	sessionFileCache.mu.Unlock()
}

func readSessionFileMetaCached(filePath string) (sessionFileMeta, error) {
	entry, ok, err := getSessionFileCacheEntry(filePath)
	if err != nil {
		return sessionFileMeta{}, err
	}
	if ok && entry.hasMeta {
		return entry.meta, nil
	}
	meta, err := readSessionFileMeta(filePath)
	if err != nil {
		return meta, err
	}
	entry.meta = meta
	entry.hasMeta = true
	setSessionFileCacheEntry(filePath, entry)
	sessionFileCacheStats.mu.Lock()
	sessionFileCacheStats.metaReads++
	sessionFileCacheStats.mu.Unlock()
	return meta, nil
}

func readSessionFileSessionIDCached(filePath string) (string, error) {
	entry, ok, err := getSessionFileCacheEntry(filePath)
	if err != nil {
		return "", err
	}
	if ok && entry.hasSessionID {
		return entry.sessionID, nil
	}
	sessionID, err := readSessionFileSessionID(filePath)
	if err != nil {
		return "", err
	}
	entry.sessionID = sessionID
	entry.hasSessionID = true
	setSessionFileCacheEntry(filePath, entry)
	sessionFileCacheStats.mu.Lock()
	sessionFileCacheStats.sessionIDReads++
	sessionFileCacheStats.mu.Unlock()
	return sessionID, nil
}

func resolveSessionIDFromFileCached(filePath string) (string, error) {
	sessionID, err := readSessionFileSessionIDCached(filePath)
	if err != nil {
		return "", err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID != "" {
		return sessionID, nil
	}
	name := filepath.Base(filePath)
	if strings.HasSuffix(name, ".jsonl") {
		name = strings.TrimSuffix(name, ".jsonl")
	}
	return strings.TrimSpace(name), nil
}
