package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
	"github.com/baaaaaaaka/claude_code_helper/internal/diskspace"
	"github.com/baaaaaaaka/claude_code_helper/internal/proc"
)

const (
	yoloPatchMirrorManifestVersion = 1
	yoloPatchMirrorKeep            = 3
	yoloPatchMirrorCorruptLeaseTTL = 24 * time.Hour
)

type yoloPatchMirrorManifest struct {
	Version       int       `json:"version"`
	SourcePath    string    `json:"sourcePath"`
	SourceSize    int64     `json:"sourceSize"`
	SourceSHA256  string    `json:"sourceSha256"`
	SpecsSHA256   string    `json:"specsSha256"`
	PatchedSHA256 string    `json:"patchedSha256"`
	ProxyVersion  string    `json:"proxyVersion"`
	GOOS          string    `json:"goos"`
	GOARCH        string    `json:"goarch"`
	Executable    string    `json:"executable"`
	CreatedAt     time.Time `json:"createdAt"`
}

type yoloPatchMirrorLease struct {
	PID           int       `json:"pid"`
	StartIdentity string    `json:"startIdentity,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

func patchClaudeExecutableMirror(path string, specs []exePatchSpec, log io.Writer, preview bool, dryRun bool, historyStore *config.PatchHistoryStore, proxyVersion string) (*patchOutcome, error) {
	if log == nil {
		log = io.Discard
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat target executable %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("target executable %q is not a regular file", path)
	}

	specsHash := patchSpecsHash(specs)
	sourceHash, err := hashFileSHA256Fn(path)
	if err != nil {
		return nil, fmt.Errorf("hash target executable %q: %w", path, err)
	}
	outcome := &patchOutcome{
		SourcePath:   path,
		SourceSHA256: sourceHash,
		TargetPath:   path,
		TargetSHA256: sourceHash,
		BackupPath:   path,
		SpecsHash:    specsHash,
		HistoryStore: historyStore,
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read target executable %q: %w", path, err)
	}
	patched, stats, err := applyExePatches(data, specs, log, preview)
	if err != nil {
		return nil, fmt.Errorf("patch target executable %q: %w", path, err)
	}

	changed := false
	touched := false
	for _, stat := range stats {
		if stat.Changed > 0 {
			changed = true
		}
		if stat.Replacements > 0 || stat.Eligible > 0 {
			touched = true
		}
	}
	patchedHash := ""
	if changed {
		patchedHash = hashBytes(patched)
		if patchedHash == sourceHash {
			changed = false
			for i := range stats {
				stats[i].Changed = 0
			}
		}
	}
	if touched && !changed {
		outcome.AlreadyPatched = true
	}
	if dryRun {
		logDryRun(log, path, changed)
		outcome.PatchStats = stats
		return outcome, nil
	}
	if !changed {
		outcome.PatchStats = stats
		return outcome, nil
	}

	mirrorPath, reused, leasePath, err := prepareYoloPatchMirror(path, info, patched, sourceHash, patchedHash, specsHash, proxyVersion, log)
	if err != nil {
		return nil, err
	}
	outcome.TargetPath = mirrorPath
	outcome.TargetSHA256 = patchedHash
	outcome.LaunchArgsPrefix = []string{mirrorPath}
	outcome.MirrorLeasePath = leasePath
	outcome.Applied = !reused
	outcome.AlreadyPatched = reused
	outcome.Verified = runtimeGOOS != "windows"

	if historyStore != nil {
		verifiedAt := time.Time{}
		history, historyLoaded := loadPatchHistory(historyStore, log)
		if historyLoaded {
			if entry, ok := history.Find(mirrorPath, specsHash); ok && entry.PatchedSHA256 == patchedHash {
				verifiedAt = entry.VerifiedAt
			}
		}
		if runtimeGOOS != "windows" && verifiedAt.IsZero() {
			verifiedAt = time.Now()
		}
		outcome.Verified = !verifiedAt.IsZero()
		entry := config.PatchHistoryEntry{
			Path:          mirrorPath,
			SpecsSHA256:   specsHash,
			PatchedSHA256: patchedHash,
			ProxyVersion:  proxyVersion,
			PatchedAt:     time.Now(),
			VerifiedAt:    verifiedAt,
		}
		if err := historyStore.Update(func(h *config.PatchHistory) error {
			h.Upsert(entry)
			return nil
		}); err != nil {
			_, _ = fmt.Fprintf(log, "exe-patch: failed to update patch history: %v\n", err)
		}
	}

	for _, stat := range stats {
		logPatchSummary(log, mirrorPath, stat)
	}
	outcome.PatchStats = stats
	return outcome, nil
}

func prepareYoloPatchMirror(sourcePath string, sourceInfo os.FileInfo, patched []byte, sourceHash string, patchedHash string, specsHash string, proxyVersion string, log io.Writer) (string, bool, string, error) {
	hostRoot, _, err := resolveClaudeProxyHostRoot()
	if err != nil {
		return "", false, "", err
	}
	cacheRoot := filepath.Join(hostRoot, "yolo-patches")
	key := yoloPatchMirrorKey(sourceHash, specsHash, proxyVersion, filepath.Base(sourcePath))
	mirrorDir := filepath.Join(cacheRoot, key)
	mirrorPath := filepath.Join(mirrorDir, filepath.Base(sourcePath))
	lockPath := filepath.Join(cacheRoot, ".lock")
	reused := false
	leasePath := ""

	err = withFileLock(lockPath, func() error {
		if err := os.MkdirAll(mirrorDir, 0o755); err != nil {
			return fmt.Errorf("create yolo patch mirror dir: %w", err)
		}
		manifest := yoloPatchMirrorManifest{
			Version:       yoloPatchMirrorManifestVersion,
			SourcePath:    sourcePath,
			SourceSize:    sourceInfo.Size(),
			SourceSHA256:  sourceHash,
			SpecsSHA256:   specsHash,
			PatchedSHA256: patchedHash,
			ProxyVersion:  proxyVersion,
			GOOS:          runtimeGOOS,
			GOARCH:        runtime.GOARCH,
			Executable:    filepath.Base(sourcePath),
			CreatedAt:     time.Now(),
		}
		if yoloPatchMirrorValid(mirrorPath, manifest) {
			reused = true
			now := time.Now()
			_ = os.Chtimes(mirrorPath, now, now)
		} else {
			if err := writeYoloPatchMirrorExecutable(mirrorPath, patched, sourceInfo.Mode().Perm()); err != nil {
				return err
			}
			if err := writeYoloPatchMirrorJSON(filepath.Join(mirrorDir, "manifest.json"), manifest); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(log, "exe-patch: prepared yolo patch mirror %s -> %s\n", sourcePath, mirrorPath)
		}
		var leaseErr error
		leasePath, leaseErr = createYoloPatchMirrorLease(mirrorDir)
		if leaseErr != nil {
			return leaseErr
		}
		return cleanupYoloPatchMirrors(cacheRoot, key)
	})
	if err != nil {
		return "", false, "", err
	}
	return mirrorPath, reused, leasePath, nil
}

func yoloPatchMirrorKey(sourceHash string, specsHash string, proxyVersion string, executable string) string {
	hasher := sha256.New()
	for _, part := range []string{sourceHash, specsHash, proxyVersion, runtimeGOOS, runtime.GOARCH, executable} {
		_, _ = io.WriteString(hasher, part)
		_, _ = io.WriteString(hasher, "\n")
	}
	return "yolo-" + hex.EncodeToString(hasher.Sum(nil))[:32]
}

func yoloPatchMirrorValid(mirrorPath string, want yoloPatchMirrorManifest) bool {
	info, err := os.Stat(mirrorPath)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	manifestPath := filepath.Join(filepath.Dir(mirrorPath), "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return false
	}
	var got yoloPatchMirrorManifest
	if err := json.Unmarshal(data, &got); err != nil {
		return false
	}
	if got.Version != want.Version ||
		got.SourceSHA256 != want.SourceSHA256 ||
		got.SpecsSHA256 != want.SpecsSHA256 ||
		got.PatchedSHA256 != want.PatchedSHA256 ||
		got.ProxyVersion != want.ProxyVersion ||
		got.GOOS != want.GOOS ||
		got.GOARCH != want.GOARCH ||
		got.Executable != want.Executable {
		return false
	}
	sha, err := hashFileSHA256Fn(mirrorPath)
	return err == nil && sha == want.PatchedSHA256
}

func writeYoloPatchMirrorExecutable(path string, data []byte, perm os.FileMode) error {
	if err := diskspace.EnsureAvailableForWrite(path, uint64(len(data))); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create yolo patch mirror dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create yolo patch mirror temp: %w", diskspace.AnnotateWriteError(path, err))
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write yolo patch mirror temp: %w", diskspace.AnnotateWriteError(tmpPath, err))
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod yolo patch mirror temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync yolo patch mirror temp: %w", diskspace.AnnotateWriteError(tmpPath, err))
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close yolo patch mirror temp: %w", diskspace.AnnotateWriteError(tmpPath, err))
	}
	if err := os.Rename(tmpPath, path); err != nil {
		if removeErr := os.Remove(path); removeErr == nil || os.IsNotExist(removeErr) {
			if retryErr := os.Rename(tmpPath, path); retryErr == nil {
				return nil
			}
		}
		return fmt.Errorf("install yolo patch mirror: %w", diskspace.AnnotateWriteError(path, err))
	}
	return nil
}

func writeYoloPatchMirrorJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create yolo patch mirror metadata dir: %w", err)
	}
	tmpPath := fmt.Sprintf("%s.tmp-%d-%d", path, os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write yolo patch mirror metadata: %w", diskspace.AnnotateWriteError(tmpPath, err))
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("install yolo patch mirror metadata: %w", diskspace.AnnotateWriteError(path, err))
	}
	return nil
}

func createYoloPatchMirrorLease(mirrorDir string) (string, error) {
	lease := yoloPatchMirrorLease{
		PID:           os.Getpid(),
		StartIdentity: processStartIdentity(os.Getpid()),
		CreatedAt:     time.Now(),
	}
	path := filepath.Join(mirrorDir, fmt.Sprintf("active.%d.%d.json", lease.PID, time.Now().UnixNano()))
	if err := writeYoloPatchMirrorJSON(path, lease); err != nil {
		return "", err
	}
	return path, nil
}

func releasePatchOutcomeMirrorLease(outcome *patchOutcome) {
	if outcome == nil || strings.TrimSpace(outcome.MirrorLeasePath) == "" {
		return
	}
	_ = os.Remove(outcome.MirrorLeasePath)
	outcome.MirrorLeasePath = ""
}

func cleanupYoloPatchMirrors(cacheRoot string, keepKey string) error {
	entries, err := os.ReadDir(cacheRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read yolo patch mirror cache: %w", err)
	}
	now := time.Now()
	type mirrorEntry struct {
		key     string
		path    string
		modTime time.Time
		active  bool
	}
	mirrors := make([]mirrorEntry, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(cacheRoot, entry.Name())
		mirrors = append(mirrors, mirrorEntry{
			key:     entry.Name(),
			path:    path,
			modTime: info.ModTime(),
			active:  yoloPatchMirrorDirActive(path, now),
		})
	}
	if len(mirrors) <= yoloPatchMirrorKeep {
		return nil
	}
	sort.Slice(mirrors, func(i, j int) bool {
		return mirrors[i].modTime.After(mirrors[j].modTime)
	})
	keep := map[string]bool{}
	if keepKey = sanitizePathComponent(keepKey); keepKey != "" {
		keep[keepKey] = true
	}
	for _, entry := range mirrors {
		if entry.active {
			keep[entry.key] = true
		}
	}
	for _, entry := range mirrors {
		if len(keep) >= yoloPatchMirrorKeep {
			break
		}
		keep[entry.key] = true
	}
	for _, entry := range mirrors {
		if keep[entry.key] {
			continue
		}
		if err := os.RemoveAll(entry.path); err != nil {
			return fmt.Errorf("remove stale yolo patch mirror %s: %w", entry.path, err)
		}
	}
	return nil
}

func yoloPatchMirrorDirActive(mirrorDir string, now time.Time) bool {
	matches, err := filepath.Glob(filepath.Join(mirrorDir, "active.*.json"))
	if err != nil {
		return false
	}
	active := false
	for _, path := range matches {
		if yoloPatchMirrorLeaseActive(path, now) {
			active = true
			continue
		}
		_ = os.Remove(path)
	}
	return active
}

func yoloPatchMirrorLeaseActive(path string, now time.Time) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var lease yoloPatchMirrorLease
	if err := json.Unmarshal(data, &lease); err != nil {
		if info, statErr := os.Stat(path); statErr == nil && now.Sub(info.ModTime()) < yoloPatchMirrorCorruptLeaseTTL {
			return true
		}
		return false
	}
	if lease.PID <= 0 || !proc.IsAlive(lease.PID) {
		return false
	}
	currentIdentity := processStartIdentity(lease.PID)
	if lease.StartIdentity != "" && currentIdentity != "" {
		return lease.StartIdentity == currentIdentity
	}
	// Some platforms do not expose a cheap process start identity. In that case,
	// keep a live-PID lease active rather than deleting the mirror under a long
	// running Claude process.
	return true
}

func processStartIdentity(pid int) string {
	if runtimeGOOS != "linux" || pid <= 0 {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join("/proc", fmt.Sprintf("%d", pid), "stat"))
	if err != nil {
		return ""
	}
	text := string(raw)
	end := strings.LastIndex(text, ")")
	if end < 0 || end+2 >= len(text) {
		return ""
	}
	fields := strings.Fields(text[end+2:])
	if len(fields) <= 19 {
		return ""
	}
	return fields[19]
}
