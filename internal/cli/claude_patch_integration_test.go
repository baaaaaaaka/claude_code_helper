package cli

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const defaultClaudePatchVersion = "2.1.94"
const defaultClaudeGCSBucket = "https://storage.googleapis.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases"
const claudePatchSkipPrecheckEnv = "CLAUDE_PATCH_SKIP_PRECHECK"

type claudePatchIntegrationOptions struct {
	seedKnownFailure bool
	rulesMode        bool
}

func TestClaudePatchIntegration(t *testing.T) {
	if os.Getenv("CLAUDE_PATCH_TEST") != "1" {
		t.Skip("set CLAUDE_PATCH_TEST=1 to run integration test")
	}
	wantVersion := strings.TrimSpace(os.Getenv("CLAUDE_PATCH_VERSION"))
	if wantVersion == "" {
		wantVersion = defaultClaudePatchVersion
	}
	installURL := strings.TrimSpace(os.Getenv("CLAUDE_PATCH_INSTALL_URL"))
	runClaudePatchIntegrationCase(t, wantVersion, installURL)
}

func TestClaudePatchIntegrationRetriesKnownFailure(t *testing.T) {
	if os.Getenv("CLAUDE_PATCH_TEST") != "1" {
		t.Skip("set CLAUDE_PATCH_TEST=1 to run integration test")
	}
	wantVersion := strings.TrimSpace(os.Getenv("CLAUDE_PATCH_VERSION"))
	if wantVersion == "" {
		wantVersion = defaultClaudePatchVersion
	}
	installURL := strings.TrimSpace(os.Getenv("CLAUDE_PATCH_INSTALL_URL"))
	runClaudePatchIntegrationCaseWithOptions(t, wantVersion, installURL, claudePatchIntegrationOptions{
		seedKnownFailure: true,
	})
}

func TestClaudePatchRulesIntegration(t *testing.T) {
	if os.Getenv("CLAUDE_PATCH_TEST") != "1" {
		t.Skip("set CLAUDE_PATCH_TEST=1 to run integration test")
	}
	wantVersion := strings.TrimSpace(os.Getenv("CLAUDE_PATCH_VERSION"))
	if wantVersion == "" {
		wantVersion = defaultClaudePatchVersion
	}
	installURL := strings.TrimSpace(os.Getenv("CLAUDE_PATCH_INSTALL_URL"))
	runClaudePatchIntegrationCaseWithOptions(t, wantVersion, installURL, claudePatchIntegrationOptions{
		rulesMode: true,
	})
}

func TestClaudePatchRegressionMatrix(t *testing.T) {
	if os.Getenv("CLAUDE_PATCH_TEST") != "1" {
		t.Skip("set CLAUDE_PATCH_TEST=1 to run integration test")
	}
	versions := parsePatchVersionMatrix(os.Getenv("CLAUDE_PATCH_VERSION_MATRIX"))
	if len(versions) == 0 {
		t.Skip("set CLAUDE_PATCH_VERSION_MATRIX (comma-separated) to run regression matrix")
	}
	installURL := strings.TrimSpace(os.Getenv("CLAUDE_PATCH_INSTALL_URL"))
	if installURL != "" && len(versions) > 1 {
		t.Fatalf("CLAUDE_PATCH_INSTALL_URL supports only one version per run")
	}

	for _, wantVersion := range versions {
		wantVersion := wantVersion
		t.Run(wantVersion, func(t *testing.T) {
			runClaudePatchIntegrationCase(t, wantVersion, installURL)
		})
	}
}

func runClaudePatchIntegrationCase(t *testing.T, wantVersion string, installURL string) {
	runClaudePatchIntegrationCaseWithOptions(t, wantVersion, installURL, claudePatchIntegrationOptions{})
}

