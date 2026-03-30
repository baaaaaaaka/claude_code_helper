package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
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
		glibcCompat: true,
	}
	if !opts.glibcCompatConfigured() {
		t.Fatalf("expected glibc compat to be configured when enabled")
	}
	if !opts.enabled() {
		t.Fatalf("expected glibc compat to enable options")
	}
	opts.glibcCompatRoot = "/tmp/glibc"
	if !opts.glibcCompatConfigured() {
		t.Fatalf("expected glibc compat to be configured with root path")
	}
	if !opts.enabled() {
		t.Fatalf("expected glibc compat to enable options when root is configured")
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
	outcome, err := patchExecutable(path, []exePatchSpec{spec}, log, false, false, store, "v1.0.0")
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
	outcome2, err := patchExecutable(path, []exePatchSpec{spec}, log, false, false, store, "v1.0.0")
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

func TestPatchExecutableProxyChangeUsesBackup(t *testing.T) {
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
	outcome, err := patchExecutable(path, []exePatchSpec{spec}, log, false, false, store, "v1.0.0")
	if err != nil {
		t.Fatalf("patchExecutable error: %v", err)
	}
	if !outcome.Applied {
		t.Fatalf("expected patch to be applied")
	}

	// Binary is now "bar bar" (patched). Proxy version changes but binary
	// stays the same — backup is still valid and should be used as source.
	log.Reset()
	outcome2, err := patchExecutable(path, []exePatchSpec{spec}, log, false, false, store, "v2.0.0")
	if err != nil {
		t.Fatalf("patchExecutable proxy update error: %v", err)
	}
	// The patched result from the backup equals the on-disk binary, so no
	// write is needed. Applied should be false (binary already correct).
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched: %v", err)
	}
	if string(data) != "bar bar" {
		t.Fatalf("expected patched data, got %q", string(data))
	}
	if _, err := os.Stat(originalBackupPath(path)); err != nil {
		t.Fatalf("expected backup to remain: %v", err)
	}
	history, err := store.Load()
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	entry, ok := history.Find(path, patchSpecsHash([]exePatchSpec{spec}))
	if !ok {
		t.Fatalf("expected history entry")
	}
	if entry.ProxyVersion != "v2.0.0" {
		t.Fatalf("expected proxy version to update, got %q", entry.ProxyVersion)
	}
	_ = outcome2
}

func TestPatchExecutableStaleBackupDiscarded(t *testing.T) {
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
	outcome, err := patchExecutable(path, []exePatchSpec{spec}, log, false, false, store, "v1.0.0")
	if err != nil {
		t.Fatalf("patchExecutable error: %v", err)
	}
	if !outcome.Applied {
		t.Fatalf("expected patch to be applied")
	}

	// Simulate external update (e.g., Claude auto-update) — binary is
	// replaced with new content that still contains patchable patterns.
	if err := os.WriteFile(path, []byte("foo baz"), 0o700); err != nil {
		t.Fatalf("replace executable: %v", err)
	}

	log.Reset()
	outcome2, err := patchExecutable(path, []exePatchSpec{spec}, log, false, false, store, "v2.0.0")
	if err != nil {
		t.Fatalf("patchExecutable error: %v", err)
	}
	if !outcome2.Applied {
		t.Fatalf("expected patch to be applied to new binary")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched: %v", err)
	}
	// Should patch the NEW binary, not restore from stale backup.
	if string(data) != "bar baz" {
		t.Fatalf("expected new binary to be patched, got %q", string(data))
	}
	if !strings.Contains(log.String(), "stale") {
		t.Fatalf("expected stale backup log, got %q", log.String())
	}
}

