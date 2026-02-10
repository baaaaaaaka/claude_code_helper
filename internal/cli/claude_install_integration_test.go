package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const claudeInstallIntegrationTimeout = 8 * time.Minute

func TestClaudeInstallLaunchIntegration(t *testing.T) {
	if os.Getenv("CLAUDE_INSTALL_TEST") != "1" {
		t.Skip("set CLAUDE_INSTALL_TEST=1 to run integration test")
	}
	if os.Getenv("CI") != "true" && os.Getenv("CLAUDE_INSTALL_TEST_ALLOW_LOCAL") != "1" {
		t.Skip("integration test runs only in CI; set CLAUDE_INSTALL_TEST_ALLOW_LOCAL=1 for local runs")
	}

	homeDir := t.TempDir()
	localBinDir := filepath.Join(homeDir, ".local", "bin")
	if err := os.MkdirAll(localBinDir, 0o755); err != nil {
		t.Fatalf("mkdir local bin: %v", err)
	}

	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(homeDir, ".config"))
	t.Setenv("PATH", filteredPathWithoutClaude(os.Getenv("PATH"), localBinDir))
	applyInstallProxyEnv(t)

	startupMarkerPath := ""
	if runtime.GOOS != "windows" {
		startupMarkerPath = writeStartupMarkerFiles(t, homeDir)
	}

	if path, err := exec.LookPath("claude"); err == nil {
		t.Fatalf("expected claude to be absent from PATH before installation, found %q", path)
	}

	ctx, cancel := context.WithTimeout(context.Background(), claudeInstallIntegrationTimeout)
	defer cancel()

	var installLog bytes.Buffer
	path, err := ensureClaudeInstalled(ctx, "", &installLog, installProxyOptions{})
	if err != nil {
		t.Fatalf("ensureClaudeInstalled: %v\ninstaller output:\n%s", err, installLog.String())
	}
	if strings.TrimSpace(path) == "" {
		t.Fatalf("ensureClaudeInstalled returned empty path")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("installed claude stat: %v", err)
	}
	if info.IsDir() {
		t.Fatalf("installed claude path is a directory: %q", path)
	}

	out, err := runClaudeProbe(path, "--version")
	if err != nil {
		t.Fatalf("claude --version: %v\noutput: %s\ninstaller output:\n%s", err, out, installLog.String())
	}
	if got := extractVersion(out); got == "" {
		t.Fatalf("failed to parse claude version from output %q", strings.TrimSpace(out))
	}

	if startupMarkerPath != "" {
		data, err := os.ReadFile(startupMarkerPath)
		if err != nil {
			if !os.IsNotExist(err) {
				t.Fatalf("read startup marker: %v", err)
			}
		} else if strings.TrimSpace(string(data)) != "" {
			t.Fatalf("installer unexpectedly sourced login startup files:\n%s", string(data))
		}
	}
}

func filteredPathWithoutClaude(currentPath string, localBinDir string) string {
	parts := filepath.SplitList(currentPath)
	filtered := make([]string, 0, len(parts)+1)
	filtered = append(filtered, localBinDir)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if samePath(part, localBinDir) {
			continue
		}
		if hasClaudeBinary(part) {
			continue
		}
		filtered = append(filtered, part)
	}
	return strings.Join(filtered, string(os.PathListSeparator))
}

func hasClaudeBinary(dir string) bool {
	names := []string{"claude"}
	if runtime.GOOS == "windows" {
		names = []string{"claude.exe", "claude.cmd", "claude.bat", "claude.com", "claude"}
	}
	for _, name := range names {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func samePath(a string, b string) bool {
	cleanA := filepath.Clean(a)
	cleanB := filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(cleanA, cleanB)
	}
	return cleanA == cleanB
}

func writeStartupMarkerFiles(t *testing.T, homeDir string) string {
	t.Helper()
	markerPath := filepath.Join(homeDir, ".claude_install_startup_marker")
	escapedMarker := strings.ReplaceAll(markerPath, "\"", "\\\"")
	payload := "echo startup_sourced >> \"" + escapedMarker + "\"\n"
	for _, name := range []string{".bash_profile", ".profile"} {
		path := filepath.Join(homeDir, name)
		if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return markerPath
}

func applyInstallProxyEnv(t *testing.T) {
	t.Helper()
	proxyURL := strings.TrimSpace(os.Getenv("CLAUDE_INSTALL_TEST_PROXY_URL"))
	if proxyURL == "" {
		return
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		t.Setenv(key, proxyURL)
	}
	mergedNoProxy := mergeNoProxyForInstallTest(
		firstNonEmptyNonBlank(os.Getenv("NO_PROXY"), os.Getenv("no_proxy")),
		[]string{"localhost", "127.0.0.1", "::1"},
	)
	t.Setenv("NO_PROXY", mergedNoProxy)
	t.Setenv("no_proxy", mergedNoProxy)
}

func firstNonEmptyNonBlank(a string, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func mergeNoProxyForInstallTest(existing string, required []string) string {
	seen := map[string]bool{}
	merged := make([]string, 0, len(required)+1)
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		key := strings.ToLower(v)
		if seen[key] {
			return
		}
		seen[key] = true
		merged = append(merged, v)
	}
	for _, part := range strings.Split(existing, ",") {
		add(part)
	}
	for _, item := range required {
		add(item)
	}
	return strings.Join(merged, ",")
}