func runClaudePatchIntegrationCaseWithOptions(t *testing.T, wantVersion string, installURL string, integrationOpts claudePatchIntegrationOptions) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	skipPrecheck := os.Getenv(claudePatchSkipPrecheckEnv) == "1"

	path, err := resolveClaudeForPatchTest(t, ctx, installURL, wantVersion)
	if err != nil {
		t.Fatalf("resolveClaudeForPatchTest: %v", err)
	}

	verifyRootGuard := false
	if skipPrecheck {
		t.Logf("skip pre-patch startup checks because %s=1", claudePatchSkipPrecheckEnv)
	} else {
		beforeVersion, err := runClaudeVersion(ctx, path, nil)
		if err != nil {
			t.Fatalf("claude --version (before): %v", err)
		}
		if !strings.Contains(beforeVersion, wantVersion) {
			t.Fatalf("expected claude %s, got %q", wantVersion, strings.TrimSpace(beforeVersion))
		}

		if integrationOpts.rulesMode {
			t.Log("skip root bypass probe in rules mode")
		} else if runtime.GOOS == "windows" {
			t.Log("skip root bypass probe on windows")
		} else if !supportsYoloFlag(path) {
			t.Log("skip root bypass probe: --permission-mode unsupported")
		} else if ok, reason := canRunAsRootWithoutPrompt(); !ok {
			t.Logf("skip root bypass probe: %s", reason)
		} else {
			probeCtx, cancelProbe := context.WithTimeout(ctx, 20*time.Second)
			beforeRoot, beforeRootErr := runClaudeAsRoot(probeCtx, path, nil, "--permission-mode", "bypassPermissions", "--version")
			cancelProbe()
			if !strings.Contains(beforeRoot, rootBypassGuardErrorMessage) {
				t.Logf("skip root bypass assert: guard message not observed before patch (err=%v, out=%q)", beforeRootErr, compactOutput(beforeRoot))
			} else {
				verifyRootGuard = true
			}
		}
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	opts := exePatchOptions{
		enabledFlag:     true,
		policySettings:  true,
		glibcCompat:     exePatchGlibcCompatDefault(),
		glibcCompatRoot: exePatchGlibcCompatRootDefault(),
	}
	if integrationOpts.seedKnownFailure {
		if err := recordPatchFailure(configPath, &patchOutcome{
			IsClaude:      true,
			SourcePath:    path,
			TargetPath:    path,
			TargetVersion: wantVersion,
		}, "seeded previous failure for integration retry"); err != nil {
			t.Fatalf("recordPatchFailure (seed known failure): %v", err)
		}
	}
	var log bytes.Buffer
	cmdArgs := yoloClaudeArgs(path)
	if integrationOpts.rulesMode {
		opts.allowBuiltInWithoutBypass = true
		cmdArgs = []string{path}
	}
	outcome, err := maybePatchExecutable(cmdArgs, opts, configPath, &log)
	if err != nil {
		t.Fatalf("maybePatchExecutable: %v\n%s", err, log.String())
	}
	if outcome == nil || (!outcome.Applied && !outcome.AlreadyPatched) {
		t.Fatalf("expected patch outcome, got none")
	}
	if integrationOpts.seedKnownFailure && !strings.Contains(log.String(), "previous failure recorded") {
		t.Fatalf("expected retry log after seeded failure, got:\n%s", log.String())
	}
	if !outcome.BuiltInClaudePatchActive {
		t.Fatalf("expected built-in Claude patch to be active after patch preparation")
	}
	if outcome.Applied && outcome.BackupPath != "" {
		t.Cleanup(func() {
			if restoreErr := restoreExecutableFromBackup(outcome); restoreErr != nil {
				t.Errorf("restoreExecutableFromBackup: %v", restoreErr)
			}
		})
	}

	// Verify every built-in policy patch matched at least once.
	// This catches silent pattern breakage (e.g. upstream code refactoring)
	// that would otherwise go undetected until a user hits the issue.
	if outcome.Applied {
		for _, stat := range outcome.PatchStats {
			if stat.Eligible == 0 && stat.Replacements == 0 {
				// process.getuid() is Unix-only; Windows binaries don't contain the root guard.
				if runtime.GOOS == "windows" && stat.Label == "root-bypass-guard" {
					t.Logf("patch %q: 0 matches on Windows (expected — process.getuid is Unix-only)", stat.Label)
					continue
				}
				t.Fatalf("patch %q found no matches in claude %s — the target pattern may have changed upstream\n%s",
					stat.Label, wantVersion, log.String())
			}
		}
	}

	afterVersion, err := runClaudeVersion(ctx, path, outcome)
	if err != nil {
		t.Fatalf("claude --version (after): %v\n%s", err, log.String())
	}
	if !strings.Contains(afterVersion, wantVersion) {
		t.Fatalf("expected claude %s after patch, got %q", wantVersion, strings.TrimSpace(afterVersion))
	}

	if verifyRootGuard {
		probeCtx, cancelProbe := context.WithTimeout(ctx, 20*time.Second)
		afterRoot, afterRootErr := runClaudeAsRoot(probeCtx, path, outcome, "--permission-mode", "bypassPermissions", "--version")
		cancelProbe()
		if afterRootErr != nil {
			t.Fatalf("claude --version (after, root bypass): %v\n%s", afterRootErr, compactOutput(afterRoot))
		}
		if strings.Contains(afterRoot, rootBypassGuardErrorMessage) {
			t.Fatalf("root bypass guard still present after patch: %q", compactOutput(afterRoot))
		}
		if !strings.Contains(afterRoot, wantVersion) {
			t.Fatalf("expected root bypass probe to return claude %s, got %q", wantVersion, compactOutput(afterRoot))
		}
	}

	if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("codesign"); err != nil {
			t.Skip("codesign not available")
		}
		verify := exec.Command("codesign", "--verify", "--verbose=2", path)
		if output, err := verify.CombinedOutput(); err != nil {
			t.Fatalf("codesign verify: %v: %s", err, string(output))
		}
	}

	assertClaudeTUIStartsWithOutcome(t, path, outcome)
}

