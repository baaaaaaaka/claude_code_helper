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

func TestInstallShChecksumMismatch(t *testing.T) {
	if _, err := exec.LookPath("sha256sum"); err != nil {
		if _, err := exec.LookPath("shasum"); err != nil {
			t.Skip("no checksum tool available")
		}
	}

	run := newInstallShRun(t, false, false)
	run.env = overrideEnv(run.env, "CLAUDE_PROXY_TEST_CHECKSUMS", strings.Repeat("0", 64)+"  "+run.asset+"\n")

	cmd := exec.Command("sh", run.scriptPath)
	cmd.Dir = run.repoRoot
	cmd.Env = run.env
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected checksum mismatch error")
	}
	if !strings.Contains(string(output), "INSTALL FAILED") {
		t.Fatalf("expected install failure banner, got %s", string(output))
	}
	if !strings.Contains(string(output), "Checksum mismatch") {
		t.Fatalf("expected checksum mismatch output, got %s", string(output))
	}
}

func TestInstallShUsesProfileWhenShellMissing(t *testing.T) {
	run := newInstallShRun(t, false, false)
	run.env = overrideEnv(run.env, "SHELL", "")

	cmd := exec.Command("sh", run.scriptPath)
	cmd.Dir = run.repoRoot
	cmd.Env = run.env
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install.sh failed: %v\n%s", err, string(output))
	}

	profilePath := filepath.Join(run.homeDir, ".profile")
	contents, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	text := string(contents)
	claudeBinDir := expectedClaudeBinDir(run.homeDir)
	if !hasPathMarker(text, expectedInstallDir(t, run.installDir)) {
		t.Fatalf("missing install dir PATH update in profile")
	}
	if !hasPathMarker(text, claudeBinDir) {
		t.Fatalf("missing claude PATH update in profile")
	}
	if !strings.Contains(text, "alias clp='claude-proxy'") {
		t.Fatalf("missing clp alias in profile")
	}
}

func TestInstallShShellSetupFailureStillReportsSuccess(t *testing.T) {
	run := newInstallShRun(t, false, false)
	profilePath := filepath.Join(run.homeDir, ".profile")
	if err := os.WriteFile(profilePath, []byte("# locked profile\n"), 0o400); err != nil {
		t.Fatalf("write readonly profile: %v", err)
	}

	cmd := exec.Command("sh", run.scriptPath)
	cmd.Dir = run.repoRoot
	cmd.Env = run.env
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install.sh failed unexpectedly: %v\n%s", err, string(output))
	}
	if !strings.Contains(string(output), "INSTALL SUCCESS") {
		t.Fatalf("expected install success banner, got %s", string(output))
	}
	if strings.Contains(string(output), "INSTALL FAILED") {
		t.Fatalf("did not expect install failure banner, got %s", string(output))
	}
	if !strings.Contains(string(output), "Attention: automatic shell setup was incomplete.") {
		t.Fatalf("expected shell setup warning, got %s", string(output))
	}
	if !strings.Contains(string(output), "Could not update shell config: "+profilePath) {
		t.Fatalf("expected profile warning, got %s", string(output))
	}
	if !strings.Contains(string(output), "To use 'clp', add \""+expectedInstallDir(t, run.installDir)+"\" to PATH manually, then open a new shell.") {
		t.Fatalf("expected manual PATH guidance, got %s", string(output))
	}
	if _, err := os.Stat(filepath.Join(run.installDir, "clp")); err != nil {
		t.Fatalf("expected clp to be installed: %v", err)
	}
}

func TestInstallShUsesZshConfigs(t *testing.T) {
	run := newInstallShRun(t, false, false)
	run.env = overrideEnv(run.env, "SHELL", "/bin/zsh")
	runInstallShRun(t, run)

	claudeBinDir := expectedClaudeBinDir(run.homeDir)
	for _, path := range []string{
		filepath.Join(run.homeDir, ".zprofile"),
		filepath.Join(run.homeDir, ".zshrc"),
	} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(contents)
		if !hasPathMarker(text, expectedInstallDir(t, run.installDir)) {
			t.Fatalf("missing install dir PATH update in %s", path)
		}
		if !hasPathMarker(text, claudeBinDir) {
			t.Fatalf("missing claude PATH update in %s", path)
		}
	}

	zshrc, err := os.ReadFile(filepath.Join(run.homeDir, ".zshrc"))
	if err != nil {
		t.Fatalf("read .zshrc: %v", err)
	}
	if !strings.Contains(string(zshrc), "alias clp='claude-proxy'") {
		t.Fatalf("missing clp alias in .zshrc")
	}
}

