package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

func TestSupportsYoloFlag(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	dir := t.TempDir()

	pathWithFlag := writeProbeScript(t, dir, "has-flag", "#!/bin/sh\necho \"--permission-mode\"; exit 0\n")
	if !supportsYoloFlag(pathWithFlag) {
		t.Fatalf("expected support when flag appears in help output")
	}

	pathWithDanger := writeProbeScript(t, dir, "has-danger", "#!/bin/sh\necho \"--dangerously-skip-permissions\"; exit 0\n")
	if !supportsYoloFlag(pathWithDanger) {
		t.Fatalf("expected support when dangerous skip flag appears in help output")
	}

	pathWithUsage := writeProbeScript(t, dir, "usage", "#!/bin/sh\necho \"usage: claude\"; exit 0\n")
	if supportsYoloFlag(pathWithUsage) {
		t.Fatalf("expected no support for plain usage output")
	}

	pathWithError := writeProbeScript(t, dir, "error", "#!/bin/sh\nexit 1\n")
	if supportsYoloFlag(pathWithError) {
		t.Fatalf("expected no support when help command fails")
	}
}

func TestYoloBypassArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	dir := t.TempDir()

	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "legacy permission mode only",
			body: "#!/bin/sh\necho \"--permission-mode\"; exit 0\n",
			want: []string{"--permission-mode", "bypassPermissions"},
		},
		{
			name: "dangerous skip only",
			body: "#!/bin/sh\necho \"--dangerously-skip-permissions\"; exit 0\n",
			want: []string{"--dangerously-skip-permissions"},
		},
		{
			name: "both flags",
			body: "#!/bin/sh\necho \"--dangerously-skip-permissions\\n--permission-mode\"; exit 0\n",
			want: []string{"--dangerously-skip-permissions", "--permission-mode", "bypassPermissions"},
		},
		{
			name: "probe error fails closed",
			body: "#!/bin/sh\nexit 1\n",
			want: nil,
		},
	}

	for _, tc := range cases {
		path := writeProbeScript(t, dir, tc.name, tc.body)
		got := yoloBypassArgs(path)
		if len(got) != len(tc.want) {
			t.Fatalf("%s: expected %v, got %v", tc.name, tc.want, got)
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Fatalf("%s: expected %v, got %v", tc.name, tc.want, got)
			}
		}
	}
}

