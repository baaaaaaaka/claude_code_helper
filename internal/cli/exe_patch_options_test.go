package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/config"
)

func TestExePatchOptionsEnabledAndValidate(t *testing.T) {
	requireExePatchEnabled(t)
	opts := exePatchOptions{}
	if opts.enabled() {
		t.Fatalf("expected options to be disabled by default")
	}

	opts.policySettings = true
	opts.enabledFlag = true
	if !opts.enabled() {
		t.Fatalf("expected policySettings to enable options")
	}

	opts = exePatchOptions{
		enabledFlag: true,
		regex1:      "a",
		regex2:      []string{"b"},
		regex3:      []string{"c"},
		replace:     []string{"d"},
	}
	if !opts.customRulesEnabled() {
		t.Fatalf("expected custom rules to be enabled")
	}
	if err := opts.validate(); err != nil {
		t.Fatalf("expected valid options, got %v", err)
	}

	opts = exePatchOptions{
		enabledFlag: true,
		regex2:      []string{"b"},
		regex3:      []string{"c"},
		replace:     []string{"d"},
	}
	if err := opts.validate(); err == nil {
		t.Fatalf("expected missing regex1 error")
	}

	opts = exePatchOptions{
		enabledFlag: true,
		regex1:      "a",
		regex2:      []string{"b"},
		regex3:      []string{"c", "d"},
		replace:     []string{"e"},
	}
	if err := opts.validate(); err == nil {
		t.Fatalf("expected mismatched list length error")
	}
}

func TestNormalizeReplacement(t *testing.T) {
	requireExePatchEnabled(t)
	cases := map[string]string{
		"$1foo":    "${1}foo",
		"$10bar":   "${10}bar",
		"$1":       "$1",
		"$$1":      "$$1",
		"${1}foo":  "${1}foo",
		"plain":    "plain",
		"$1_thing": "${1}_thing",
	}
	for input, want := range cases {
		got := normalizeReplacement(input)
		if got != want {
			t.Fatalf("normalizeReplacement(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestResolveExecutablePath(t *testing.T) {
	requireExePatchEnabled(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")

	if err := os.WriteFile(target, []byte("data"), 0o700); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	resolved, err := resolveExecutablePath(link)
	if err != nil {
		t.Fatalf("resolveExecutablePath error: %v", err)
	}
	expected, err := resolveExecutablePath(target)
	if err != nil {
		t.Fatalf("resolveExecutablePath target error: %v", err)
	}
	if resolved != expected {
		t.Fatalf("expected resolved path %q, got %q", expected, resolved)
	}
}

func TestExePatchOptionsCompileInvalidRegex(t *testing.T) {
	requireExePatchEnabled(t)
	opts := exePatchOptions{
		enabledFlag: true,
		regex1:      "(",
		regex2:      []string{"a"},
		regex3:      []string{"b"},
		replace:     []string{"c"},
	}
	if _, err := opts.compile(); err == nil {
		t.Fatalf("expected compile error for invalid regex1")
	}

	opts = exePatchOptions{
		enabledFlag: true,
		regex1:      "a",
		regex2:      []string{"("},
		regex3:      []string{"b"},
		replace:     []string{"c"},
	}
	if _, err := opts.compile(); err == nil {
		t.Fatalf("expected compile error for invalid regex2")
	}

	opts = exePatchOptions{
		enabledFlag: true,
		regex1:      "a",
		regex2:      []string{"b"},
		regex3:      []string{"("},
		replace:     []string{"c"},
	}
	if _, err := opts.compile(); err == nil {
		t.Fatalf("expected compile error for invalid regex3")
	}
}

func TestMaybePatchExecutable(t *testing.T) {
	requireExePatchEnabled(t)
	dir := t.TempDir()
	name := "dummy"
	if runtime.GOOS == "windows" {
		name = "dummy.exe"
	}
	binPath := filepath.Join(dir, name)
	if err := os.WriteFile(binPath, []byte("foo"), 0o700); err != nil {
		t.Fatalf("write dummy: %v", err)
	}

	t.Setenv("PATH", dir)
	opts := exePatchOptions{
		enabledFlag: true,
		regex1:      "foo",
		regex2:      []string{"foo"},
		regex3:      []string{"foo"},
		replace:     []string{"bar"},
	}

	log := &bytes.Buffer{}
	outcome, err := maybePatchExecutable([]string{name}, opts, filepath.Join(dir, "config.json"), log)
	if err != nil {
		t.Fatalf("maybePatchExecutable error: %v", err)
	}
	if outcome == nil || !outcome.Applied {
		t.Fatalf("expected patch to be applied")
	}
	data, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("read patched: %v", err)
	}
	if string(data) != "bar" {
		t.Fatalf("expected patched data to be %q, got %q", "bar", string(data))
	}
	if outcome.BackupPath == "" {
		t.Fatalf("expected backup path to be set")
	}
	if _, err := os.Stat(outcome.BackupPath); err != nil {
		t.Fatalf("expected backup to exist: %v", err)
	}
}

func TestPatchExecutableWithHistory(t *testing.T) {
	requireExePatchEnabled(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, []byte("foo foo"), 0o700); err != nil {
		t.Fatalf("write executable: %v", err)
	}

	store, err := config.NewPatchHistoryStore(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("NewPatchHistoryStore error: %v", err)
	}

	spec := exePatchSpec{
		match:   regexp.MustCompile("foo"),
		guard:   nil,
		patch:   regexp.MustCompile("foo"),
		replace: []byte("bar"),
		label:   "custom-1",
	}
	log := &bytes.Buffer{}
	outcome, err := patchExecutable(path, []exePatchSpec{spec}, log, false, false, store)
	if err != nil {
		t.Fatalf("patchExecutable error: %v", err)
	}
	if !outcome.Applied || outcome.BackupPath == "" {
		t.Fatalf("expected patch to be applied with backup")
	}
	if _, err := os.Stat(outcome.BackupPath); err != nil {
		t.Fatalf("expected backup to exist: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched: %v", err)
	}
	if string(data) != "bar bar" {
		t.Fatalf("expected patched data, got %q", string(data))
	}

	history, err := store.Load()
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(history.Entries) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history.Entries))
	}

	log.Reset()
	outcome2, err := patchExecutable(path, []exePatchSpec{spec}, log, false, false, store)
	if err != nil {
		t.Fatalf("patchExecutable second run error: %v", err)
	}
	if outcome2.Applied {
		t.Fatalf("expected second run to skip patching")
	}
	if !strings.Contains(log.String(), "already patched") {
		t.Fatalf("expected already patched log, got %q", log.String())
	}
}

