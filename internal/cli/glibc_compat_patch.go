package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/update"
	"github.com/gofrs/flock"
)

const (
	patchelfBinaryName = "patchelf"

	claudeProxyHostIDEnv = "CLAUDE_PROXY_HOST_ID"

	glibcCompatRepoEnv = "CLAUDE_PROXY_GLIBC_COMPAT_REPO"
	glibcCompatTagEnv  = "CLAUDE_PROXY_GLIBC_COMPAT_TAG"

	glibcCompatDefaultTag      = "glibc-compat-v2.31"
	glibcCompatExtractedDir    = "glibc-2.31"
	glibcCompatDownloadTimeout = 2 * time.Minute
	glibcCompatMirrorKeep      = 2
)

type glibcCompatLayout struct {
	RootDir    string
	LibDir     string
	LoaderPath string
}

var (
	glibcCompatHostEligibleFn = isEL7GlibcCompatHost
	userCacheDirFn            = os.UserCacheDir
	userHomeDirFn             = os.UserHomeDir
	tempDirFn                 = os.TempDir
	readLinuxOSReleaseFn      = func() ([]byte, error) { return os.ReadFile("/etc/os-release") }
)

const (
	glibcCompatMirrorVariantPatched = "patched"
	glibcCompatMirrorVariantWrapper = "wrapper"
)

func applyClaudeGlibcCompatPatch(path string, opts exePatchOptions, log io.Writer, dryRun bool, outcome *patchOutcome) (*patchOutcome, bool, error) {
	if runtime.GOOS != "linux" || !opts.glibcCompatConfigured() {
		return outcome, false, nil
	}
	if log == nil {
		log = io.Discard
	}
	if outcome == nil {
		outcome = &patchOutcome{}
	}
	if strings.TrimSpace(outcome.SourcePath) == "" {
		outcome.SourcePath = path
	}

	layout, err := resolveOrPrepareGlibcCompatLayout(opts, log)
	if err != nil {
		return outcome, false, err
	}
	if dryRun {
		_, _ = fmt.Fprintf(log, "exe-patch: dry-run enabled; would prepare host-local glibc compat launch path for %s\n", path)
		return outcome, false, nil
	}
	preparedOutcome, compatApplied, compatErr := prepareGlibcCompatMirror(path, layout, log, outcome)
	if compatErr == nil {
		return preparedOutcome, compatApplied, nil
	}
	_, _ = fmt.Fprintf(log, "exe-patch: host-local glibc compat mirror failed: %v\n", compatErr)
	wrapperOutcome, wrapperErr := prepareGlibcCompatWrapper(path, layout, log, outcome)
	if wrapperErr != nil {
		return outcome, false, fmt.Errorf("prepare glibc compat mirror: %w; wrapper fallback failed: %v", compatErr, wrapperErr)
	}
	return wrapperOutcome, true, nil
}

func prepareGlibcCompatMirror(path string, layout glibcCompatLayout, log io.Writer, outcome *patchOutcome) (*patchOutcome, bool, error) {
	return prepareGlibcCompatLaunchMirror(path, layout, log, outcome, glibcCompatMirrorVariantPatched, true)
}