func TestRecordPatchFailureAndSkip(t *testing.T) {
	requireExePatchEnabled(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	t.Setenv(claudeProxyHostIDEnv, "host-a")
	origVersion := version
	version = "v9.9.9"
	t.Cleanup(func() { version = origVersion })

	outcome := &patchOutcome{
		IsClaude:      true,
		TargetVersion: "2.1.19",
		TargetSHA256:  "abc123",
		TargetPath:    "/tmp/claude",
	}
	if err := recordPatchFailure(configPath, outcome, "boom"); err != nil {
		t.Fatalf("recordPatchFailure error: %v", err)
	}
	store, err := config.NewStore(configPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.PatchFailures) != 1 {
		t.Fatalf("expected 1 patch failure, got %d", len(cfg.PatchFailures))
	}
	entry := cfg.PatchFailures[0]
	if entry.ProxyVersion != "v9.9.9" || entry.HostID != "host-a" || entry.ClaudeVersion != "2.1.19" || entry.ClaudeSHA256 != "abc123" {
		t.Fatalf("unexpected patch failure entry: %#v", entry)
	}
	if entry.Reason == "" {
		t.Fatalf("expected failure reason to be recorded")
	}

	if skip, err := shouldSkipPatchFailure(configPath, "v9.9.9", "2.1.19", ""); err != nil || !skip {
		t.Fatalf("expected skip by version, got skip=%v err=%v", skip, err)
	}
	if skip, err := shouldSkipPatchFailure(configPath, "v9.9.9", "", "abc123"); err != nil || !skip {
		t.Fatalf("expected skip by sha, got skip=%v err=%v", skip, err)
	}
	if skip, err := shouldSkipPatchFailure(configPath, "v9.9.8", "2.1.19", ""); err != nil || skip {
		t.Fatalf("expected no skip for different proxy version, got skip=%v err=%v", skip, err)
	}
}

func TestShouldSkipPatchFailureIsHostScoped(t *testing.T) {
	requireExePatchEnabled(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	t.Setenv(claudeProxyHostIDEnv, "host-a")
	origVersion := version
	version = "v9.9.9"
	t.Cleanup(func() { version = origVersion })

	outcome := &patchOutcome{
		IsClaude:      true,
		TargetVersion: "2.1.19",
		TargetSHA256:  "abc123",
		TargetPath:    "/shared/claude",
	}
	if err := recordPatchFailure(configPath, outcome, "boom"); err != nil {
		t.Fatalf("recordPatchFailure error: %v", err)
	}

	t.Setenv(claudeProxyHostIDEnv, "host-b")
	if skip, err := shouldSkipPatchFailure(configPath, "v9.9.9", "2.1.19", ""); err != nil || skip {
		t.Fatalf("expected host-scoped skip miss, got skip=%v err=%v", skip, err)
	}

	t.Setenv(claudeProxyHostIDEnv, "host-a")
	if skip, err := shouldSkipPatchFailure(configPath, "v9.9.9", "2.1.19", ""); err != nil || !skip {
		t.Fatalf("expected host-scoped skip hit, got skip=%v err=%v", skip, err)
	}
}

func TestRecordPatchFailureUsesSourceSHAForMirrorTargets(t *testing.T) {
	requireExePatchEnabled(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	t.Setenv(claudeProxyHostIDEnv, "host-a")
	origVersion := version
	version = "v9.9.9"
	t.Cleanup(func() { version = origVersion })

	sourcePath := filepath.Join(dir, "source-claude")
	mirrorPath := filepath.Join(dir, "mirror-claude")
	if err := os.WriteFile(sourcePath, []byte("source-bytes"), 0o700); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(mirrorPath, []byte("mirror-bytes"), 0o700); err != nil {
		t.Fatalf("write mirror: %v", err)
	}
	sourceSHA, err := hashFileSHA256(sourcePath)
	if err != nil {
		t.Fatalf("hash source: %v", err)
	}
	mirrorSHA, err := hashFileSHA256(mirrorPath)
	if err != nil {
		t.Fatalf("hash mirror: %v", err)
	}

	outcome := &patchOutcome{
		IsClaude:      true,
		SourcePath:    sourcePath,
		SourceSHA256:  sourceSHA,
		TargetPath:    mirrorPath,
		TargetSHA256:  mirrorSHA,
		TargetVersion: "",
	}
	if err := os.WriteFile(sourcePath, []byte("source-bytes-v2"), 0o700); err != nil {
		t.Fatalf("rewrite source: %v", err)
	}
	newSourceSHA, err := hashFileSHA256(sourcePath)
	if err != nil {
		t.Fatalf("hash rewritten source: %v", err)
	}
	if err := recordPatchFailure(configPath, outcome, "boom"); err != nil {
		t.Fatalf("recordPatchFailure error: %v", err)
	}
	store, err := config.NewStore(configPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.PatchFailures) != 1 {
		t.Fatalf("expected 1 patch failure, got %d", len(cfg.PatchFailures))
	}
	entry := cfg.PatchFailures[0]
	if entry.ClaudePath != sourcePath {
		t.Fatalf("expected source path %q, got %q", sourcePath, entry.ClaudePath)
	}
	if entry.ClaudeSHA256 != sourceSHA {
		t.Fatalf("expected source sha %q, got %q", sourceSHA, entry.ClaudeSHA256)
	}
	if skip, err := shouldSkipPatchFailure(configPath, "v9.9.9", "", sourceSHA); err != nil || !skip {
		t.Fatalf("expected skip by source sha, got skip=%v err=%v", skip, err)
	}
	if skip, err := shouldSkipPatchFailure(configPath, "v9.9.9", "", newSourceSHA); err != nil || skip {
		t.Fatalf("expected rewritten source sha not to match old failure, got skip=%v err=%v", skip, err)
	}
}

func TestIsClaudeExecutableUsesCmdArgWhenResolvedDiffers(t *testing.T) {
	if !isClaudeExecutable("claude", "/Users/mocha/.local/share/claude/versions/2.1.22") {
		t.Fatalf("expected cmd arg to identify claude binary")
	}
	if !isClaudeExecutable("claude.exe", "C:\\Users\\mocha\\AppData\\Local\\claude\\2.1.22") {
		t.Fatalf("expected cmd arg to identify claude.exe binary")
	}
	if isClaudeExecutable("not-claude", "/tmp/2.1.22") {
		t.Fatalf("expected non-claude to be rejected")
	}
}

func TestIsYoloFailure(t *testing.T) {
	cases := []struct {
		output string
		want   bool
	}{
		{"", false},
		{"unknown flag: --permission-mode", true},
		{"unknown flag: --dangerously-skip-permissions", true},
		{"permission-mode unknown", true},
		{"dangerously-skip-permissions unknown", true},
		{"permission-mode not supported", true},
		{"permission-mode invalid", true},
		{"permission-mode flag provided but not defined", true},
		{"Tool permission request failed: Error: Stream closed", true},
		{"unrelated error", false},
	}
	for _, tc := range cases {
		got := isYoloFailure(os.ErrInvalid, tc.output)
		if got != tc.want {
			t.Fatalf("output %q: expected %v, got %v", tc.output, tc.want, got)
		}
	}
	if isYoloFailure(nil, "permission-mode unknown") {
		t.Fatalf("expected nil error to return false")
	}
}

func TestStripYoloArgs(t *testing.T) {
	in := []string{
		"claude",
		"--dangerously-skip-permissions",
		"--allow-dangerously-skip-permissions",
		"--permission-mode",
		"bypassPermissions",
		"--resume",
		"abc",
	}
	out := stripYoloArgs(in)
	want := []string{"claude", "--resume", "abc"}
	if len(out) != len(want) {
		t.Fatalf("expected %v, got %v", want, out)
	}
	for i := range want {
		if out[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, out)
		}
	}
}

func TestHasYoloBypassPermissionsArg(t *testing.T) {
	cases := []struct {
		name    string
		cmdArgs []string
		want    bool
	}{
		{
			name:    "disabled",
			cmdArgs: []string{"claude", "--resume", "abc"},
			want:    false,
		},
		{
			name:    "split args",
			cmdArgs: []string{"claude", "--permission-mode", "bypassPermissions", "--resume", "abc"},
			want:    true,
		},
		{
			name:    "dangerous skip",
			cmdArgs: []string{"claude", "--dangerously-skip-permissions", "--resume", "abc"},
			want:    true,
		},
		{
			name:    "equals form",
			cmdArgs: []string{"claude", "--permission-mode=bypassPermissions", "--resume", "abc"},
			want:    true,
		},
		{
			name:    "different mode",
			cmdArgs: []string{"claude", "--permission-mode", "acceptEdits"},
			want:    false,
		},
	}

	for _, tc := range cases {
		if got := hasYoloBypassPermissionsArg(tc.cmdArgs); got != tc.want {
			t.Fatalf("%s: expected %v, got %v", tc.name, tc.want, got)
		}
	}
}

func TestExtractVersion(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"Claude Code 1.2.3", "1.2.3"},
		{"v2.0", "2.0"},
		{"version", "version"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := extractVersion(tc.input); got != tc.want {
			t.Fatalf("extractVersion(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestResolveClaudeVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	if runtime.GOOS == "windows" {
		path += ".cmd"
	}
	writeStub(t, dir, "claude", "#!/bin/sh\necho \"Claude Code 1.2.3\"\n", "@echo off\r\necho Claude Code 1.2.3\r\n")
	if got := resolveClaudeVersion(path); got != "1.2.3" {
		t.Fatalf("expected version 1.2.3, got %q", got)
	}
}

func TestRunClaudeProbeArgsAndOutcome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script probe test on windows")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "probe.sh")
	content := "#!/bin/sh\nprintf '%s\\n' \"$@\"\n"
	if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
		t.Fatalf("write probe script: %v", err)
	}

	out, err := runClaudeProbeArgs([]string{script, "--shim"}, "--version")
	if err != nil {
		t.Fatalf("runClaudeProbeArgs error: %v", err)
	}
	if out != "--shim\n--version\n" {
		t.Fatalf("unexpected probe args output: %q", out)
	}

	outcome := &patchOutcome{LaunchArgsPrefix: []string{script, "--shim"}}
	out, err = runClaudeProbeOutcome(outcome, "/ignored", "--help")
	if err != nil {
		t.Fatalf("runClaudeProbeOutcome error: %v", err)
	}
	if out != "--shim\n--help\n" {
		t.Fatalf("unexpected probe outcome output: %q", out)
	}
}

func TestResolveYoloBypassArgsUsesCachedProbe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	body := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then\n" +
		"  echo \"Claude Code 9.9.9\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"--help\" ]; then\n" +
		"  echo \"--dangerously-skip-permissions\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write probe script: %v", err)
	}

	configPath := filepath.Join(t.TempDir(), "config.json")
	got := resolveYoloBypassArgs(path, configPath)
	want := []string{"--dangerously-skip-permissions"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("expected %v, got %v", want, got)
	}

	body = "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then\n" +
		"  echo \"Claude Code 9.9.9\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("rewrite probe script: %v", err)
	}

	got = resolveYoloBypassArgs(path, configPath)
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("expected cached args %v, got %v", want, got)
	}
}