func parsePatchVersionMatrix(raw string) []string {
	parts := strings.Split(raw, ",")
	seen := make(map[string]struct{}, len(parts))
	versions := make([]string, 0, len(parts))
	for _, part := range parts {
		version := strings.TrimSpace(part)
		if version == "" {
			continue
		}
		if _, ok := seen[version]; ok {
			continue
		}
		seen[version] = struct{}{}
		versions = append(versions, version)
	}
	return versions
}

func canRunAsRootWithoutPrompt() (bool, string) {
	if isRootSession() {
		return true, ""
	}
	if _, err := exec.LookPath("sudo"); err != nil {
		return false, "sudo not found"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "sudo", "-n", "true").Run(); err != nil {
		return false, "sudo -n unavailable"
	}
	return true, ""
}

func isRootSession() bool {
	out, err := exec.Command("id", "-u").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "0"
}

func runClaudeAsRoot(ctx context.Context, path string, outcome *patchOutcome, args ...string) (string, error) {
	cmdArgs := commandArgsForOutcome(outcome, append([]string{path}, args...))
	if isRootSession() {
		cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	sudoArgs := append([]string{"-n"}, cmdArgs...)
	cmd := exec.CommandContext(ctx, "sudo", sudoArgs...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func compactOutput(s string) string {
	trimmed := strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	const maxLen = 400
	if len(trimmed) <= maxLen {
		return trimmed
	}
	return trimmed[:maxLen] + "..."
}

func runClaudeVersion(ctx context.Context, path string, outcome *patchOutcome) (string, error) {
	cmdArgs := commandArgsForOutcome(outcome, []string{path, "--version"})
	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func assertClaudeTUIStartsWithOutcome(t *testing.T, path string, outcome *patchOutcome) {
	t.Helper()
	if outcome == nil || len(outcome.LaunchArgsPrefix) == 0 {
		assertClaudeTUIStarts(t, path)
		return
	}
	if runtime.GOOS == "windows" {
		assertClaudeTUIStarts(t, path)
		return
	}

	launchPath := path
	launchArgs := commandArgsForOutcome(outcome, []string{path})
	scriptPath := filepath.Join(t.TempDir(), "claude-launch.sh")
	var script strings.Builder
	script.WriteString("#!/bin/sh\nexec")
	for _, arg := range launchArgs {
		script.WriteString(" ")
		script.WriteString(shQuote(arg))
	}
	script.WriteString("\n")
	if err := os.WriteFile(scriptPath, []byte(script.String()), 0o755); err != nil {
		t.Fatalf("write TUI launch shim: %v", err)
	}
	launchPath = scriptPath
	assertClaudeTUIStarts(t, launchPath)
}

func shQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func resolveClaudeForPatchTest(t *testing.T, ctx context.Context, installURL string, version string) (string, error) {
	if installURL != "" {
		return installClaudeFromURL(t, ctx, installURL)
	}
	return installClaudeFromGCS(ctx, version)
}

func installClaudeFromGCS(ctx context.Context, version string) (string, error) {
	platform, err := platformForPatchTest()
	if err != nil {
		return "", err
	}
	bucket := bucketForPatchTest()
	manifest, err := fetchManifest(ctx, bucket, version)
	if err != nil {
		return "", err
	}
	entry, ok := manifest.Platforms[platform]
	if !ok || entry.Checksum == "" {
		return "", fmt.Errorf("platform %s not found in manifest", platform)
	}
	binName := claudeBinaryName()
	if platform == "win32-x64" {
		binName = "claude.exe"
	}
	url := fmt.Sprintf("%s/%s/%s/%s", bucket, manifest.Version, platform, binName)
	tmpDir, err := os.MkdirTemp("", "claude-download-")
	if err != nil {
		return "", err
	}
	binPath := filepath.Join(tmpDir, binName)
	if err := downloadFile(ctx, url, binPath); err != nil {
		return "", err
	}
	if err := verifySHA256(binPath, entry.Checksum); err != nil {
		return "", err
	}
	if err := ensureExecutable(binPath); err != nil {
		return "", err
	}
	return binPath, nil
}

func installClaudeFromURL(t *testing.T, ctx context.Context, url string) (string, error) {
	t.Helper()
	tmpDir := t.TempDir()
	downloadPath := filepath.Join(tmpDir, "claude-download")
	if err := downloadFile(ctx, url, downloadPath); err != nil {
		return "", err
	}
	if isArchive(url) {
		extractDir := filepath.Join(tmpDir, "claude-extract")
		if err := os.MkdirAll(extractDir, 0o755); err != nil {
			return "", err
		}
		if err := extractArchive(downloadPath, extractDir); err != nil {
			return "", err
		}
		return findClaudeBinary(extractDir)
	}

	binName := claudeBinaryName()
	binPath := filepath.Join(tmpDir, binName)
	if err := moveOrCopy(downloadPath, binPath); err != nil {
		return "", err
	}
	if err := ensureExecutable(binPath); err != nil {
		return "", err
	}
	return binPath, nil
}

func downloadFile(ctx context.Context, url, dst string) error {
	if strings.HasPrefix(url, "file://") {
		return copyFile(strings.TrimPrefix(url, "file://"), dst)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, resp.Body); err != nil {
		return err
	}
	return out.Close()
}

func isArchive(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".zip") || strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz")
}

func extractArchive(path, dest string) error {
	switch {
	case strings.HasSuffix(strings.ToLower(path), ".zip"):
		return extractZip(path, dest)
	case strings.HasSuffix(strings.ToLower(path), ".tar.gz"), strings.HasSuffix(strings.ToLower(path), ".tgz"):
		return extractTarGz(path, dest)
	default:
		return fmt.Errorf("unsupported archive format: %s", path)
	}
}

func extractZip(path, dest string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, f := range reader.File {
		target := filepath.Join(dest, f.Name)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		src, err := f.Open()
		if err != nil {
			return err
		}
		if err := writeFileFromReader(target, src, f.Mode()); err != nil {
			_ = src.Close()
			return err
		}
		_ = src.Close()
	}
	return nil
}

func extractTarGz(path, dest string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gzr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dest, hdr.Name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeFileFromReader(target, tr, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		default:
		}
	}
}

func writeFileFromReader(path string, r io.Reader, perm os.FileMode) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func findClaudeBinary(root string) (string, error) {
	want := strings.ToLower(claudeBinaryName())
	var found string
	errFound := errors.New("found")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.ToLower(d.Name()) == want {
			found = path
			return errFound
		}
		return nil
	})
	if err != nil && !errors.Is(err, errFound) {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("claude binary not found in %s", root)
	}
	if err := ensureExecutable(found); err != nil {
		return "", err
	}
	return found, nil
}

