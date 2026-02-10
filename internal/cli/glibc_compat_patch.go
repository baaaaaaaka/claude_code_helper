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
	"strings"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/update"
)

const (
	patchelfBinaryName = "patchelf"

	glibcCompatRepoEnv = "CLAUDE_PROXY_GLIBC_COMPAT_REPO"
	glibcCompatTagEnv  = "CLAUDE_PROXY_GLIBC_COMPAT_TAG"

	glibcCompatDefaultTag      = "glibc-compat-v2.31"
	glibcCompatExtractedDir    = "glibc-2.31"
	glibcCompatDownloadTimeout = 2 * time.Minute
)

type glibcCompatLayout struct {
	RootDir    string
	LibDir     string
	LoaderPath string
}

func applyClaudeGlibcCompatPatch(path string, opts exePatchOptions, log io.Writer, dryRun bool, outcome *patchOutcome) (*patchOutcome, bool, error) {
	if runtime.GOOS != "linux" || !opts.glibcCompatConfigured() {
		return outcome, false, nil
	}
	if log == nil {
		log = io.Discard
	}

	layout, err := resolveOrPrepareGlibcCompatLayout(opts, log)
	if err != nil {
		return outcome, false, err
	}
	if _, err := exec.LookPath(patchelfBinaryName); err != nil {
		return outcome, false, fmt.Errorf("missing %s in PATH: %w", patchelfBinaryName, err)
	}

	currentInterpreter, err := readPatchelfValue(path, "--print-interpreter")
	if err != nil {
		return outcome, false, fmt.Errorf("read interpreter: %w", err)
	}
	currentRPath, err := readPatchelfValue(path, "--print-rpath")
	if err != nil {
		return outcome, false, fmt.Errorf("read rpath: %w", err)
	}

	targetRPath := mergeRPath(layout.LibDir, currentRPath)
	if sameFilePath(currentInterpreter, layout.LoaderPath) && pathListContains(currentRPath, layout.LibDir) {
		_, _ = fmt.Fprintf(log, "exe-patch: glibc compat already configured for %s\n", path)
		return outcome, false, nil
	}
	if dryRun {
		_, _ = fmt.Fprintf(log, "exe-patch: dry-run enabled; would apply glibc compat patch to %s\n", path)
		return outcome, false, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return outcome, false, fmt.Errorf("stat executable for glibc patch: %w", err)
	}
	if outcome == nil {
		outcome = &patchOutcome{TargetPath: path}
	}
	if strings.TrimSpace(outcome.BackupPath) == "" {
		backupPath, err := backupExecutable(path, info.Mode().Perm())
		if err != nil {
			return outcome, false, fmt.Errorf("create backup for glibc patch: %w", err)
		}
		outcome.BackupPath = backupPath
	}

	if err := patchElfInterpreterAndRPath(path, layout.LoaderPath, targetRPath); err != nil {
		return outcome, false, err
	}

	outcome.TargetPath = path
	outcome.Applied = true
	_, _ = fmt.Fprintf(log, "exe-patch: applied glibc compat patch to %s (loader=%s)\n", path, layout.LoaderPath)
	return outcome, true, nil
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

	cacheBase, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(cacheBase) == "" {
		cacheBase = os.TempDir()
	}
	cacheRoot := filepath.Join(cacheBase, "claude-proxy", "glibc-compat", sanitizePathComponent(tag))
	runtimeRoot := filepath.Join(cacheRoot, "runtime")
	if _, err := resolveGlibcCompatLayout(runtimeRoot); err == nil {
		return runtimeRoot, nil
	}
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return "", fmt.Errorf("create glibc compat cache dir: %w", err)
	}

	bundlePath := filepath.Join(cacheRoot, asset)
	checksumPath := bundlePath + ".sha256"
	if err := ensureGlibcCompatBundle(cacheRoot, repo, tag, asset, bundlePath, checksumPath); err != nil {
		return "", err
	}
	if err := extractGlibcCompatBundle(bundlePath, runtimeRoot); err != nil {
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
	_ = os.RemoveAll(runtimeRoot)
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