func TestInstallShUsesFishConfig(t *testing.T) {
	run := newInstallShRun(t, false, false)
	run.env = overrideEnv(run.env, "SHELL", "/usr/bin/fish")
	runInstallShRun(t, run)

	configPath := filepath.Join(run.homeDir, ".config", "fish", "config.fish")
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read fish config: %v", err)
	}
	text := string(contents)
	claudeBinDir := expectedClaudeBinDir(run.homeDir)
	if !hasPathMarker(text, expectedInstallDir(t, run.installDir)) {
		t.Fatalf("missing install dir PATH update in fish config")
	}
	if !hasPathMarker(text, claudeBinDir) {
		t.Fatalf("missing claude PATH update in fish config")
	}
	if !strings.Contains(text, "alias clp \"claude-proxy\"") {
		t.Fatalf("missing clp alias in fish config")
	}
}

func TestInstallShUsesCshConfigs(t *testing.T) {
	run := newInstallShRun(t, false, false)
	run.env = overrideEnv(run.env, "SHELL", "/bin/csh")
	runInstallShRun(t, run)

	claudeBinDir := expectedClaudeBinDir(run.homeDir)
	for _, path := range []string{
		filepath.Join(run.homeDir, ".cshrc"),
		filepath.Join(run.homeDir, ".login"),
	} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(contents)
		if !hasPathMarker(text, expectedInstallDir(t, run.installDir)) {
			t.Fatalf("missing install dir PATH update in %s", path)
		}
		if !hasPathMarker(text, claudeBinDir) {
			t.Fatalf("missing claude PATH update in %s", path)
		}
	}

	cshrc, err := os.ReadFile(filepath.Join(run.homeDir, ".cshrc"))
	if err != nil {
		t.Fatalf("read .cshrc: %v", err)
	}
	if !strings.Contains(string(cshrc), "alias clp claude-proxy") {
		t.Fatalf("missing clp alias in .cshrc")
	}
}

func TestInstallShUsesTcshConfigs(t *testing.T) {
	run := newInstallShRun(t, false, false)
	run.env = overrideEnv(run.env, "SHELL", "/bin/tcsh")
	runInstallShRun(t, run)

	claudeBinDir := expectedClaudeBinDir(run.homeDir)
	for _, path := range []string{
		filepath.Join(run.homeDir, ".cshrc"),
		filepath.Join(run.homeDir, ".login"),
	} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(contents)
		if !hasPathMarker(text, expectedInstallDir(t, run.installDir)) {
			t.Fatalf("missing install dir PATH update in %s", path)
		}
		if !hasPathMarker(text, claudeBinDir) {
			t.Fatalf("missing claude PATH update in %s", path)
		}
	}

	if _, err := os.Stat(filepath.Join(run.homeDir, ".tcshrc")); !os.IsNotExist(err) {
		t.Fatalf("expected .tcshrc to remain absent, got err=%v", err)
	}
}

func TestInstallShUpdatesExistingTcshrc(t *testing.T) {
	run := newInstallShRun(t, false, false)
	run.env = overrideEnv(run.env, "SHELL", "/bin/tcsh")
	tcshrcPath := filepath.Join(run.homeDir, ".tcshrc")
	if err := os.WriteFile(tcshrcPath, []byte("# existing tcshrc\n"), 0o644); err != nil {
		t.Fatalf("write .tcshrc: %v", err)
	}

	runInstallShRun(t, run)

	tcshrc, err := os.ReadFile(tcshrcPath)
	if err != nil {
		t.Fatalf("read .tcshrc: %v", err)
	}
	text := string(tcshrc)
	if !hasPathMarker(text, expectedInstallDir(t, run.installDir)) {
		t.Fatalf("missing install dir PATH update in .tcshrc")
	}
	if !hasPathMarker(text, expectedClaudeBinDir(run.homeDir)) {
		t.Fatalf("missing claude PATH update in .tcshrc")
	}
	if !strings.Contains(text, "alias clp claude-proxy") {
		t.Fatalf("missing clp alias in .tcshrc")
	}
}