func prepareGlibcCompatLaunchMirror(path string, layout glibcCompatLayout, log io.Writer, outcome *patchOutcome, variant string, patchELF bool) (*patchOutcome, bool, error) {
	if outcome == nil {
		outcome = &patchOutcome{}
	}
	if strings.TrimSpace(outcome.SourcePath) == "" {
		outcome.SourcePath = path
	}
	hostRoot, _, err := resolveClaudeProxyHostRoot()
	if err != nil {
		return outcome, false, err
	}
	claudeRoot := filepath.Join(hostRoot, "claude")
	currentSHA, err := resolveGlibcCompatSourceSHA(path, outcome)
	if err != nil {
		return outcome, false, fmt.Errorf("hash glibc compat source: %w", err)
	}
	mirrorKey, err := glibcCompatMirrorKey(currentSHA, layout, variant)
	if err != nil {
		return outcome, false, fmt.Errorf("build glibc compat mirror key: %w", err)
	}
	mirrorDir := filepath.Join(claudeRoot, mirrorKey)
	mirrorPath := filepath.Join(mirrorDir, filepath.Base(path))
	lockPath := filepath.Join(claudeRoot, ".lock")
	created := false
	if err := withFileLock(lockPath, func() error {
		if fileExists(mirrorPath) {
			_ = touchPath(mirrorPath)
			_ = touchPath(mirrorDir)
			return pruneGlibcCompatMirrors(claudeRoot, mirrorKey)
		}
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("stat executable for glibc patch: %w", err)
		}
		if err := os.MkdirAll(mirrorDir, 0o755); err != nil {
			return fmt.Errorf("create glibc compat mirror dir: %w", err)
		}
		stagePath, err := copyToTempFile(path, mirrorDir, filepath.Base(path), info.Mode().Perm())
		if err != nil {
			return err
		}
		defer func() { _ = os.Remove(stagePath) }()
		if patchELF {
			if _, err := exec.LookPath(patchelfBinaryName); err != nil {
				return fmt.Errorf("missing %s in PATH: %w", patchelfBinaryName, err)
			}
			currentInterpreter, err := readPatchelfValue(stagePath, "--print-interpreter")
			if err != nil {
				return fmt.Errorf("read interpreter: %w", err)
			}
			currentRPath, err := readPatchelfValue(stagePath, "--print-rpath")
			if err != nil {
				return fmt.Errorf("read rpath: %w", err)
			}
			targetRPath := mergeRPath(layout.LibDir, currentRPath)
			if !sameFilePath(currentInterpreter, layout.LoaderPath) || !pathListContains(currentRPath, layout.LibDir) {
				if err := patchElfInterpreterAndRPath(stagePath, layout.LoaderPath, targetRPath); err != nil {
					return err
				}
			}
		}
		if err := os.Rename(stagePath, mirrorPath); err != nil {
			return fmt.Errorf("install glibc compat mirror: %w", err)
		}
		created = true
		return pruneGlibcCompatMirrors(claudeRoot, mirrorKey)
	}); err != nil {
		return outcome, false, err
	}
	outcome.TargetPath = mirrorPath
	outcome.LaunchArgsPrefix = []string{mirrorPath}
	outcome.Applied = false
	if created {
		if patchELF {
			_, _ = fmt.Fprintf(log, "exe-patch: prepared glibc compat mirror %s -> %s\n", path, mirrorPath)
		} else {
			_, _ = fmt.Fprintf(log, "exe-patch: prepared glibc compat wrapper mirror %s -> %s\n", path, mirrorPath)
		}
	} else {
		if patchELF {
			_, _ = fmt.Fprintf(log, "exe-patch: reusing glibc compat mirror %s\n", mirrorPath)
		} else {
			_, _ = fmt.Fprintf(log, "exe-patch: reusing glibc compat wrapper mirror %s\n", mirrorPath)
		}
	}
	return outcome, true, nil
}

func resolveGlibcCompatSourceSHA(path string, outcome *patchOutcome) (string, error) {
	sourcePath := strings.TrimSpace(path)
	targetPath := sourcePath
	if outcome != nil {
		if sha := strings.ToLower(strings.TrimSpace(outcome.SourceSHA256)); sha != "" {
			return sha, nil
		}
		if candidate := strings.TrimSpace(outcome.SourcePath); candidate != "" {
			sourcePath = candidate
		}
		if candidate := strings.TrimSpace(outcome.TargetPath); candidate != "" {
			targetPath = candidate
		}
		if sha := strings.ToLower(strings.TrimSpace(outcome.TargetSHA256)); sha != "" && sameFilePath(sourcePath, targetPath) {
			return sha, nil
		}
	}
	if sourcePath == "" {
		return "", fmt.Errorf("source path is empty")
	}
	return sha256File(sourcePath)
}

func glibcCompatMirrorKey(sourceSHA string, layout glibcCompatLayout, variant string) (string, error) {
	sourceSHA = strings.ToLower(strings.TrimSpace(sourceSHA))
	if sourceSHA == "" {
		return "", fmt.Errorf("source sha is empty")
	}
	variant = sanitizePathComponent(variant)
	if variant == "" {
		variant = "default"
	}
	fingerprint, err := glibcCompatLayoutFingerprint(layout)
	if err != nil {
		return "", err
	}
	return sanitizePathComponent(sourceSHA + "-" + variant + "-" + fingerprint), nil
}