func TestPatchExecutableProxyChangeWithoutBackupDoesNotCreatePatchedBackup(t *testing.T) {
	requireExePatchEnabled(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, []byte("bar bar"), 0o700); err != nil {
		t.Fatalf("write executable: %v", err)
	}

	store, err := config.NewPatchHistoryStore(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("NewPatchHistoryStore error: %v", err)
	}

	spec := exePatchSpec{
		match:   regexp.MustCompile("bar"),
		guard:   nil,
		patch:   regexp.MustCompile("bar"),
		replace: []byte("bar"),
		label:   "custom-1",
	}
	specsHash := patchSpecsHash([]exePatchSpec{spec})
	currentHash := hashBytes([]byte("bar bar"))
	if err := store.Update(func(h *config.PatchHistory) error {
		h.Upsert(config.PatchHistoryEntry{
			Path:          path,
			SpecsSHA256:   specsHash,
			PatchedSHA256: currentHash,
			ProxyVersion:  "v1.0.0",
		})
		return nil
	}); err != nil {
		t.Fatalf("seed history error: %v", err)
	}

	log := &bytes.Buffer{}
	outcome, err := patchExecutable(path, []exePatchSpec{spec}, log, false, false, store, "v2.0.0")
	if err != nil {
		t.Fatalf("patchExecutable error: %v", err)
	}
	if outcome.Applied {
		t.Fatalf("expected no rewrite when already patched")
	}
	if _, err := os.Stat(originalBackupPath(path)); !os.IsNotExist(err) {
		t.Fatalf("expected no backup to be created from already-patched bytes, got err=%v", err)
	}
	history, err := store.Load()
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	entry, ok := history.Find(path, specsHash)
	if !ok {
		t.Fatalf("expected history entry")
	}
	if entry.ProxyVersion != "v2.0.0" {
		t.Fatalf("expected proxy version to update, got %q", entry.ProxyVersion)
	}
}

