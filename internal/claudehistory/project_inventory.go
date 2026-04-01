package claudehistory

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type projectInventory struct {
	dir                     string
	recursive               bool
	cacheEligible           bool
	indexPath               string
	indexExists             bool
	indexIsReadable         bool
	indexKey                fileCacheKey
	sessionFiles            []string
	agentFiles              []string
	fileKeys                map[string]fileCacheKey
	fileReadableState       map[string]bool
	sessionPathByID         map[string]string
	sessionMetaProjectPaths map[string]struct{}
}

func buildProjectInventoryContext(ctx context.Context, dir string, recursive bool) (projectInventory, error) {
	inv := projectInventory{
		dir:                     dir,
		recursive:               recursive,
		cacheEligible:           true,
		indexPath:               filepath.Join(dir, "sessions-index.json"),
		fileKeys:                map[string]fileCacheKey{},
		fileReadableState:       map[string]bool{},
		sessionPathByID:         map[string]string{},
		sessionMetaProjectPaths: map[string]struct{}{},
	}
	if err := ctx.Err(); err != nil {
		return inv, err
	}

	if key, err := currentFileCacheKey(inv.indexPath); err == nil {
		inv.indexExists = true
		inv.indexKey = key
		inv.indexIsReadable = validateFileReadable(inv.indexPath) == nil
		if !key.usableForPersistentCache() || !inv.indexIsReadable {
			inv.cacheEligible = false
		}
	}

	if !recursive {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return inv, err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return inv, err
			}
			if entry.IsDir() {
				continue
			}
			if err := inv.addFile(filepath.Join(dir, entry.Name())); err != nil {
				return inv, err
			}
		}
		inv.sort()
		return inv, nil
	}

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		return inv.addFile(path)
	})
	if err != nil {
		return inv, err
	}
	inv.sort()
	return inv, nil
}

func (inv *projectInventory) addFile(path string) error {
	name := filepath.Base(path)
	if name == "sessions-index.json" || !strings.HasSuffix(name, ".jsonl") {
		return nil
	}

	key, err := currentFileCacheKey(path)
	if err == nil {
		inv.fileKeys[path] = key
		inv.fileReadableState[path] = validateFileReadable(path) == nil
		if !key.usableForPersistentCache() || !inv.fileReadableState[path] {
			inv.cacheEligible = false
		}
	} else {
		inv.fileReadableState[path] = false
		inv.cacheEligible = false
	}
	if isAgentSessionFileName(name) {
		inv.agentFiles = append(inv.agentFiles, path)
		return nil
	}

	inv.sessionFiles = append(inv.sessionFiles, path)
	sessionID := sessionIDFromFilePath(path)
	if _, ok := inv.sessionPathByID[sessionID]; !ok && sessionID != "" {
		inv.sessionPathByID[sessionID] = path
	}
	return nil
}

func (inv *projectInventory) sort() {
	sort.Strings(inv.sessionFiles)
	sort.Strings(inv.agentFiles)
}

func (inv projectInventory) fileKey(path string) (fileCacheKey, bool) {
	key, ok := inv.fileKeys[path]
	return key, ok
}

func (inv projectInventory) sessionPath(sessionID string) string {
	return inv.sessionPathByID[strings.TrimSpace(sessionID)]
}

func (inv projectInventory) fileReadable(path string) bool {
	readable, ok := inv.fileReadableState[path]
	return ok && readable
}

func (inv projectInventory) indexReadable() bool {
	if !inv.indexExists {
		return true
	}
	return inv.indexIsReadable
}

func (inv *projectInventory) rememberSessionMetaProjectPath(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	inv.sessionMetaProjectPaths[path] = struct{}{}
}

func (inv projectInventory) projectCacheEligible(history historyDependency) bool {
	if !inv.cacheEligible {
		return false
	}
	if history.Exists && !history.Key.usableForPersistentCache() {
		return false
	}
	if inv.indexExists && !inv.indexKey.usableForPersistentCache() {
		return false
	}
	if inv.indexExists && !inv.indexReadable() {
		return false
	}
	return true
}