func claudeBinaryName() string {
	if runtime.GOOS == "windows" {
		return "claude.exe"
	}
	return "claude"
}

func ensureExecutable(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	return os.Chmod(path, 0o755)
}

func moveOrCopy(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	return copyFile(src, dst)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

type manifestEntry struct {
	Checksum string `json:"checksum"`
}

type releaseManifest struct {
	Version   string                   `json:"version"`
	Platforms map[string]manifestEntry `json:"platforms"`
}

func fetchManifest(ctx context.Context, bucket string, version string) (releaseManifest, error) {
	url := fmt.Sprintf("%s/%s/manifest.json", bucket, version)
	body, err := fetchText(ctx, url)
	if err != nil {
		return releaseManifest{}, err
	}
	var manifest releaseManifest
	if err := jsonUnmarshal([]byte(body), &manifest); err != nil {
		return releaseManifest{}, err
	}
	if manifest.Version == "" {
		manifest.Version = version
	}
	return manifest, nil
}

func fetchText(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func verifySHA256(path string, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actual := fmt.Sprintf("%x", h.Sum(nil))
	if strings.ToLower(actual) != strings.ToLower(strings.TrimSpace(expected)) {
		return fmt.Errorf("checksum mismatch for %s", path)
	}
	return nil
}

func bucketForPatchTest() string {
	bucket := strings.TrimSpace(os.Getenv("CLAUDE_PATCH_BUCKET"))
	if bucket == "" {
		return defaultClaudeGCSBucket
	}
	return strings.TrimRight(bucket, "/")
}

func platformForPatchTest() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		arch, err := archForPatchTest()
		if err != nil {
			return "", err
		}
		return "darwin-" + arch, nil
	case "linux":
		arch, err := archForPatchTest()
		if err != nil {
			return "", err
		}
		if isMusl() {
			return "linux-" + arch + "-musl", nil
		}
		return "linux-" + arch, nil
	case "windows":
		return "win32-x64", nil
	default:
		return "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

func archForPatchTest() (string, error) {
	switch runtime.GOARCH {
	case "amd64":
		return "x64", nil
	case "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
	}
}

func isMusl() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if _, err := os.Stat("/lib/libc.musl-x86_64.so.1"); err == nil {
		return true
	}
	if _, err := os.Stat("/lib/libc.musl-aarch64.so.1"); err == nil {
		return true
	}
	out, err := exec.Command("ldd", "/bin/ls").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "musl")
}

func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