func TestResolveYoloBypassArgsDoesNotCacheProbeFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	body := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then\n" +
		"  echo \"Claude Code 9.9.9\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write failing probe script: %v", err)
	}

	configPath := filepath.Join(t.TempDir(), "config.json")
	if got := resolveYoloBypassArgs(path, configPath); got != nil {
		t.Fatalf("expected nil args after probe failure, got %v", got)
	}

	body = "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then\n" +
		"  echo \"Claude Code 9.9.9\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"--help\" ]; then\n" +
		"  echo \"--dangerously-skip-permissions\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("rewrite probe script: %v", err)
	}

	got := resolveYoloBypassArgs(path, configPath)
	want := []string{"--dangerously-skip-permissions"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("expected reprobed args %v after transient failure, got %v", want, got)
	}
}

func TestResolveYoloBypassArgsCachesSuccessfulUnsupportedProbe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	body := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then\n" +
		"  echo \"Claude Code 9.9.9\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"--help\" ]; then\n" +
		"  echo \"usage: claude\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write unsupported probe script: %v", err)
	}

	configPath := filepath.Join(t.TempDir(), "config.json")
	if got := resolveYoloBypassArgs(path, configPath); got != nil {
		t.Fatalf("expected nil args for unsupported probe, got %v", got)
	}

	body = "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then\n" +
		"  echo \"Claude Code 9.9.9\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"--help\" ]; then\n" +
		"  echo \"--dangerously-skip-permissions\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("rewrite probe script: %v", err)
	}

	if got := resolveYoloBypassArgs(path, configPath); got != nil {
		t.Fatalf("expected cached unsupported result to suppress reprobe, got %v", got)
	}
}

