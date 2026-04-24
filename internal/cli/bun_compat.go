package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
	"github.com/baaaaaaaka/claude_code_helper/internal/diskspace"
)

const (
	bunCompatLaunchEnvCacheVersion = 1
	bunCompatLaunchEnvSetVersion   = 1
)

type bunCompatLaunchEnvCache struct {
	Version int                            `json:"version"`
	Entries []bunCompatLaunchEnvCacheEntry `json:"entries"`
}

type bunCompatLaunchEnvCacheEntry struct {
	ResolvedPath  string    `json:"resolvedPath"`
	SHA256        string    `json:"sha256"`
	GOOS          string    `json:"goos"`
	GOARCH        string    `json:"goarch"`
	EnvSetVersion int       `json:"envSetVersion"`
	LaunchEnv     []string  `json:"launchEnv"`
	CreatedAt     time.Time `json:"createdAt"`
}

func bunCompatLaunchEnv() []string {
	return []string{
		"BUN_FEATURE_FLAG_DISABLE_MEMFD=1",
		"BUN_CONFIG_DISABLE_COPY_FILE_RANGE=1",
		"BUN_CONFIG_DISABLE_ioctl_ficlonerange=1",
		"BUN_FEATURE_FLAG_DISABLE_RWF_NONBLOCK=1",
	}
}

func lookupCachedBunCompatLaunchEnv(path string) ([]string, bool) {
	identity, ok := bunCompatLaunchEnvIdentity(path)
	if !ok {
		return nil, false
	}
	cachePath, ok := bunCompatLaunchEnvCachePath()
	if !ok {
		return nil, false
	}

	var env []string
	lockErr := withFileLock(cachePath+".lock", func() error {
		cache, err := readBunCompatLaunchEnvCache(cachePath)
		if err != nil {
			return err
		}
		for _, entry := range cache.Entries {
			if !entry.matches(identity) {
				continue
			}
			if !sameStringSet(entry.LaunchEnv, bunCompatLaunchEnv()) {
				continue
			}
			env = append([]string{}, entry.LaunchEnv...)
			return nil
		}
		return nil
	})
	if lockErr != nil || len(env) == 0 {
		return nil, false
	}
	return env, true
}

func saveCachedBunCompatLaunchEnv(path string, launchEnv []string) error {
	if !sameStringSet(launchEnv, bunCompatLaunchEnv()) {
		return nil
	}
	identity, ok := bunCompatLaunchEnvIdentity(path)
	if !ok {
		return nil
	}
	cachePath, ok := bunCompatLaunchEnvCachePath()
	if !ok {
		return nil
	}

	return withFileLock(cachePath+".lock", func() error {
		cache, err := readBunCompatLaunchEnvCache(cachePath)
		if err != nil {
			return err
		}
		entry := bunCompatLaunchEnvCacheEntry{
			ResolvedPath:  identity.ResolvedPath,
			SHA256:        identity.SHA256,
			GOOS:          runtime.GOOS,
			GOARCH:        runtime.GOARCH,
			EnvSetVersion: bunCompatLaunchEnvSetVersion,
			LaunchEnv:     append([]string{}, launchEnv...),
			CreatedAt:     time.Now().UTC(),
		}
		replaced := false
		for i := range cache.Entries {
			if config.PathsEqual(cache.Entries[i].ResolvedPath, entry.ResolvedPath) &&
				cache.Entries[i].GOOS == entry.GOOS &&
				cache.Entries[i].GOARCH == entry.GOARCH {
				cache.Entries[i] = entry
				replaced = true
				break
			}
		}
		if !replaced {
			cache.Entries = append(cache.Entries, entry)
		}
		return writeBunCompatLaunchEnvCache(cachePath, cache)
	})
}

func bunCompatLaunchEnvCachePath() (string, bool) {
	hostRoot, _, err := resolveClaudeProxyHostRoot()
	if err != nil || strings.TrimSpace(hostRoot) == "" {
		return "", false
	}
	return filepath.Join(hostRoot, "bun-compat", "launch-env.json"), true
}

type bunCompatLaunchEnvCacheIdentity struct {
	ResolvedPath string
	SHA256       string
}

func bunCompatLaunchEnvIdentity(path string) (bunCompatLaunchEnvCacheIdentity, bool) {
	resolvedPath, err := resolveExecutablePathFn(path)
	if err != nil || strings.TrimSpace(resolvedPath) == "" {
		return bunCompatLaunchEnvCacheIdentity{}, false
	}
	sha, err := hashFileSHA256Fn(resolvedPath)
	if err != nil || strings.TrimSpace(sha) == "" {
		return bunCompatLaunchEnvCacheIdentity{}, false
	}
	return bunCompatLaunchEnvCacheIdentity{
		ResolvedPath: resolvedPath,
		SHA256:       strings.TrimSpace(sha),
	}, true
}

func (entry bunCompatLaunchEnvCacheEntry) matches(identity bunCompatLaunchEnvCacheIdentity) bool {
	return entry.EnvSetVersion == bunCompatLaunchEnvSetVersion &&
		entry.GOOS == runtime.GOOS &&
		entry.GOARCH == runtime.GOARCH &&
		config.PathsEqual(entry.ResolvedPath, identity.ResolvedPath) &&
		strings.EqualFold(strings.TrimSpace(entry.SHA256), strings.TrimSpace(identity.SHA256))
}

func readBunCompatLaunchEnvCache(path string) (bunCompatLaunchEnvCache, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return bunCompatLaunchEnvCache{Version: bunCompatLaunchEnvCacheVersion}, nil
	}
	if err != nil {
		return bunCompatLaunchEnvCache{}, fmt.Errorf("read Bun compat launch env cache: %w", err)
	}
	var cache bunCompatLaunchEnvCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return bunCompatLaunchEnvCache{}, fmt.Errorf("parse Bun compat launch env cache: %w", err)
	}
	if cache.Version == 0 {
		cache.Version = bunCompatLaunchEnvCacheVersion
	}
	if cache.Version != bunCompatLaunchEnvCacheVersion {
		return bunCompatLaunchEnvCache{Version: bunCompatLaunchEnvCacheVersion}, nil
	}
	return cache, nil
}

func writeBunCompatLaunchEnvCache(path string, cache bunCompatLaunchEnvCache) error {
	cache.Version = bunCompatLaunchEnvCacheVersion
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Bun compat launch env cache: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create Bun compat launch env cache dir: %w", err)
	}
	data = append(data, '\n')
	if err := diskspace.EnsureAvailable(path, uint64(len(data))); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return diskspace.AnnotateWriteError(path, err)
	}
	return nil
}

func sameStringSet(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, v := range a {
		seen[v]++
	}
	for _, v := range b {
		if seen[v] == 0 {
			return false
		}
		seen[v]--
	}
	return true
}