func glibcCompatLayoutFingerprint(layout glibcCompatLayout) (string, error) {
	loaderHash, err := sha256File(layout.LoaderPath)
	if err != nil {
		return "", fmt.Errorf("hash glibc loader: %w", err)
	}
	libcHash, err := sha256File(filepath.Join(layout.LibDir, "libc.so.6"))
	if err != nil {
		return "", fmt.Errorf("hash glibc libc: %w", err)
	}
	h := sha256.New()
	for _, part := range []string{
		filepath.Clean(layout.RootDir),
		filepath.Clean(layout.LoaderPath),
		filepath.Clean(layout.LibDir),
		strings.ToLower(loaderHash),
		strings.ToLower(libcHash),
	} {
		_, _ = io.WriteString(h, part)
		_, _ = io.WriteString(h, "\x00")
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

func prepareGlibcCompatWrapper(path string, layout glibcCompatLayout, log io.Writer, outcome *patchOutcome) (*patchOutcome, error) {
	if outcome == nil {
		outcome = &patchOutcome{}
	}
	if strings.TrimSpace(outcome.SourcePath) == "" {
		outcome.SourcePath = path
	}
	wrapperPath, err := resolveGlibcCompatWrapperPath(layout)
	if err != nil {
		return outcome, err
	}
	outcome, _, err = prepareGlibcCompatLaunchMirror(path, layout, log, outcome, glibcCompatMirrorVariantWrapper, false)
	if err != nil {
		return outcome, err
	}
	outcome.LaunchArgsPrefix = []string{wrapperPath, outcome.TargetPath}
	outcome.Applied = false
	_, _ = fmt.Fprintf(log, "exe-patch: using glibc compat wrapper %s for %s via %s\n", wrapperPath, path, outcome.TargetPath)
	return outcome, nil
}

func resolveOrPrepareGlibcCompatLayout(opts exePatchOptions, log io.Writer) (glibcCompatLayout, error) {
	root := strings.TrimSpace(opts.glibcCompatRoot)
	if root != "" {
		return resolveGlibcCompatLayout(root)
	}
	autoRoot, err := ensureDefaultGlibcCompatRuntime(log)
	if err != nil {
		return glibcCompatLayout{}, err
	}
	return resolveGlibcCompatLayout(autoRoot)
}

func ensureDefaultGlibcCompatRuntime(log io.Writer) (string, error) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return "", fmt.Errorf("automatic glibc compat download unsupported on %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	repo := resolveGlibcCompatRepo()
	tag := resolveGlibcCompatTag()
	asset, err := glibcCompatAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}

	hostRoot, _, err := resolveClaudeProxyHostRoot()
	if err != nil {
		return "", err
	}
	cacheRoot := filepath.Join(hostRoot, "glibc-compat", sanitizePathComponent(tag))
	runtimeRoot := filepath.Join(cacheRoot, "runtime")
	if _, err := resolveGlibcCompatLayout(runtimeRoot); err == nil {
		return runtimeRoot, nil
	}
	lockPath := filepath.Join(cacheRoot, ".runtime.lock")
	if err := withFileLock(lockPath, func() error {
		if _, err := resolveGlibcCompatLayout(runtimeRoot); err == nil {
			return nil
		}
		if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
			return fmt.Errorf("create glibc compat cache dir: %w", err)
		}
		bundlePath := filepath.Join(cacheRoot, asset)
		checksumPath := bundlePath + ".sha256"
		if err := ensureGlibcCompatBundle(cacheRoot, repo, tag, asset, bundlePath, checksumPath); err != nil {
			return err
		}
		stageDir, err := os.MkdirTemp(cacheRoot, "runtime-staging-")
		if err != nil {
			return fmt.Errorf("create glibc compat staging dir: %w", err)
		}
		defer func() { _ = os.RemoveAll(stageDir) }()
		if err := extractGlibcCompatBundle(bundlePath, stageDir); err != nil {
			return err
		}
		if _, err := resolveGlibcCompatLayout(stageDir); err != nil {
			return err
		}
		if err := installPreparedGlibcCompatRuntime(stageDir, runtimeRoot); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return "", err
	}
	if _, err := resolveGlibcCompatLayout(runtimeRoot); err != nil {
		return "", err
	}
	if log != nil {
		_, _ = fmt.Fprintf(log, "exe-patch: downloaded glibc compat bundle to %s\n", cacheRoot)
	}
	return runtimeRoot, nil
}

func installPreparedGlibcCompatRuntime(stageDir string, runtimeRoot string) error {
	if _, err := resolveGlibcCompatLayout(runtimeRoot); err == nil {
		return nil
	}
	if info, err := os.Stat(runtimeRoot); err == nil {
		if !info.IsDir() {
			if removeErr := os.Remove(runtimeRoot); removeErr != nil {
				return fmt.Errorf("remove invalid glibc compat runtime file: %w", removeErr)
			}
		} else if removeErr := os.RemoveAll(runtimeRoot); removeErr != nil {
			return fmt.Errorf("remove invalid glibc compat runtime dir: %w", removeErr)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat existing glibc compat runtime: %w", err)
	}
	if err := os.Rename(stageDir, runtimeRoot); err != nil {
		if _, statErr := resolveGlibcCompatLayout(runtimeRoot); statErr == nil {
			return nil
		}
		return fmt.Errorf("install glibc compat runtime: %w", err)
	}
	return nil
}

func resolveClaudeProxyHostRoot() (string, string, error) {
	cacheBase, err := resolveStableCacheBase()
	if err != nil {
		return "", "", err
	}
	hostID := resolveHostID()
	return filepath.Join(cacheBase, "claude-proxy", "hosts", hostID), hostID, nil
}

func resolveStableCacheBase() (string, error) {
	if cacheBase, err := userCacheDirFn(); err == nil && strings.TrimSpace(cacheBase) != "" {
		return cacheBase, nil
	}
	homeDir, err := userHomeDirFn()
	if err != nil || strings.TrimSpace(homeDir) == "" {
		if tempDir := strings.TrimSpace(tempDirFn()); tempDir != "" {
			return tempDir, nil
		}
		return "", fmt.Errorf("resolve stable cache dir: %w", err)
	}
	return filepath.Join(homeDir, ".cache"), nil
}

func resolveHostID() string {
	if v := strings.TrimSpace(os.Getenv(claudeProxyHostIDEnv)); v != "" {
		return sanitizePathComponent(v)
	}
	for _, candidate := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		raw, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		if id := sanitizePathComponent(strings.TrimSpace(string(raw))); id != "" && id != "default" {
			return id
		}
	}
	if hostname, err := os.Hostname(); err == nil {
		if id := sanitizePathComponent(hostname); id != "" {
			return id
		}
	}
	return "unknown-host"
}

func isEL7GlibcCompatHost() bool {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return false
	}
	versionID := linuxOSReleaseField("VERSION_ID")
	if !strings.HasPrefix(strings.Trim(versionID, "\""), "7") {
		return false
	}
	switch linuxOSReleaseID() {
	case "centos", "rhel":
		return true
	}
	for _, field := range strings.Fields(strings.ReplaceAll(linuxOSReleaseField("ID_LIKE"), ",", " ")) {
		switch strings.ToLower(strings.Trim(field, "\"")) {
		case "centos", "rhel":
			return true
		}
	}
	return false
}