func TestInstallShBashConfigSources(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	run := newInstallShRun(t, false, false)
	run.env = overrideEnv(run.env, "SHELL", "/bin/bash")
	runInstallShRun(t, run)
	installDir := expectedInstallDir(t, run.installDir)

	script := fmt.Sprintf(`
. "%s"
case ":$PATH:" in
  *:"%s":*) ;;
  *) exit 11 ;;
esac
case ":$PATH:" in
  *:"%s":*) ;;
  *) exit 12 ;;
esac
alias clp >/dev/null 2>&1 || exit 13
`, expectedBashConfigPath(run.homeDir), installDir, expectedClaudeBinDir(run.homeDir))
	runShellCheck(t, "bash", []string{"-lc", script}, run.env)
}

func TestInstallShZshConfigSources(t *testing.T) {
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not available")
	}

	run := newInstallShRun(t, false, false)
	run.env = overrideEnv(run.env, "SHELL", "/bin/zsh")
	runInstallShRun(t, run)
	installDir := expectedInstallDir(t, run.installDir)

	script := fmt.Sprintf(`
source "%s"
source "%s"
case ":$PATH:" in
  *:"%s":*) ;;
  *) exit 11 ;;
esac
case ":$PATH:" in
  *:"%s":*) ;;
  *) exit 12 ;;
esac
alias clp >/dev/null 2>&1 || exit 13
`, filepath.Join(run.homeDir, ".zprofile"), filepath.Join(run.homeDir, ".zshrc"), installDir, expectedClaudeBinDir(run.homeDir))
	runShellCheck(t, "zsh", []string{"-lc", script}, run.env)
}

func TestInstallShFishConfigSources(t *testing.T) {
	if _, err := exec.LookPath("fish"); err != nil {
		t.Skip("fish not available")
	}

	run := newInstallShRun(t, false, false)
	run.env = overrideEnv(run.env, "SHELL", "/usr/bin/fish")
	runInstallShRun(t, run)
	installDir := expectedInstallDir(t, run.installDir)

	script := fmt.Sprintf(`
source "%s"
contains -- "%s" $PATH; or exit 11
contains -- "%s" $PATH; or exit 12
functions -q clp; or exit 13
`, filepath.Join(run.homeDir, ".config", "fish", "config.fish"), installDir, expectedClaudeBinDir(run.homeDir))
	runShellCheck(t, "fish", []string{"-c", script}, run.env)
}

func TestInstallShCshConfigSources(t *testing.T) {
	shellPath := "csh"
	if _, err := exec.LookPath(shellPath); err != nil {
		t.Skip("csh not available")
	}

	run := newInstallShRun(t, false, false)
	run.env = overrideEnv(run.env, "SHELL", "/bin/csh")
	runInstallShRun(t, run)
	installDir := expectedInstallDir(t, run.installDir)

	script := fmt.Sprintf(`
source "%s"
source "%s"
if (":$PATH:" !~ "*:%s:*") exit 11
if (":$PATH:" !~ "*:%s:*") exit 12
alias clp >& /dev/null
if ($status != 0) exit 13
`, filepath.Join(run.homeDir, ".login"), filepath.Join(run.homeDir, ".cshrc"), installDir, expectedClaudeBinDir(run.homeDir))
	runShellCheck(t, shellPath, []string{"-c", script}, run.env)
}

func TestInstallShTcshConfigSources(t *testing.T) {
	shellPath := "tcsh"
	if _, err := exec.LookPath(shellPath); err != nil {
		t.Skip("tcsh not available")
	}

	run := newInstallShRun(t, false, false)
	run.env = overrideEnv(run.env, "SHELL", "/bin/tcsh")
	if err := os.WriteFile(filepath.Join(run.homeDir, ".tcshrc"), []byte("# existing tcshrc\n"), 0o644); err != nil {
		t.Fatalf("write .tcshrc: %v", err)
	}
	runInstallShRun(t, run)
	installDir := expectedInstallDir(t, run.installDir)

	script := fmt.Sprintf(`
source "%s"
source "%s"
source "%s"
if (":$PATH:" !~ "*:%s:*") exit 11
if (":$PATH:" !~ "*:%s:*") exit 12
alias clp >& /dev/null
if ($status != 0) exit 13
`, filepath.Join(run.homeDir, ".login"), filepath.Join(run.homeDir, ".cshrc"), filepath.Join(run.homeDir, ".tcshrc"), installDir, expectedClaudeBinDir(run.homeDir))
	runShellCheck(t, shellPath, []string{"-c", script}, run.env)
}

