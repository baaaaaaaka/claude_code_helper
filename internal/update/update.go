package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	EnvRepo       = "CLAUDE_PROXY_REPO"
	EnvVersion    = "CLAUDE_PROXY_VERSION"
	EnvInstallDir = "CLAUDE_PROXY_INSTALL_DIR"

	DefaultRepo = "baaaaaaaka/claude_code_helper"
)

var (
	githubAPIBase     = "https://api.github.com"
	githubReleaseBase = "https://github.com"
)

type Status struct {
	Supported        bool
	Repo             string
	InstalledVersion string
	RemoteVersion    string
	Asset            string
	UpdateAvailable  bool
	Error            string
}

type CheckOptions struct {
	Repo             string
	InstalledVersion string
	Timeout          time.Duration
}

type UpdateOptions struct {
	Repo        string
	Version     string
	InstallPath string
	Timeout     time.Duration
}

type ApplyResult struct {
	Repo            string
	Version         string
	Asset           string
	InstallPath     string
	RestartRequired bool
}

type replaceResult struct {
	restartRequired bool
}

type releaseInfo struct {
	TagName string `json:"tag_name"`
}

func ResolveRepo(explicit string) string {
	if v := strings.TrimSpace(explicit); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv(EnvRepo)); v != "" {
		return v
	}
	return DefaultRepo
}

func ResolveVersion(explicit string) string {
	if v := strings.TrimSpace(explicit); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv(EnvVersion)); v != "" {
		return v
	}
	return "latest"
}

func ResolveInstallPath(explicit string) (string, error) {
	if v := strings.TrimSpace(explicit); v != "" {
		return normalizeInstallPath(v)
	}
	if v := strings.TrimSpace(os.Getenv(EnvInstallDir)); v != "" {
		return normalizeInstallPath(v)
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Clean(exe), nil
}

func normalizeInstallPath(path string) (string, error) {
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		return filepath.Join(path, binaryName()), nil
	}
	return filepath.Clean(path), nil
}

func CheckForUpdate(ctx context.Context, opts CheckOptions) Status {
	repo := ResolveRepo(opts.Repo)
	local := normalizeVersion(opts.InstalledVersion)
	if local == "" {
		return Status{
			Supported:        false,
			Repo:             repo,
			InstalledVersion: opts.InstalledVersion,
			Error:            "unknown local version",
		}
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	_, remote, err := fetchLatestRelease(ctx, repo, timeout)
	if err != nil {
		return Status{
			Supported:        false,
			Repo:             repo,
			InstalledVersion: local,
			Error:            err.Error(),
		}
	}

	asset, err := assetName(remote, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return Status{
			Supported:        false,
			Repo:             repo,
			InstalledVersion: local,
			RemoteVersion:    remote,
			Error:            err.Error(),
		}
	}

	newer, ok := isVersionNewer(remote, local)
	if !ok {
		return Status{
			Supported:        false,
			Repo:             repo,
			InstalledVersion: local,
			RemoteVersion:    remote,
			Asset:            asset,
			Error:            fmt.Sprintf("unsupported version format (local=%s, remote=%s)", local, remote),
		}
	}

	return Status{
		Supported:        true,
		Repo:             repo,
		InstalledVersion: local,
		RemoteVersion:    remote,
		Asset:            asset,
		UpdateAvailable:  newer,
		Error:            "",
	}
}

func PerformUpdate(ctx context.Context, opts UpdateOptions) (ApplyResult, error) {
	repo := ResolveRepo(opts.Repo)
	version := ResolveVersion(opts.Version)
	if version == "" {
		version = "latest"
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	tag := version
	verNoV := strings.TrimPrefix(version, "v")
	if strings.EqualFold(version, "latest") {
		var err error
		tag, verNoV, err = fetchLatestRelease(ctx, repo, timeout)
		if err != nil {
			return ApplyResult{}, err
		}
	} else if verNoV == "" {
		return ApplyResult{}, fmt.Errorf("invalid version %q", version)
	}

	asset, err := assetName(verNoV, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return ApplyResult{}, err
	}

	installPath, err := ResolveInstallPath(opts.InstallPath)
	if err != nil {
		return ApplyResult{}, err
	}

	tmpPath, err := downloadReleaseAsset(ctx, repo, tag, asset, installPath, timeout)
	if err != nil {
		return ApplyResult{}, err
	}

	rep, err := replaceBinary(tmpPath, installPath)
	if err != nil {
		return ApplyResult{}, err
	}

	return ApplyResult{
		Repo:            repo,
		Version:         verNoV,
		Asset:           asset,
		InstallPath:     installPath,
		RestartRequired: rep.restartRequired,
	}, nil
}

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "claude-proxy.exe"
	}
	return "claude-proxy"
}

func normalizeVersion(v string) string {
	s := strings.TrimSpace(v)
	s = strings.TrimPrefix(s, "v")
	if s == "" || strings.EqualFold(s, "dev") {
		return ""
	}
	return s
}

func fetchLatestRelease(ctx context.Context, repo string, timeout time.Duration) (string, string, error) {
	tag, ver, err := fetchLatestReleaseAPI(ctx, repo, timeout)
	if err == nil {
		return tag, ver, nil
	}

	tag, ver, redirectErr := fetchLatestReleaseRedirect(ctx, repo, timeout)
	if redirectErr == nil {
		return tag, ver, nil
	}

	return "", "", fmt.Errorf("release lookup failed: %v; redirect fallback failed: %v", err, redirectErr)
}