func linuxOSReleaseField(key string) string {
	raw, err := readLinuxOSReleaseFn()
	if err != nil {
		return ""
	}
	prefix := key + "="
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		return strings.Trim(strings.TrimPrefix(line, prefix), "\"")
	}
	return ""
}

func withFileLock(lockPath string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return fmt.Errorf("create lock dir: %w", err)
	}
	lock := flock.New(lockPath)
	if err := lock.Lock(); err != nil {
		return fmt.Errorf("lock %s: %w", lockPath, err)
	}
	defer func() { _ = lock.Unlock() }()
	return fn()
}

func copyToTempFile(sourcePath string, dir string, prefix string, perm os.FileMode) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create glibc compat mirror dir: %w", err)
	}
	f, err := os.CreateTemp(dir, prefix+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("create temp mirror: %w", err)
	}
	tmpPath := f.Name()
	src, err := os.Open(sourcePath)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("open executable for glibc mirror: %w", err)
	}
	if _, err := io.Copy(f, src); err != nil {
		_ = src.Close()
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("copy executable for glibc mirror: %w", err)
	}
	_ = src.Close()
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("close temp mirror: %w", err)
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("chmod temp mirror: %w", err)
	}
	return tmpPath, nil
}

func touchPath(path string) error {
	now := time.Now()
	return os.Chtimes(path, now, now)
}