func TestPatchExecutableHistoryLoadFailure(t *testing.T) {
	requireExePatchEnabled(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, []byte("foo"), 0o700); err != nil {
		t.Fatalf("write executable: %v", err)
	}

	store, err := config.NewPatchHistoryStore(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("NewPatchHistoryStore error: %v", err)
	}
	historyPath, err := config.PatchHistoryPath(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("PatchHistoryPath error: %v", err)
	}
	if err := os.WriteFile(historyPath, []byte("{invalid json"), 0o600); err != nil {
		t.Fatalf("write invalid history: %v", err)
	}

	spec := exePatchSpec{
		match:   regexp.MustCompile("foo"),
		guard:   nil,
		patch:   regexp.MustCompile("foo"),
		replace: []byte("bar"),
		label:   "custom-1",
	}
	log := &bytes.Buffer{}
	if _, err := patchExecutable(path, []exePatchSpec{spec}, log, false, false, store, "v1.0.0"); err != nil {
		t.Fatalf("patchExecutable error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched: %v", err)
	}
	if string(data) != "bar" {
		t.Fatalf("expected patched data, got %q", string(data))
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
	if newBackup != existing {
		t.Fatalf("expected existing backup path, got %q", newBackup)
	}
	if _, err := os.Stat(existing); err != nil {
		t.Fatalf("expected new backup to exist: %v", err)
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

func TestPatchSpecsHashChangesWithApplyID(t *testing.T) {
	requireExePatchEnabled(t)
	makeSpec := func(id string) exePatchSpec {
		return exePatchSpec{
			label:   "same-label",
			applyID: id,
			apply: func(data []byte, _ io.Writer, _ bool) ([]byte, exePatchStats, error) {
				return data, exePatchStats{}, nil
			},
			fixedLength: true,
		}
	}
	hashV1 := patchSpecsHash([]exePatchSpec{makeSpec("root-bypass-guard-v1")})
	hashV2 := patchSpecsHash([]exePatchSpec{makeSpec("root-bypass-guard-v2")})
	if hashV1 == hashV2 {
		t.Fatalf("expected different specsHash when applyID changes")
	}
}

func TestPatchExecutablePopulatesPatchStats(t *testing.T) {
	requireExePatchEnabled(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, []byte("foo baz"), 0o700); err != nil {
		t.Fatalf("write executable: %v", err)
	}

	store, err := config.NewPatchHistoryStore(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("NewPatchHistoryStore error: %v", err)
	}

	// Two built-in-style patches: one matches, one doesn't.
	specs := []exePatchSpec{
		{
			label:   "match-spec",
			applyID: "match-v1",
			apply: func(data []byte, _ io.Writer, _ bool) ([]byte, exePatchStats, error) {
				out := bytes.Replace(data, []byte("foo"), []byte("bar"), 1)
				return out, exePatchStats{Label: "match-spec", Segments: 1, Eligible: 1, Changed: 1, Replacements: 1}, nil
			},
		},
		{
			label:   "miss-spec",
			applyID: "miss-v1",
			apply: func(data []byte, _ io.Writer, _ bool) ([]byte, exePatchStats, error) {
				return data, exePatchStats{Label: "miss-spec"}, nil
			},
		},
	}

	log := &bytes.Buffer{}
	outcome, err := patchExecutable(path, specs, log, false, false, store, "v1.0.0")
	if err != nil {
		t.Fatalf("patchExecutable error: %v", err)
	}
	if !outcome.Applied {
		t.Fatalf("expected patch to be applied")
	}
	if len(outcome.PatchStats) != 2 {
		t.Fatalf("expected 2 PatchStats entries, got %d", len(outcome.PatchStats))
	}

	// First spec matched.
	if outcome.PatchStats[0].Label != "match-spec" || outcome.PatchStats[0].Changed == 0 {
		t.Fatalf("expected match-spec to have changes: %+v", outcome.PatchStats[0])
	}
	// Second spec had zero matches — mirrors the root-bypass-guard failure scenario.
	if outcome.PatchStats[1].Label != "miss-spec" || outcome.PatchStats[1].Eligible != 0 {
		t.Fatalf("expected miss-spec to have no matches: %+v", outcome.PatchStats[1])
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

func TestPatchExecutableStaleBackupNoHistory(t *testing.T) {
	// When there is no history, the backup cannot be determined stale.
	// The backup should be used as the patch source.
	requireExePatchEnabled(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	original := []byte("foo foo")
	if err := os.WriteFile(path, original, 0o700); err != nil {
		t.Fatalf("write: %v", err)
	}

	spec := exePatchSpec{
		match:   regexp.MustCompile("foo"),
		patch:   regexp.MustCompile("foo"),
		replace: []byte("bar"),
		label:   "test",
	}

	// Patch once without history store to create a backup.
	log := &bytes.Buffer{}
	outcome, err := patchExecutable(path, []exePatchSpec{spec}, log, false, false, nil, "v1")
	if err != nil {
		t.Fatalf("first patch: %v", err)
	}
	if !outcome.Applied {
		t.Fatalf("expected first patch to be applied")
	}

	// Replace binary externally (simulating auto-update).
	if err := os.WriteFile(path, []byte("qux qux"), 0o700); err != nil {
		t.Fatalf("replace: %v", err)
	}

	// Patch again WITHOUT history store — cannot determine staleness.
	log.Reset()
	_, err = patchExecutable(path, []exePatchSpec{spec}, log, false, false, nil, "v2")
	if err != nil {
		t.Fatalf("second patch: %v", err)
	}
	// The backup is used (not discarded) because there's no history to
	// compare against. The patched output from backup overwrites the binary.
	data, _ := os.ReadFile(path)
	if string(data) != "bar bar" {
		t.Fatalf("expected backup-based patch result, got %q", data)
	}
}

func TestPatchExecutableStaleBackupNoPatchableContent(t *testing.T) {
	// Stale backup with a new binary that has no patchable content.
	// The backup should be discarded; patching fails because the new
	// binary has no matches (which is the correct behavior — we don't
	// patch from a stale backup).
	requireExePatchEnabled(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, []byte("foo foo"), 0o700); err != nil {
		t.Fatalf("write: %v", err)
	}

	store, err := config.NewPatchHistoryStore(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	spec := exePatchSpec{
		match:   regexp.MustCompile("foo"),
		patch:   regexp.MustCompile("foo"),
		replace: []byte("bar"),
		label:   "test",
	}

	// Patch once with history.
	log := &bytes.Buffer{}
	if _, err := patchExecutable(path, []exePatchSpec{spec}, log, false, false, store, "v1"); err != nil {
		t.Fatalf("first patch: %v", err)
	}

	// Replace binary with content that doesn't match the pattern.
	if err := os.WriteFile(path, []byte("qux qux"), 0o700); err != nil {
		t.Fatalf("replace: %v", err)
	}

	log.Reset()
	_, err = patchExecutable(path, []exePatchSpec{spec}, log, false, false, store, "v2")
	// Patching returns an error because the new binary has no matches.
	// This is correct — we must not fall back to the stale backup.
	if err == nil {
		t.Fatalf("expected error from patching (no matches in new binary)")
	}
	if !strings.Contains(err.Error(), "no matches") {
		t.Fatalf("expected 'no matches' error, got: %v", err)
	}
	// Binary should remain unchanged (not overwritten from stale backup).
	data, _ := os.ReadFile(path)
	if string(data) != "qux qux" {
		t.Fatalf("expected new binary to be untouched, got %q", data)
	}
	if !strings.Contains(log.String(), "stale") {
		t.Fatalf("expected stale backup log, got %q", log.String())
	}
}

func TestPatchExecutableAlreadyPatchedSkips(t *testing.T) {
	// After patching, a second run with the same version should skip.
	requireExePatchEnabled(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, []byte("foo foo"), 0o700); err != nil {
		t.Fatalf("write: %v", err)
	}

	store, err := config.NewPatchHistoryStore(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	spec := exePatchSpec{
		match:   regexp.MustCompile("foo"),
		patch:   regexp.MustCompile("foo"),
		replace: []byte("bar"),
		label:   "test",
	}

	log := &bytes.Buffer{}
	if _, err := patchExecutable(path, []exePatchSpec{spec}, log, false, false, store, "v1"); err != nil {
		t.Fatalf("first patch: %v", err)
	}

	log.Reset()
	outcome, err := patchExecutable(path, []exePatchSpec{spec}, log, false, false, store, "v1")
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !outcome.AlreadyPatched {
		t.Fatalf("expected AlreadyPatched=true on second run")
	}
	if runtimeGOOS != "windows" && !outcome.Verified {
		t.Fatalf("expected non-Windows already-patched binary to remain verified")
	}
	if !strings.Contains(log.String(), "already patched") {
		t.Fatalf("expected 'already patched' log, got %q", log.String())
	}
}

func TestPatchExecutableAlreadyPatchedWithoutHistoryDoesNotCreateBackupOnWindows(t *testing.T) {
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	runtimeGOOS = "windows"

	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, []byte("bar bar"), 0o700); err != nil {
		t.Fatalf("write: %v", err)
	}

	store, err := config.NewPatchHistoryStore(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	spec := exePatchSpec{
		match:   regexp.MustCompile("bar"),
		patch:   regexp.MustCompile("bar"),
		replace: []byte("bar"),
		label:   "already-patched",
	}

	log := &bytes.Buffer{}
	outcome, err := patchExecutable(path, []exePatchSpec{spec}, log, false, false, store, "v1")
	if err != nil {
		t.Fatalf("patchExecutable: %v", err)
	}
	if !outcome.AlreadyPatched {
		t.Fatalf("expected AlreadyPatched=true, got %#v", outcome)
	}
	if outcome.BackupPath != "" {
		t.Fatalf("expected no backup path for already-patched binary, got %q", outcome.BackupPath)
	}
	if outcome.NeedsVerification {
		t.Fatalf("expected no readiness requirement without original backup, got %#v", outcome)
	}
	if _, err := os.Stat(originalBackupPath(path)); !os.IsNotExist(err) {
		t.Fatalf("expected no backup file, got err=%v", err)
	}
}

func TestPatchExecutableNoRealChangeLogsCorrectly(t *testing.T) {
	// When a backup exists and the binary is already patched (but
	// IsPatched misses due to proxy version change), the log should
	// report "no byte changes", not "updated".
	requireExePatchEnabled(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, []byte("foo foo"), 0o700); err != nil {
		t.Fatalf("write: %v", err)
	}

	store, err := config.NewPatchHistoryStore(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	spec := exePatchSpec{
		match:   regexp.MustCompile("foo"),
		patch:   regexp.MustCompile("foo"),
		replace: []byte("bar"),
		label:   "test",
	}

	log := &bytes.Buffer{}
	if _, err := patchExecutable(path, []exePatchSpec{spec}, log, false, false, store, "v1"); err != nil {
		t.Fatalf("first patch: %v", err)
	}

	// Second run with different proxy version — IsPatched returns false,
	// but the binary content is already correct.
	log.Reset()
	outcome, err := patchExecutable(path, []exePatchSpec{spec}, log, false, false, store, "v2")
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if outcome.Applied {
		t.Fatalf("expected no write (binary already correct)")
	}
	if strings.Contains(log.String(), "updated") {
		t.Fatalf("log should not say 'updated' when binary is unchanged: %q", log.String())
	}
	if !strings.Contains(log.String(), "no byte changes") {
		t.Fatalf("expected 'no byte changes' log, got %q", log.String())
	}
	if !outcome.AlreadyPatched {
		t.Fatalf("expected AlreadyPatched=true when touched but not changed")
	}
}

func TestPatchExecutableAlreadyPatchedWindowsRemainsUnverified(t *testing.T) {
	// On Windows, an already-patched binary from history that was never
	// verified should remain unverified so the outer function can trigger
	// the readiness check.
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	runtimeGOOS = "windows"

	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, []byte("bar bar"), 0o700); err != nil {
		t.Fatalf("write: %v", err)
	}

	store, err := config.NewPatchHistoryStore(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	spec := exePatchSpec{
		match:   regexp.MustCompile("foo"),
		patch:   regexp.MustCompile("foo"),
		replace: []byte("bar"),
		label:   "test",
	}

	// Seed history with a matching entry but no VerifiedAt.
	patchedHash := hashBytes([]byte("bar bar"))
	specsHash := patchSpecsHash([]exePatchSpec{spec})
	if err := store.Update(func(h *config.PatchHistory) error {
		h.Upsert(config.PatchHistoryEntry{
			Path:          path,
			SpecsSHA256:   specsHash,
			PatchedSHA256: patchedHash,
			ProxyVersion:  "v1",
			PatchedAt:     time.Now(),
			VerifiedAt:    time.Time{}, // explicitly unverified
		})
		return nil
	}); err != nil {
		t.Fatalf("seed history: %v", err)
	}

	log := &bytes.Buffer{}
	outcome, err := patchExecutable(path, []exePatchSpec{spec}, log, false, false, store, "v1")
	if err != nil {
		t.Fatalf("patchExecutable: %v", err)
	}
	if !outcome.AlreadyPatched {
		t.Fatalf("expected AlreadyPatched=true")
	}
	if outcome.Verified {
		t.Fatalf("expected Verified=false on Windows for unverified history entry")
	}
}

func TestPatchExecutableAlreadyPatchedNonWindowsAutoVerifies(t *testing.T) {
	// On non-Windows, an already-patched binary that has no VerifiedAt
	// should be auto-promoted to verified.
	requireExePatchEnabled(t)
	withExePatchTestHooks(t)

	runtimeGOOS = "linux"

	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, []byte("bar bar"), 0o700); err != nil {
		t.Fatalf("write: %v", err)
	}

	store, err := config.NewPatchHistoryStore(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	spec := exePatchSpec{
		match:   regexp.MustCompile("foo"),
		patch:   regexp.MustCompile("foo"),
		replace: []byte("bar"),
		label:   "test",
	}

	patchedHash := hashBytes([]byte("bar bar"))
	specsHash := patchSpecsHash([]exePatchSpec{spec})
	if err := store.Update(func(h *config.PatchHistory) error {
		h.Upsert(config.PatchHistoryEntry{
			Path:          path,
			SpecsSHA256:   specsHash,
			PatchedSHA256: patchedHash,
			ProxyVersion:  "v1",
			PatchedAt:     time.Now(),
			VerifiedAt:    time.Time{}, // explicitly unverified
		})
		return nil
	}); err != nil {
		t.Fatalf("seed history: %v", err)
	}

	log := &bytes.Buffer{}
	outcome, err := patchExecutable(path, []exePatchSpec{spec}, log, false, false, store, "v1")
	if err != nil {
		t.Fatalf("patchExecutable: %v", err)
	}
	if !outcome.AlreadyPatched {
		t.Fatalf("expected AlreadyPatched=true")
	}
	if !outcome.Verified {
		t.Fatalf("expected Verified=true on non-Windows (auto-promoted)")
	}
}

func TestTruncHash(t *testing.T) {
	if got := truncHash("abcdef1234567890"); got != "abcdef123456" {
		t.Fatalf("expected first 12 chars, got %q", got)
	}
	if got := truncHash("short"); got != "short" {
		t.Fatalf("expected full string for short input, got %q", got)
	}
	if got := truncHash(""); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestLogPatchHistoryMiss(t *testing.T) {
	log := &bytes.Buffer{}

	// No entries — should log "no history entry".
	history := config.PatchHistory{
		Version: 1,
		Entries: []config.PatchHistoryEntry{
			{Path: "/other/path", SpecsSHA256: "other-spec", PatchedSHA256: "h1", ProxyVersion: "v1"},
		},
	}
	logPatchHistoryMiss(log, "/usr/bin/claude", "abcdef123456abcdef", history)
	if !strings.Contains(log.String(), "no history entry") {
		t.Fatalf("expected 'no history entry', got %q", log.String())
	}

	// Case mismatch — should log "path case mismatch".
	log.Reset()
	history.Entries[0].Path = "/usr/bin/Claude"
	logPatchHistoryMiss(log, "/usr/bin/claude", "abcdef123456abcdef", history)
	if !strings.Contains(log.String(), "path case mismatch") {
		t.Fatalf("expected 'path case mismatch', got %q", log.String())
	}

	// nil writer — should not panic.
	logPatchHistoryMiss(nil, "/usr/bin/claude", "abcdef123456abcdef", history)
}
