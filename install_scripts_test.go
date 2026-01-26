//go:build !windows

package installtest

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallShLatestViaAPI(t *testing.T) {
	runInstallSh(t, false, false)
}

func TestInstallShLatestViaRedirect(t *testing.T) {
	runInstallSh(t, true, false)
}

func TestInstallShSkipsPathUpdateWhenAlreadySet(t *testing.T) {
	runInstallSh(t, false, true)
}

func runInstallSh(t *testing.T, apiFail bool, pathAlreadySet bool) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	scriptPath := filepath.Join(repoRoot, "install.sh")

	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	writeStubCurl(t, binDir)

	homeDir := t.TempDir()
	installDir := t.TempDir()
	version := "v1.2.3"
	verNoV := strings.TrimPrefix(version, "v")
	asset := fmt.Sprintf("claude-proxy_%s_%s_%s", verNoV, runtime.GOOS, runtime.GOARCH)
	assetData := []byte("fake-binary")
	checksum := sha256.Sum256(assetData)
	checksums := fmt.Sprintf("%x  %s\n", checksum, asset)
	apiJSON := fmt.Sprintf("{\"tag_name\":\"%s\"}", version)
	latestURL := "https://github.com/owner/name/releases/tag/" + version

	pathValue := binDir + string(os.PathListSeparator) + os.Getenv("PATH")
	if pathAlreadySet {
		pathValue = installDir + string(os.PathListSeparator) + pathValue
	}
	env := append([]string{}, os.Environ()...)
	env = append(env,
		"PATH="+pathValue,
		"HOME="+homeDir,
		"SHELL=/bin/bash",
		"CLAUDE_PROXY_REPO=owner/name",
		"CLAUDE_PROXY_VERSION=latest",
		"CLAUDE_PROXY_INSTALL_DIR="+installDir,
		"CLAUDE_PROXY_TEST_API_FAIL="+boolEnv(apiFail),
		"CLAUDE_PROXY_TEST_API_JSON="+apiJSON,
		"CLAUDE_PROXY_TEST_LATEST_URL="+latestURL,
		"CLAUDE_PROXY_TEST_ASSET="+asset,
		"CLAUDE_PROXY_TEST_ASSET_DATA="+string(assetData),
		"CLAUDE_PROXY_TEST_CHECKSUMS="+checksums,
	)

	cmd := exec.Command("sh", scriptPath)
	cmd.Dir = repoRoot
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install.sh failed: %v\n%s", err, string(output))
	}

	installed := filepath.Join(installDir, "claude-proxy")
	got, err := os.ReadFile(installed)
	if err != nil {
		t.Fatalf("read installed: %v", err)
	}
	if string(got) != string(assetData) {
		t.Fatalf("installed payload mismatch")
	}

	clpPath := filepath.Join(installDir, "clp")
	clpData, err := os.ReadFile(clpPath)
	if err != nil {
		t.Fatalf("read clp: %v", err)
	}
	if string(clpData) != string(assetData) {
		t.Fatalf("clp payload mismatch")
	}

	configPath := expectedBashConfigPath(homeDir)
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read shell config: %v", err)
	}
	text := string(contents)
	pathLine := fmt.Sprintf("export PATH=\"%s:$PATH\"", installDir)
	if pathAlreadySet {
		if strings.Contains(text, pathLine) {
			t.Fatalf("unexpected PATH update in shell config")
		}
	} else {
		if !strings.Contains(text, pathLine) {
			t.Fatalf("missing PATH update in shell config")
		}
	}
	if !strings.Contains(text, "alias clp='claude-proxy'") {
		t.Fatalf("missing clp alias in shell config")
	}
}

func boolEnv(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func writeStubCurl(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, "curl")
	script := `#!/usr/bin/env sh
set -e
out=""
write_effective=""
url=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o)
      out="$2"
      shift 2
      ;;
    -w)
      write_effective="$2"
      shift 2
      ;;
    -*)
      shift
      ;;
    *)
      url="$1"
      shift
      ;;
  esac
done

if [ -n "$write_effective" ]; then
  if [ -z "${CLAUDE_PROXY_TEST_LATEST_URL:-}" ]; then
    exit 1
  fi
  printf "%s" "$CLAUDE_PROXY_TEST_LATEST_URL"
  exit 0
fi

if [ -z "$out" ]; then
  exit 1
fi

case "$url" in
  *"/repos/"*"/releases/latest")
    if [ "${CLAUDE_PROXY_TEST_API_FAIL:-}" = "1" ]; then
      exit 22
    fi
    printf "%s" "${CLAUDE_PROXY_TEST_API_JSON:-}" > "$out"
    ;;
  *"/checksums.txt")
    printf "%s" "${CLAUDE_PROXY_TEST_CHECKSUMS:-}" > "$out"
    ;;
  *"/${CLAUDE_PROXY_TEST_ASSET}")
    printf "%s" "${CLAUDE_PROXY_TEST_ASSET_DATA:-}" > "$out"
    ;;
  *)
    exit 22
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub curl: %v", err)
	}
}

func expectedBashConfigPath(home string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, ".bash_profile")
	}
	return filepath.Join(home, ".bashrc")
}