func TestRunClaudeProbeArgsWithContextMissingCommand(t *testing.T) {
	if _, err := runClaudeProbeArgsWithContext(context.Background(), nil, "--version", time.Second); err == nil {
		t.Fatalf("expected missing probe command error")
	}
}

func TestHashFileSHA256(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	got, err := hashFileSHA256(path)
	if err != nil {
		t.Fatalf("hashFileSHA256 error: %v", err)
	}
	want := sha256.Sum256([]byte("hello"))
	if got != hex.EncodeToString(want[:]) {
		t.Fatalf("unexpected hash %q", got)
	}
}

func TestIsPatchedBinaryStartupFailure(t *testing.T) {
	if isPatchedBinaryStartupFailure(nil, "") {
		t.Fatalf("expected nil error to be false")
	}
	if !isPatchedBinaryStartupFailure(os.ErrInvalid, "@bun @bytecode") {
		t.Fatalf("expected bun bytecode output to be treated as failure")
	}
	if !isPatchedBinaryStartupFailure(&os.PathError{Op: "open", Path: "/nope", Err: os.ErrNotExist}, "") {
		t.Fatalf("expected path error to be treated as failure")
	}
	if !isPatchedBinaryStartupFailure(&exec.Error{Name: "missing", Err: exec.ErrNotFound}, "") {
		t.Fatalf("expected exec error to be treated as failure")
	}
	cmdName := "sh"
	cmdArgs := []string{"-c", "exit 1"}
	if runtime.GOOS == "windows" {
		cmdName = "cmd"
		cmdArgs = []string{"/C", "exit 1"}
	}
	cmd := exec.Command(cmdName, cmdArgs...)
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected exit error")
	}
	if isPatchedBinaryStartupFailure(err, "") {
		t.Fatalf("expected exit error to be treated as non-startup failure")
	}
}