func TestBackupExecutableExisting(t *testing.T) {
	requireExePatchEnabled(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, []byte("data"), 0o700); err != nil {
		t.Fatalf("write executable: %v", err)
	}

	existing := filepath.Join(dir, "bin.claude-proxy.bak")
	if err := os.WriteFile(existing, []byte("old"), 0o700); err != nil {
		t.Fatalf("write existing backup: %v", err)
	}

	newBackup, err := backupExecutable(path, 0o700)
	if err != nil {
		t.Fatalf("backupExecutable error: %v", err)
	}
	if newBackup == existing {
		t.Fatalf("expected new backup path, got existing")
	}
	if _, err := os.Stat(newBackup); err != nil {
		t.Fatalf("expected new backup to exist: %v", err)
	}
}

func TestCleanupBackup(t *testing.T) {
	requireExePatchEnabled(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "bin.bak")
	if err := os.WriteFile(path, []byte("data"), 0o700); err != nil {
		t.Fatalf("write backup: %v", err)
	}
	outcome := &patchOutcome{BackupPath: path}
	cleanupBackup(outcome)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected backup to be removed")
	}
}

func TestLogHelpers(t *testing.T) {
	requireExePatchEnabled(t)
	buf := &bytes.Buffer{}
	logDryRun(buf, "path", true)
	logAlreadyPatched(buf, "path")
	logPatchSummary(buf, "path", exePatchStats{
		Label:        "test",
		Segments:     1,
		Eligible:     1,
		Changed:      1,
		Replacements: 1,
	})
	logPatchSummary(buf, "path", exePatchStats{
		Label:    "test",
		Segments: 1,
		Eligible: 1,
	})
	if !strings.Contains(buf.String(), "dry-run") {
		t.Fatalf("expected dry-run log output")
	}
	if !strings.Contains(buf.String(), "already patched") {
		t.Fatalf("expected already patched log output")
	}
	if !strings.Contains(buf.String(), "updated") {
		t.Fatalf("expected updated summary")
	}
	if !strings.Contains(buf.String(), "no byte changes") {
		t.Fatalf("expected no changes summary")
	}
}

func TestPatchSpecsHashDiffers(t *testing.T) {
	requireExePatchEnabled(t)
	specA := exePatchSpec{
		match:   regexp.MustCompile("foo"),
		guard:   regexp.MustCompile("bar"),
		patch:   regexp.MustCompile("foo"),
		replace: []byte("baz"),
		label:   "a",
	}
	specB := exePatchSpec{
		match:   regexp.MustCompile("foo"),
		guard:   regexp.MustCompile("bar"),
		patch:   regexp.MustCompile("foo"),
		replace: []byte("baz"),
		label:   "b",
	}
	hashA := patchSpecsHash([]exePatchSpec{specA})
	hashB := patchSpecsHash([]exePatchSpec{specB})
	if hashA == hashB {
		t.Fatalf("expected different hashes for different labels")
	}
}

func TestFormatPreviewSegment(t *testing.T) {
	requireExePatchEnabled(t)
	short := bytes.Repeat([]byte("a"), previewByteLimit)
	if got := formatPreviewSegment(short); !strings.Contains(got, "aaaa") {
		t.Fatalf("expected preview to include content, got %q", got)
	}

	long := bytes.Repeat([]byte("b"), previewByteLimit+1)
	if got := formatPreviewSegment(long); !strings.Contains(got, "truncated 1 bytes") {
		t.Fatalf("expected truncated preview, got %q", got)
	}
}