func TestInstallShRejectsUnknownArg(t *testing.T) {
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	scriptPath := filepath.Join(repoRoot, "install.sh")
	cmd := exec.Command("sh", scriptPath, "--unknown")
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected unknown arg error")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected ExitError, got %T", err)
	}
	if exitErr.ExitCode() != 2 {
		t.Fatalf("expected exit code 2, got %d\n%s", exitErr.ExitCode(), string(output))
	}
	if !strings.Contains(string(output), "INSTALL FAILED") {
		t.Fatalf("expected install failure banner, got %s", string(output))
	}
}

func TestInstallShHelpDoesNotPrintStatusBanner(t *testing.T) {
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	scriptPath := filepath.Join(repoRoot, "install.sh")
	cmd := exec.Command("sh", scriptPath, "--help")
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("help failed: %v\n%s", err, string(output))
	}
	if strings.Contains(string(output), "INSTALL SUCCESS") || strings.Contains(string(output), "INSTALL FAILED") {
		t.Fatalf("did not expect status banner in help output, got %s", string(output))
	}
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
		pathValue = expectedInstallDir(t, installDir) + string(os.PathListSeparator) + pathValue
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
	if !strings.Contains(string(output), "INSTALL SUCCESS") {
		t.Fatalf("expected install success banner, got %s", string(output))
	}
	if strings.Contains(string(output), "sed: can't read") {
		t.Fatalf("unexpected redirect fallback noise in output, got %s", string(output))
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
	if !strings.Contains(string(output), "Installed: "+installed) {
		t.Fatalf("expected installed binary path in output, got %s", string(output))
	}
	if !strings.Contains(string(output), "Installed: "+clpPath) {
		t.Fatalf("expected clp path in output, got %s", string(output))
	}
	if !strings.Contains(string(output), "Shell setup checked for PATH entries and alias 'clp'.") {
		t.Fatalf("expected shell setup status in output, got %s", string(output))
	}
	if !strings.Contains(string(output), "If 'clp' is not found in this shell, open a new shell.") {
		t.Fatalf("expected shell hint in output, got %s", string(output))
	}

	configPath := expectedBashConfigPath(homeDir)
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read shell config: %v", err)
	}
	text := string(contents)
	claudeBinDir := expectedClaudeBinDir(homeDir)
	installDirResolved := expectedInstallDir(t, installDir)
	if pathAlreadySet {
		if hasPathMarker(text, installDirResolved) {
			t.Fatalf("unexpected install dir PATH update in shell config")
		}
	} else {
		if !hasPathMarker(text, installDirResolved) {
			t.Fatalf("missing install dir PATH update in shell config")
		}
	}
	if !hasPathMarker(text, claudeBinDir) {
		t.Fatalf("missing claude PATH update in shell config")
	}
	if !strings.Contains(text, "alias clp='claude-proxy'") {
		t.Fatalf("missing clp alias in shell config")
	}
}

func runInstallShRun(t *testing.T, run installShRun) {
	t.Helper()
	cmd := exec.Command("sh", run.scriptPath)
	cmd.Dir = run.repoRoot
	cmd.Env = run.env
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install.sh failed: %v\n%s", err, string(output))
	}
}

func runShellCheck(t *testing.T, shell string, args []string, env []string) {
	t.Helper()
	cmd := exec.Command(shell, args...)
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s check failed: %v\n%s", shell, err, string(output))
	}
}

type installShRun struct {
	repoRoot   string
	scriptPath string
	homeDir    string
	installDir string
	asset      string
	assetData  []byte
	env        []string
}

func newInstallShRun(t *testing.T, apiFail bool, pathAlreadySet bool) installShRun {
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
		pathValue = expectedInstallDir(t, installDir) + string(os.PathListSeparator) + pathValue
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

	return installShRun{
		repoRoot:   repoRoot,
		scriptPath: scriptPath,
		homeDir:    homeDir,
		installDir: installDir,
		asset:      asset,
		assetData:  assetData,
		env:        env,
	}
}

func overrideEnv(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		k, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if k == key {
			continue
		}
		out = append(out, kv)
	}
	return append(out, key+"="+value)
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

func expectedClaudeBinDir(home string) string {
	return filepath.Join(home, ".local", "bin")
}

func expectedInstallDir(t *testing.T, dir string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err == nil && resolved != "" {
		return resolved
	}
	abs, err := filepath.Abs(dir)
	if err == nil && abs != "" {
		return abs
	}
	return dir
}

func hasPathMarker(text, dir string) bool {
	return strings.Contains(text, "# claude-proxy PATH "+dir)
}