func fetchLatestReleaseAPI(ctx context.Context, repo string, timeout time.Duration) (string, string, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", githubAPIBase, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "claude-proxy")
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("release lookup failed: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}

	var parsed releaseInfo
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", "", err
	}
	tag := strings.TrimSpace(parsed.TagName)
	if tag == "" {
		return "", "", fmt.Errorf("missing tag_name in GitHub response")
	}
	ver := strings.TrimPrefix(tag, "v")
	return tag, ver, nil
}

func fetchLatestReleaseRedirect(ctx context.Context, repo string, timeout time.Duration) (string, string, error) {
	url := fmt.Sprintf("%s/%s/releases/latest", githubReleaseBase, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "claude-proxy")
	req.Header.Set("Accept", "text/html")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return "", "", fmt.Errorf("release redirect lookup failed: %s", resp.Status)
	}

	tag := latestTagFromPath(resp.Request.URL.Path)
	if tag == "" || strings.EqualFold(tag, "latest") {
		return "", "", fmt.Errorf("missing tag in redirect URL")
	}
	ver := strings.TrimPrefix(tag, "v")
	if ver == "" {
		return "", "", fmt.Errorf("invalid latest tag %q", tag)
	}
	return tag, ver, nil
}

func latestTagFromPath(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i := len(parts) - 2; i >= 0; i-- {
		if parts[i] == "tag" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}

func assetName(version, goos, goarch string) (string, error) {
	osName := strings.ToLower(goos)
	archName := strings.ToLower(goarch)

	switch osName {
	case "linux", "darwin", "windows":
	default:
		return "", fmt.Errorf("unsupported OS %q", osName)
	}

	switch archName {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf("unsupported architecture %q", archName)
	}

	if osName == "linux" && archName == "arm64" {
		return "", fmt.Errorf("linux arm64 release asset not published")
	}
	if osName == "windows" && archName == "arm64" {
		return "", fmt.Errorf("windows arm64 release asset not published")
	}

	name := fmt.Sprintf("claude-proxy_%s_%s_%s", version, osName, archName)
	if osName == "windows" {
		name += ".exe"
	}
	return name, nil
}

func buildReleaseURL(repo, tag, asset string) string {
	return fmt.Sprintf("%s/%s/releases/download/%s/%s", githubReleaseBase, repo, tag, asset)
}

func buildChecksumsURL(repo, tag string) string {
	return fmt.Sprintf("%s/%s/releases/download/%s/checksums.txt", githubReleaseBase, repo, tag)
}

func downloadReleaseAsset(ctx context.Context, repo, tag, asset, installPath string, timeout time.Duration) (string, error) {
	destDir := filepath.Dir(installPath)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("create install dir: %w", err)
	}

	tmpFile, err := os.CreateTemp(destDir, "."+filepath.Base(asset)+".*")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer tmpFile.Close()

	if err := fetchToWriter(ctx, buildReleaseURL(repo, tag, asset), tmpFile, timeout); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}

	if err := verifyChecksum(ctx, repo, tag, asset, tmpPath, timeout); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}

	if runtime.GOOS != "windows" {
		_ = os.Chmod(tmpPath, 0o755)
	}

	return tmpPath, nil
}

func fetchToWriter(ctx context.Context, url string, w io.Writer, timeout time.Duration) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
		return fmt.Errorf("download failed: %s", resp.Status)
	}

	_, err = io.Copy(w, resp.Body)
	return err
}

func verifyChecksum(ctx context.Context, repo, tag, asset, path string, timeout time.Duration) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, buildChecksumsURL(repo, tag), nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "claude-proxy")
	req.Header.Set("Accept", "text/plain")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}
	expected := parseChecksums(raw)[asset]
	if expected == "" {
		return nil
	}

	actual, err := sha256File(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(expected, actual) {
		return fmt.Errorf("checksum mismatch for %s", asset)
	}
	return nil
}

func parseChecksums(raw []byte) map[string]string {
	out := make(map[string]string)
	lines := strings.Split(string(raw), "\n")
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		fields := strings.Fields(ln)
		if len(fields) < 2 {
			continue
		}
		sha := fields[0]
		name := fields[len(fields)-1]
		if len(sha) >= 32 && name != "" {
			out[name] = sha
		}
	}
	return out
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

func isVersionNewer(remote, local string) (bool, bool) {
	rv, ok := parseVersionTuple(remote)
	if !ok {
		return false, false
	}
	lv, ok := parseVersionTuple(local)
	if !ok {
		return false, false
	}

	n := len(rv)
	if len(lv) > n {
		n = len(lv)
	}
	for len(rv) < n {
		rv = append(rv, 0)
	}
	for len(lv) < n {
		lv = append(lv, 0)
	}

	for i := 0; i < n; i++ {
		if rv[i] > lv[i] {
			return true, true
		}
		if rv[i] < lv[i] {
			return false, true
		}
	}
	return false, true
}

func parseVersionTuple(v string) ([]int, bool) {
	s := strings.TrimSpace(v)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return nil, false
	}
	parts := strings.Split(s, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		digits := ""
		for _, ch := range p {
			if ch < '0' || ch > '9' {
				break
			}
			digits += string(ch)
		}
		if digits == "" {
			break
		}
		n, err := strconv.Atoi(digits)
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}