func pruneGlibcCompatMirrors(claudeRoot string, keepKey string) error {
	entries, err := os.ReadDir(claudeRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read glibc compat mirror dir: %w", err)
	}
	type mirrorEntry struct {
		name    string
		modTime time.Time
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
		mirrors = append(mirrors, mirrorEntry{name: entry.Name(), modTime: info.ModTime()})
	}
	if len(mirrors) <= glibcCompatMirrorKeep {
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
		if len(keep) >= glibcCompatMirrorKeep {
			break
		}
		keep[entry.name] = true
	}
	for _, entry := range mirrors {
		if keep[entry.name] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(claudeRoot, entry.name)); err != nil {
			return fmt.Errorf("remove stale glibc compat mirror %s: %w", entry.name, err)
		}
	}
	return nil
}

func resolveGlibcCompatWrapperPath(layout glibcCompatLayout) (string, error) {
	candidates := []string{
		filepath.Join(filepath.Dir(layout.RootDir), "run-with-glibc-2.31.sh"),
		filepath.Join(layout.RootDir, "run-with-glibc-2.31.sh"),
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(layout.RootDir), "run-with-glibc-*.sh"))
	candidates = append(candidates, matches...)
	seen := map[string]bool{}
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("glibc compat wrapper not found near %s", layout.RootDir)
}

func ensureGlibcCompatBundle(cacheRoot string, repo string, tag string, asset string, bundlePath string, checksumPath string) error {
	assetURL := glibcCompatReleaseURL(repo, tag, asset)
	checksumURL := glibcCompatReleaseURL(repo, tag, asset+".sha256")
	if err := downloadIfMissing(assetURL, bundlePath, glibcCompatDownloadTimeout); err != nil {
		return err
	}
	if err := downloadIfMissing(checksumURL, checksumPath, glibcCompatDownloadTimeout); err != nil {
		return err
	}
	if err := verifyBundleSHA256(bundlePath, checksumPath); err == nil {
		return nil
	}

	_ = os.Remove(bundlePath)
	_ = os.Remove(checksumPath)
	if err := downloadURLToFile(assetURL, bundlePath, glibcCompatDownloadTimeout); err != nil {
		return err
	}
	if err := downloadURLToFile(checksumURL, checksumPath, glibcCompatDownloadTimeout); err != nil {
		return err
	}
	if err := verifyBundleSHA256(bundlePath, checksumPath); err != nil {
		return fmt.Errorf("verify downloaded glibc bundle in %s: %w", cacheRoot, err)
	}
	return nil
}

func resolveGlibcCompatRepo() string {
	if v := strings.TrimSpace(os.Getenv(glibcCompatRepoEnv)); v != "" {
		return v
	}
	return update.ResolveRepo("")
}

func resolveGlibcCompatTag() string {
	if v := strings.TrimSpace(os.Getenv(glibcCompatTagEnv)); v != "" {
		return v
	}
	return glibcCompatDefaultTag
}

func glibcCompatAssetName(goos string, goarch string) (string, error) {
	if goos != "linux" || goarch != "amd64" {
		return "", fmt.Errorf("unsupported glibc compat platform: %s/%s", goos, goarch)
	}
	return "glibc-2.31-centos7-runtime-x86_64.tar.xz", nil
}

func glibcCompatReleaseURL(repo string, tag string, asset string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, tag, asset)
}

func downloadIfMissing(url string, targetPath string, timeout time.Duration) error {
	if fileExists(targetPath) {
		return nil
	}
	return downloadURLToFile(url, targetPath, timeout)
}