func TestPatchFailureHelpers(t *testing.T) {
	t.Run("formatFailureReason truncates", func(t *testing.T) {
		long := strings.Repeat("x", 300)
		got := formatFailureReason(nil, long)
		if len(got) != 243 {
			t.Fatalf("expected truncated length 243, got %d", len(got))
		}
		if !strings.HasSuffix(got, "...") {
			t.Fatalf("expected truncation suffix, got %q", got)
		}
	})

	t.Run("recordPatchFailure merges concurrent entries", func(t *testing.T) {
		requireExePatchEnabled(t)
		dir := t.TempDir()
		configPath := filepath.Join(dir, "config.json")
		t.Setenv(claudeProxyHostIDEnv, "host-a")
		outcome := &patchOutcome{
			IsClaude:      true,
			TargetVersion: "2.2.0",
			TargetSHA256:  "sha-1",
			TargetPath:    "/tmp/claude",
		}

		const workers = 5
		errCh := make(chan error, workers)
		for i := 0; i < workers; i++ {
			go func() {
				errCh <- recordPatchFailure(configPath, outcome, "boom")
			}()
		}
		for i := 0; i < workers; i++ {
			if err := <-errCh; err != nil {
				t.Fatalf("recordPatchFailure error: %v", err)
			}
		}

		store, err := config.NewStore(configPath)
		if err != nil {
			t.Fatalf("new store: %v", err)
		}
		cfg, err := store.Load()
		if err != nil {
			t.Fatalf("load config: %v", err)
		}
		if len(cfg.PatchFailures) != 1 {
			t.Fatalf("expected 1 patch failure entry, got %d", len(cfg.PatchFailures))
		}
	})

	t.Run("probeArgsForOutcome handles fallback shapes", func(t *testing.T) {
		if got := probeArgsForOutcome(nil, ""); got != nil {
			t.Fatalf("expected nil args for empty probe arg, got %#v", got)
		}
		outcome := &patchOutcome{TargetPath: "/tmp/mirror"}
		got := probeArgsForOutcome(outcome, "--version")
		if len(got) != 2 || got[0] != "/tmp/mirror" || got[1] != "--version" {
			t.Fatalf("unexpected target-path probe args: %#v", got)
		}
		outcome = &patchOutcome{SourcePath: "/tmp/source"}
		got = probeArgsForOutcome(outcome, "--help")
		if len(got) != 2 || got[0] != "/tmp/source" || got[1] != "--help" {
			t.Fatalf("unexpected source-path probe args: %#v", got)
		}
		outcome = &patchOutcome{LaunchArgsPrefix: []string{"/tmp/wrapper", "--shim"}}
		got = probeArgsForOutcome(outcome, "--help")
		if len(got) != 3 || got[0] != "/tmp/wrapper" || got[1] != "--shim" || got[2] != "--help" {
			t.Fatalf("unexpected launch-prefix probe args: %#v", got)
		}
	})
}

func TestShouldSkipPatchFailurePurgesStaleEntriesOnWindows(t *testing.T) {
	requireExePatchEnabled(t)

	seedStaleFailure := func(t *testing.T) (string, *config.Store) {
		t.Helper()
		dir := t.TempDir()
		configPath := filepath.Join(dir, "config.json")
		t.Setenv(claudeProxyHostIDEnv, "host-a")
		store, err := config.NewStore(configPath)
		if err != nil {
			t.Fatalf("new store: %v", err)
		}
		if err := store.Update(func(cfg *config.Config) error {
			cfg.UpsertPatchFailure(config.PatchFailure{
				ProxyVersion:  "v0.0.40",
				HostID:        "host-a",
				ClaudeVersion: "2.1.19",
				ClaudeSHA256:  "abc",
			})
			return nil
		}); err != nil {
			t.Fatalf("seed failure: %v", err)
		}
		return configPath, store
	}

	t.Run("Windows purges stale failures on upgrade", func(t *testing.T) {
		prev := runtimeGOOS
		runtimeGOOS = "windows"
		t.Cleanup(func() { runtimeGOOS = prev })

		configPath, store := seedStaleFailure(t)

		skip, err := shouldSkipPatchFailure(configPath, "v0.0.42", "2.1.19", "")
		if err != nil {
			t.Fatalf("shouldSkipPatchFailure error: %v", err)
		}
		if skip {
			t.Fatalf("expected stale failure to be purged, not skipped")
		}

		cfg, err := store.Load()
		if err != nil {
			t.Fatalf("load config: %v", err)
		}
		if len(cfg.PatchFailures) != 0 {
			t.Fatalf("expected 0 failures after purge, got %d", len(cfg.PatchFailures))
		}
	})

	t.Run("non-Windows does not purge stale failures", func(t *testing.T) {
		prev := runtimeGOOS
		runtimeGOOS = "linux"
		t.Cleanup(func() { runtimeGOOS = prev })

		configPath, store := seedStaleFailure(t)

		// Different proxy version, but same claude version — on non-Windows
		// the old entry is NOT purged, so the lookup simply misses (different
		// proxy version) and returns false.
		skip, err := shouldSkipPatchFailure(configPath, "v0.0.42", "2.1.19", "")
		if err != nil {
			t.Fatalf("shouldSkipPatchFailure error: %v", err)
		}
		if skip {
			t.Fatalf("expected no skip for different proxy version")
		}

		// The stale entry should still be on disk.
		cfg, err := store.Load()
		if err != nil {
			t.Fatalf("load config: %v", err)
		}
		if len(cfg.PatchFailures) != 1 {
			t.Fatalf("expected stale entry preserved on non-Windows, got %d", len(cfg.PatchFailures))
		}
	})
}

func writeProbeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}
