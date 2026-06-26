package cli

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

func TestMaybePatchExecutableBuiltInClaudeUsesMirrorAndPreservesSource(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)
	if runtimeGOOS == "windows" {
		t.Skip("mirror readiness path is covered by Windows-specific tests")
	}

	dir := t.TempDir()
	cacheRoot := filepath.Join(dir, "cache")
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv(claudeProxyHostIDEnv, "yolo-mirror-test-host")
	setStubPath(t, dir)

	path := filepath.Join(dir, "claude")
	original := []byte("function FI(H){if(H===\"policySettings\"){let L=sqA();if(L&&Object.keys(L).length>0)return L}let $=L4(H);if(!$)return null;let{settings:A}=DmA($);return A}")
	if err := os.WriteFile(path, original, 0o700); err != nil {
		t.Fatalf("write original claude: %v", err)
	}
	runClaudeProbeFn = func(path string, arg string) (string, error) {
		return "Claude Code 1.2.3", nil
	}

	configPath := filepath.Join(dir, "config.json")
	outcome, err := maybePatchExecutable(yoloClaudeArgs("claude"), exePatchOptions{
		enabledFlag:    true,
		policySettings: true,
	}, configPath, io.Discard)
	if err != nil {
		t.Fatalf("maybePatchExecutable error: %v", err)
	}
	if outcome == nil || !outcome.BuiltInClaudePatchActive {
		t.Fatalf("expected active built-in mirror patch, got %#v", outcome)
	}
	if config.PathsEqual(outcome.TargetPath, path) {
		t.Fatalf("expected mirror target, got source path %q", outcome.TargetPath)
	}
	if len(outcome.LaunchArgsPrefix) != 1 || !config.PathsEqual(outcome.LaunchArgsPrefix[0], outcome.TargetPath) {
		t.Fatalf("expected mirror launch prefix, got %#v", outcome.LaunchArgsPrefix)
	}
	if outcome.MirrorLeasePath == "" {
		t.Fatalf("expected mirror lease path")
	}
	if _, err := os.Stat(outcome.MirrorLeasePath); err != nil {
		t.Fatalf("expected mirror lease to exist: %v", err)
	}
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	if string(source) != string(original) {
		t.Fatalf("expected source executable to stay unchanged")
	}
	mirror, err := os.ReadFile(outcome.TargetPath)
	if err != nil {
		t.Fatalf("read mirror: %v", err)
	}
	if string(mirror) == string(original) {
		t.Fatalf("expected mirror executable to be patched")
	}
	if _, err := os.Stat(originalBackupPath(path)); !os.IsNotExist(err) {
		t.Fatalf("expected no source backup, got err=%v", err)
	}

	firstMirror := outcome.TargetPath
	firstLease := outcome.MirrorLeasePath
	releasePatchOutcomeMirrorLease(outcome)
	if _, err := os.Stat(firstLease); outcome.MirrorLeasePath != "" || !os.IsNotExist(err) {
		t.Fatalf("expected release to clear lease, lease=%q err=%v", outcome.MirrorLeasePath, err)
	}

	second, err := maybePatchExecutable(yoloClaudeArgs("claude"), exePatchOptions{
		enabledFlag:    true,
		policySettings: true,
	}, configPath, io.Discard)
	if err != nil {
		t.Fatalf("second maybePatchExecutable error: %v", err)
	}
	if second == nil || !second.AlreadyPatched {
		t.Fatalf("expected second launch to reuse mirror, got %#v", second)
	}
	if !config.PathsEqual(second.TargetPath, firstMirror) {
		t.Fatalf("expected reused mirror %q, got %q", firstMirror, second.TargetPath)
	}
	releasePatchOutcomeMirrorLease(second)
}

func TestCleanupYoloPatchMirrorsKeepsActiveLease(t *testing.T) {
	dir := t.TempDir()
	cacheRoot := filepath.Join(dir, "cache")
	now := time.Now()

	for i, name := range []string{"yolo-a", "yolo-b", "yolo-c", "yolo-d"} {
		mirrorDir := filepath.Join(cacheRoot, name)
		if err := os.MkdirAll(mirrorDir, 0o755); err != nil {
			t.Fatalf("mkdir mirror dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(mirrorDir, "claude"), []byte(name), 0o700); err != nil {
			t.Fatalf("write mirror: %v", err)
		}
		ts := now.Add(time.Duration(i-10) * time.Minute)
		if err := os.Chtimes(mirrorDir, ts, ts); err != nil {
			t.Fatalf("chtimes mirror dir: %v", err)
		}
	}

	activeDir := filepath.Join(cacheRoot, "yolo-a")
	lease := yoloPatchMirrorLease{
		PID:           os.Getpid(),
		StartIdentity: processStartIdentity(os.Getpid()),
		CreatedAt:     now,
	}
	if err := writeYoloPatchMirrorJSON(filepath.Join(activeDir, "active.test.json"), lease); err != nil {
		t.Fatalf("write active lease: %v", err)
	}

	if err := cleanupYoloPatchMirrors(cacheRoot, "yolo-d"); err != nil {
		t.Fatalf("cleanupYoloPatchMirrors error: %v", err)
	}
	if _, err := os.Stat(activeDir); err != nil {
		t.Fatalf("expected active mirror to be kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheRoot, "yolo-d")); err != nil {
		t.Fatalf("expected current mirror to be kept: %v", err)
	}
}

func TestYoloPatchMirrorLeaseActiveKeepsLivePIDWithoutIdentityPastTTL(t *testing.T) {
	dir := t.TempDir()
	leasePath := filepath.Join(dir, "active.test.json")
	now := time.Now()
	lease := yoloPatchMirrorLease{
		PID:       os.Getpid(),
		CreatedAt: now.Add(-2 * yoloPatchMirrorCorruptLeaseTTL),
	}
	if err := writeYoloPatchMirrorJSON(leasePath, lease); err != nil {
		t.Fatalf("write lease: %v", err)
	}
	if !yoloPatchMirrorLeaseActive(leasePath, now) {
		t.Fatalf("expected live PID lease without start identity to stay active")
	}
}