func downloadURLToFile(url string, targetPath string, timeout time.Duration) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("create download dir: %w", err)
	}
	tmpPath := targetPath + ".tmp"
	_ = os.Remove(tmpPath)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "claude-proxy")
	req.Header.Set("Accept", "application/octet-stream")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download %s failed: %s", url, resp.Status)
	}

	out, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		_ = out.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func verifyBundleSHA256(bundlePath string, checksumPath string) error {
	raw, err := os.ReadFile(checksumPath)
	if err != nil {
		return err
	}
	expected := firstSHA256Token(string(raw))
	if expected == "" {
		return fmt.Errorf("missing sha256 in %s", checksumPath)
	}
	actual, err := sha256File(bundlePath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(expected, actual) {
		return fmt.Errorf("checksum mismatch for %s", bundlePath)
	}
	return nil
}

func firstSHA256Token(raw string) string {
	for _, field := range strings.Fields(raw) {
		field = strings.TrimSpace(field)
		if isHexSHA256(field) {
			return strings.ToLower(field)
		}
	}
	return ""
}

func isHexSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, ch := range s {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f' || ch >= 'A' && ch <= 'F') {
			return false
		}
	}
	return true
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func extractGlibcCompatBundle(bundlePath string, runtimeRoot string) error {
	if _, err := exec.LookPath("tar"); err != nil {
		return fmt.Errorf("missing tar in PATH: %w", err)
	}
	if err := os.MkdirAll(runtimeRoot, 0o755); err != nil {
		return err
	}
	cmd := exec.Command("tar", "-xJf", bundlePath, "-C", runtimeRoot)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return err
		}
		return fmt.Errorf("extract glibc bundle: %w: %s", err, msg)
	}
	return nil
}

func sanitizePathComponent(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "default"
	}
	v = strings.ReplaceAll(v, "/", "_")
	v = strings.ReplaceAll(v, "\\", "_")
	v = strings.ReplaceAll(v, " ", "_")
	return v
}

func resolveGlibcCompatLayout(root string) (glibcCompatLayout, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return glibcCompatLayout{}, fmt.Errorf("glibc compat root is empty")
	}
	candidates := []string{
		filepath.Clean(root),
		filepath.Join(root, glibcCompatExtractedDir),
	}
	if entries, err := os.ReadDir(root); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "glibc-") {
				continue
			}
			candidates = append(candidates, filepath.Join(root, entry.Name()))
		}
	}

	seen := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		libDir := filepath.Join(candidate, "lib")
		loaderPath := filepath.Join(libDir, "ld-linux-x86-64.so.2")
		libcPath := filepath.Join(libDir, "libc.so.6")
		if fileExists(loaderPath) && fileExists(libcPath) {
			return glibcCompatLayout{
				RootDir:    candidate,
				LibDir:     libDir,
				LoaderPath: loaderPath,
			}, nil
		}
	}
	return glibcCompatLayout{}, fmt.Errorf("glibc compat runtime not found under %s", root)
}

func isMissingGlibcSymbolError(output string) bool {
	if !strings.Contains(output, "GLIBC_") {
		return false
	}
	return strings.Contains(strings.ToLower(output), "not found")
}

func readPatchelfValue(path string, flag string) (string, error) {
	out, err := runPatchelf(flag, path)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return "", err
		}
		return "", fmt.Errorf("%w: %s", err, msg)
	}
	return strings.TrimSpace(string(out)), nil
}

func patchElfInterpreterAndRPath(path string, loaderPath string, rpath string) error {
	out, err := runPatchelf("--set-interpreter", loaderPath, "--set-rpath", rpath, path)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, msg)
	}
	return nil
}

func runPatchelf(args ...string) ([]byte, error) {
	cmd := exec.Command(patchelfBinaryName, args...)
	return cmd.CombinedOutput()
}

func mergeRPath(preferred string, existing string) string {
	var merged []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		for _, item := range merged {
			if sameFilePath(item, path) {
				return
			}
		}
		merged = append(merged, path)
	}
	add(preferred)
	for _, part := range strings.Split(existing, ":") {
		add(part)
	}
	return strings.Join(merged, ":")
}

func pathListContains(pathList string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for _, path := range strings.Split(pathList, ":") {
		if sameFilePath(path, target) {
			return true
		}
	}
	return false
}

func sameFilePath(a string, b string) bool {
	a = filepath.Clean(strings.TrimSpace(a))
	b = filepath.Clean(strings.TrimSpace(b))
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
