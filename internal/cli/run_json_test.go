package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

func writeRunJSONSpec(t *testing.T, dir string, body string) string {
	t.Helper()
	path := filepath.Join(dir, "spec.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write run-json spec: %v", err)
	}
	return path
}

func stringPtr(v string) *string { return &v }

func requireSymlinkOrSkip(t *testing.T, oldname string, newname string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("skip symlink path test on windows")
	}
	if err := os.Symlink(oldname, newname); err != nil {
		t.Skipf("skip symlink path test: %v", err)
	}
}

func TestLoadClaudeRunJSONSpecDefaultsAndResolvesPaths(t *testing.T) {
	specDir := t.TempDir()
	cwd := filepath.Join(specDir, "workspace")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}
	stdinPath := filepath.Join(specDir, "inputs", "request.jsonl")
	if err := os.MkdirAll(filepath.Dir(stdinPath), 0o755); err != nil {
		t.Fatalf("mkdir stdin dir: %v", err)
	}
	if err := os.WriteFile(stdinPath, []byte(`{"type":"user"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write stdin file: %v", err)
	}

	specPath := writeRunJSONSpec(t, specDir, `{
  "cwd": "workspace",
  "args": ["--print", "--input-format", "stream-json", "--output-format", "stream-json"],
  "stdinPath": "inputs/request.jsonl",
  "stdoutPath": "outputs/response.jsonl",
  "stderrPath": "logs/error.log"
}`)

	spec, err := loadClaudeRunJSONSpec(specPath)
	if err != nil {
		t.Fatalf("loadClaudeRunJSONSpec error: %v", err)
	}
	if spec.Cwd != cwd {
		t.Fatalf("expected cwd %q, got %q", cwd, spec.Cwd)
	}
	if spec.StdinPath != stdinPath {
		t.Fatalf("expected stdinPath %q, got %q", stdinPath, spec.StdinPath)
	}
	if spec.StdoutPath != filepath.Join(specDir, "outputs", "response.jsonl") {
		t.Fatalf("unexpected stdoutPath %q", spec.StdoutPath)
	}
	if spec.StderrPath != filepath.Join(specDir, "logs", "error.log") {
		t.Fatalf("unexpected stderrPath %q", spec.StderrPath)
	}
}

func TestLoadClaudeRunJSONSpecRequiresCwd(t *testing.T) {
	specDir := t.TempDir()
	specPath := writeRunJSONSpec(t, specDir, `{}`)

	if _, err := loadClaudeRunJSONSpec(specPath); err == nil {
		t.Fatalf("expected cwd validation error")
	}
}

func TestLoadClaudeRunJSONSpecRejectsUnknownField(t *testing.T) {
	specPath := writeRunJSONSpec(t, t.TempDir(), `{"wat":"nope"}`)
	if _, err := loadClaudeRunJSONSpec(specPath); err == nil {
		t.Fatalf("expected unknown field error")
	}
}

func TestLoadClaudeRunJSONSpecRejectsMultipleJSONObjects(t *testing.T) {
	specPath := writeRunJSONSpec(t, t.TempDir(), `{"cwd":"."}{"cwd":"."}`)
	if _, err := loadClaudeRunJSONSpec(specPath); err == nil || !strings.Contains(err.Error(), "expected a single JSON object") {
		t.Fatalf("expected multiple object parse error, got %v", err)
	}
}

func TestLoadClaudeRunJSONSpecNormalizesClaudeArgs(t *testing.T) {
	specPath := writeRunJSONSpec(t, t.TempDir(), `{
  "cwd": ".",
  "args": ["claude", "--permission-mode", "plan", "--print"]
}`)

	spec, err := loadClaudeRunJSONSpec(specPath)
	if err != nil {
		t.Fatalf("loadClaudeRunJSONSpec error: %v", err)
	}
	want := []string{"--permission-mode", "plan", "--print"}
	if len(spec.Args) != len(want) {
		t.Fatalf("expected args %v, got %v", want, spec.Args)
	}
	for i := range want {
		if spec.Args[i] != want[i] {
			t.Fatalf("expected args %v, got %v", want, spec.Args)
		}
	}
}

func TestLoadClaudeRunJSONSpecRejectsInvalidPaths(t *testing.T) {
	t.Run("missing stdin", func(t *testing.T) {
		specPath := writeRunJSONSpec(t, t.TempDir(), `{"cwd":".","stdinPath":"missing.jsonl"}`)
		if _, err := loadClaudeRunJSONSpec(specPath); err == nil {
			t.Fatalf("expected missing stdin error")
		}
	})

	t.Run("stdout matches spec", func(t *testing.T) {
		specDir := t.TempDir()
		specPath := writeRunJSONSpec(t, specDir, `{"cwd":".","stdoutPath":"spec.json"}`)
		if _, err := loadClaudeRunJSONSpec(specPath); err == nil {
			t.Fatalf("expected spec overwrite error")
		}
	})

	t.Run("stdout matches stderr", func(t *testing.T) {
		specDir := t.TempDir()
		stdinPath := filepath.Join(specDir, "in.jsonl")
		if err := os.WriteFile(stdinPath, []byte("{}\n"), 0o600); err != nil {
			t.Fatalf("write stdin: %v", err)
		}
		specPath := writeRunJSONSpec(t, specDir, `{"cwd":".","stdinPath":"in.jsonl","stdoutPath":"same.log","stderrPath":"same.log"}`)
		if _, err := loadClaudeRunJSONSpec(specPath); err == nil {
			t.Fatalf("expected stdout/stderr collision error")
		}
	})

	t.Run("stdout matches symlinked spec target", func(t *testing.T) {
		realDir := t.TempDir()
		linkDir := t.TempDir()
		specRealPath := filepath.Join(realDir, "spec.json")
		linkPath := filepath.Join(linkDir, "spec-link.json")
		requireSymlinkOrSkip(t, specRealPath, linkPath)
		if err := os.WriteFile(specRealPath, []byte("{\n  \"cwd\": \".\",\n  \"stdoutPath\": "+strconv.Quote(specRealPath)+"\n}\n"), 0o600); err != nil {
			t.Fatalf("write real spec: %v", err)
		}
		if _, err := loadClaudeRunJSONSpec(linkPath); err == nil {
			t.Fatalf("expected spec overwrite collision through symlink alias")
		}
	})

	t.Run("stdout matches stderr through symlinked parent", func(t *testing.T) {
		specDir := t.TempDir()
		realDir := t.TempDir()
		aliasBase := t.TempDir()
		aliasDir := filepath.Join(aliasBase, "alias")
		requireSymlinkOrSkip(t, realDir, aliasDir)
		stdoutPath := filepath.Join(realDir, "nested", "out.log")
		stderrPath := filepath.Join(aliasDir, "nested", "out.log")
		specPath := writeRunJSONSpec(t, specDir, "{\n  \"cwd\": \".\",\n  \"stdoutPath\": "+strconv.Quote(stdoutPath)+",\n  \"stderrPath\": "+strconv.Quote(stderrPath)+"\n}")
		if _, err := loadClaudeRunJSONSpec(specPath); err == nil {
			t.Fatalf("expected stdout/stderr collision through symlinked parent")
		}
	})

	t.Run("cwd must be a directory", func(t *testing.T) {
		specDir := t.TempDir()
		notDirPath := filepath.Join(specDir, "not-a-dir")
		if err := os.WriteFile(notDirPath, []byte("x"), 0o600); err != nil {
			t.Fatalf("write non-dir path: %v", err)
		}
		specPath := writeRunJSONSpec(t, specDir, `{"cwd":"not-a-dir"}`)
		if _, err := loadClaudeRunJSONSpec(specPath); err == nil || !strings.Contains(err.Error(), "working directory is not a directory") {
			t.Fatalf("expected non-directory cwd error, got %v", err)
		}
	})

	t.Run("stdout path must not be a directory", func(t *testing.T) {
		specDir := t.TempDir()
		outDir := filepath.Join(specDir, "outdir")
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			t.Fatalf("mkdir outdir: %v", err)
		}
		specPath := writeRunJSONSpec(t, specDir, `{"cwd":".","stdoutPath":"outdir"}`)
		if _, err := loadClaudeRunJSONSpec(specPath); err == nil || !strings.Contains(err.Error(), "stdoutPath must be a file path") {
			t.Fatalf("expected stdout directory error, got %v", err)
		}
	})
}

func TestLoadClaudeRunJSONSpecLoadsHeadlessRetryOptions(t *testing.T) {
	specDir := t.TempDir()
	cwd := filepath.Join(specDir, "workspace")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}
	specPath := writeRunJSONSpec(t, specDir, `{
  "cwd": "workspace",
  "headless": true,
  "preserveRetryOutputs": true,
  "stdoutPath": "out.txt",
  "stderrPath": "err.txt"
}`)

	spec, err := loadClaudeRunJSONSpec(specPath)
	if err != nil {
		t.Fatalf("loadClaudeRunJSONSpec error: %v", err)
	}
	if !spec.Headless {
		t.Fatalf("expected headless to be true")
	}
	if !spec.PreserveRetryOutputs {
		t.Fatalf("expected preserveRetryOutputs to be true")
	}
}

func TestRunJSONCmdUsesYoloVisibilityGate(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.Config
		want bool
	}{
		{
			name: "hidden",
			cfg:  config.Config{Version: config.CurrentVersion},
			want: false,
		},
		{
			name: "bypass",
			cfg: config.Config{
				Version:  config.CurrentVersion,
				YoloMode: stringPtr(string(config.YoloModeBypass)),
			},
			want: true,
		},
		{
			name: "visible off",
			cfg: config.Config{
				Version:  config.CurrentVersion,
				YoloMode: stringPtr(string(config.YoloModeOff)),
			},
			want: true,
		},
		{
			name: "rules",
			cfg: config.Config{
				Version:  config.CurrentVersion,
				YoloMode: stringPtr(string(config.YoloModeRules)),
			},
			want: true,
		},
		{
			name: "legacy false still visible",
			cfg: config.Config{
				Version:     config.CurrentVersion,
				YoloEnabled: boolPtr(false),
			},
			want: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			store := newTempStore(t)
			disabled := false
			tc.cfg.ProxyEnabled = &disabled
			if err := store.Save(tc.cfg); err != nil {
				t.Fatalf("save config: %v", err)
			}

			specDir := t.TempDir()
			specPath := writeRunJSONSpec(t, specDir, `{"cwd":"."}`)

			prevRun := runClaudeJSONSpecFunc
			t.Cleanup(func() { runClaudeJSONSpecFunc = prevRun })

			called := false
			runClaudeJSONSpecFunc = func(
				ctx context.Context,
				root *rootOptions,
				store *config.Store,
				profile *config.Profile,
				instances []config.Instance,
				spec preparedClaudeRunJSONSpec,
				claudePath string,
				claudeDir string,
				useProxy bool,
				yoloBypassUnlocked bool,
				log io.Writer,
			) error {
				called = true
				if spec.Cwd != specDir {
					t.Fatalf("expected spec cwd %q, got %q", specDir, spec.Cwd)
				}
				if yoloBypassUnlocked != tc.want {
					t.Fatalf("expected yolo gate %v, got %v", tc.want, yoloBypassUnlocked)
				}
				return nil
			}

			cmd := newRunJSONCmd(&rootOptions{configPath: store.Path()})
			cmd.SetArgs([]string{specPath})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("run-json command error: %v", err)
			}
			if !called {
				t.Fatalf("expected runClaudeJSONSpec to be called")
			}
		})
	}
}

func TestRunJSONCmdExplicitProfileUsesProxy(t *testing.T) {
	store := newTempStore(t)
	disabled := false
	cfg := config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &disabled,
		Profiles: []config.Profile{{
			ID:   "p1",
			Name: "profile",
			Host: "host",
			Port: 22,
			User: "user",
		}},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	specDir := t.TempDir()
	specPath := writeRunJSONSpec(t, specDir, `{"cwd":"."}`)

	prevRun := runClaudeJSONSpecFunc
	t.Cleanup(func() { runClaudeJSONSpecFunc = prevRun })

	called := false
	runClaudeJSONSpecFunc = func(
		ctx context.Context,
		root *rootOptions,
		store *config.Store,
		profile *config.Profile,
		instances []config.Instance,
		spec preparedClaudeRunJSONSpec,
		claudePath string,
		claudeDir string,
		useProxy bool,
		yoloBypassUnlocked bool,
		log io.Writer,
	) error {
		called = true
		if !useProxy {
			t.Fatalf("expected explicit profile to force proxy")
		}
		if profile == nil || profile.ID != "p1" {
			t.Fatalf("expected selected profile p1, got %#v", profile)
		}
		return nil
	}

	cmd := newRunJSONCmd(&rootOptions{configPath: store.Path()})
	cmd.SetArgs([]string{"--profile", "p1", specPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run-json command error: %v", err)
	}
	if !called {
		t.Fatalf("expected runClaudeJSONSpec to be called")
	}
}

func TestRunClaudeJSONSpecDoesNotMutateConfigOnYoloFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script execution on windows")
	}

	claudePath := filepath.Join(t.TempDir(), "claude")
	body := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then\n" +
		"  echo \"Claude Code 1.2.3\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"--help\" ]; then\n" +
		"  echo \"--permission-mode\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"for arg in \"$@\"; do\n" +
		"  if [ \"$arg\" = \"--permission-mode\" ]; then\n" +
		"    echo \"unknown flag: --permission-mode\" >&2\n" +
		"    exit 2\n" +
		"  fi\n" +
		"done\n" +
		"exit 0\n"
	if err := os.WriteFile(claudePath, []byte(body), 0o700); err != nil {
		t.Fatalf("write claude stub: %v", err)
	}

	store := newTempStore(t)
	disabled := false
	cfg := config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &disabled,
		YoloMode:     stringPtr(string(config.YoloModeRules)),
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	spec := preparedClaudeRunJSONSpec{Cwd: t.TempDir()}
	root := &rootOptions{configPath: store.Path()}
	if err := runClaudeJSONSpec(context.Background(), root, store, nil, nil, spec, claudePath, "", false, true, io.Discard); err != nil {
		t.Fatalf("runClaudeJSONSpec error: %v", err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got.YoloMode == nil || *got.YoloMode != string(config.YoloModeRules) {
		t.Fatalf("expected yolo mode to stay rules, got %#v", got.YoloMode)
	}
}

func TestRunClaudeJSONSpecHeadlessRoutesStatusWriterToStderrFile(t *testing.T) {
	withExePatchTestHooks(t)

	claudePath := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write claude stub: %v", err)
	}

	store := newTempStore(t)
	root := &rootOptions{configPath: store.Path()}
	stderrPath := filepath.Join(t.TempDir(), "logs", "status.log")
	spec := preparedClaudeRunJSONSpec{
		Cwd:        t.TempDir(),
		Headless:   true,
		StderrPath: stderrPath,
	}

	prevRun := runTargetWithFallbackWithOptionsFn
	prevPatch := maybePatchExecutableCtxFn
	t.Cleanup(func() {
		runTargetWithFallbackWithOptionsFn = prevRun
		maybePatchExecutableCtxFn = prevPatch
	})

	var terminalLog bytes.Buffer
	maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
		_, _ = io.WriteString(log, "patch-log should be discarded")
		return nil, nil
	}
	runTargetWithFallbackWithOptionsFn = func(ctx context.Context, cmdArgs []string, proxyURL string, healthCheck func() error, patchOutcome *patchOutcome, fatalCh <-chan error, opts runTargetOptions) error {
		if opts.PreserveTTY {
			t.Fatalf("expected headless run-json to disable tty preservation")
		}
		if opts.PrepareIO == nil {
			t.Fatalf("expected headless run-json to prepare custom IO")
		}
		if opts.StatusWriter == nil {
			t.Fatalf("expected headless run-json to configure a status writer")
		}
		_, err := io.WriteString(opts.StatusWriter, "status-line\n")
		return err
	}

	if err := runClaudeJSONSpec(context.Background(), root, store, nil, nil, spec, claudePath, "", false, false, &terminalLog); err != nil {
		t.Fatalf("runClaudeJSONSpec error: %v", err)
	}
	if terminalLog.Len() != 0 {
		t.Fatalf("expected headless launcher log to stay empty, got %q", terminalLog.String())
	}

	data, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatalf("read stderr log: %v", err)
	}
	if string(data) != "status-line\n" {
		t.Fatalf("unexpected headless status log %q", string(data))
	}
}

func TestRunClaudeJSONSpecPrefersExplicitPermissionArgsOverAutoBypass(t *testing.T) {
	withExePatchTestHooks(t)

	claudePath := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write claude stub: %v", err)
	}

	store := newTempStore(t)
	root := &rootOptions{configPath: store.Path()}
	spec := preparedClaudeRunJSONSpec{
		Cwd:  t.TempDir(),
		Args: []string{"--permission-mode", "plan", "--print"},
	}

	prevRun := runTargetWithFallbackWithOptionsFn
	prevPatch := maybePatchExecutableCtxFn
	t.Cleanup(func() {
		runTargetWithFallbackWithOptionsFn = prevRun
		maybePatchExecutableCtxFn = prevPatch
	})

	maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
		return nil, nil
	}
	runTargetWithFallbackWithOptionsFn = func(ctx context.Context, cmdArgs []string, proxyURL string, healthCheck func() error, patchOutcome *patchOutcome, fatalCh <-chan error, opts runTargetOptions) error {
		want := []string{claudePath, "--permission-mode", "plan", "--print"}
		requireArgsEqual(t, cmdArgs, want)
		if opts.YoloEnabled {
			t.Fatalf("expected explicit permission args to disable auto bypass injection")
		}
		return nil
	}

	if err := runClaudeJSONSpec(context.Background(), root, store, nil, nil, spec, claudePath, "", false, true, io.Discard); err != nil {
		t.Fatalf("runClaudeJSONSpec error: %v", err)
	}
}

func TestRunClaudeJSONSpecProxyRequiresProfile(t *testing.T) {
	withExePatchTestHooks(t)

	claudePath := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write claude stub: %v", err)
	}

	store := newTempStore(t)
	root := &rootOptions{configPath: store.Path()}
	err := runClaudeJSONSpec(
		context.Background(),
		root,
		store,
		nil,
		nil,
		preparedClaudeRunJSONSpec{Cwd: t.TempDir()},
		claudePath,
		"",
		true,
		false,
		io.Discard,
	)
	if err == nil || !strings.Contains(err.Error(), "proxy mode enabled but no profile configured") {
		t.Fatalf("expected missing profile error, got %v", err)
	}
}

func TestResolveRunJSONProxyPreferenceNonInteractive(t *testing.T) {
	t.Run("direct by default without saved preference", func(t *testing.T) {
		store := newTempStore(t)
		if err := store.Save(config.Config{Version: config.CurrentVersion}); err != nil {
			t.Fatalf("save config: %v", err)
		}
		pref, err := resolveRunJSONProxyPreference(store, "")
		if err != nil {
			t.Fatalf("resolveRunJSONProxyPreference error: %v", err)
		}
		if pref.Enabled {
			t.Fatalf("expected direct default when no proxy preference is saved")
		}
	})

	t.Run("requires explicit choice when profiles exist but no saved preference", func(t *testing.T) {
		store := newTempStore(t)
		if err := store.Save(config.Config{
			Version: config.CurrentVersion,
			Profiles: []config.Profile{{
				ID:   "p1",
				Name: "profile",
				Host: "host",
				Port: 22,
				User: "user",
			}},
		}); err != nil {
			t.Fatalf("save config: %v", err)
		}
		if _, err := resolveRunJSONProxyPreference(store, ""); err == nil {
			t.Fatalf("expected explicit proxy choice error")
		}
	})

	t.Run("explicit profile forces proxy without prompting", func(t *testing.T) {
		store := newTempStore(t)
		if err := store.Save(config.Config{Version: config.CurrentVersion}); err != nil {
			t.Fatalf("save config: %v", err)
		}
		pref, err := resolveRunJSONProxyPreference(store, "named-profile")
		if err != nil {
			t.Fatalf("resolveRunJSONProxyPreference error: %v", err)
		}
		if !pref.Enabled {
			t.Fatalf("expected explicit profile to force proxy")
		}
	})
}

func TestHasExplicitClaudePermissionArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{name: "none", args: []string{"--print"}, want: false},
		{name: "permission mode split", args: []string{"--permission-mode", "default"}, want: true},
		{name: "permission mode inline", args: []string{"--permission-mode=bypassPermissions"}, want: true},
		{name: "dangerous skip", args: []string{"--dangerously-skip-permissions"}, want: true},
		{name: "allow dangerous skip", args: []string{"--allow-dangerously-skip-permissions"}, want: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := hasExplicitClaudePermissionArgs(tc.args); got != tc.want {
				t.Fatalf("hasExplicitClaudePermissionArgs(%v)=%v want %v", tc.args, got, tc.want)
			}
		})
	}
}

func TestRunTargetOnceWithFileRunTargetIO(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script execution on windows")
	}

	dir := t.TempDir()
	shell := requireShell(t)
	stdinPath := filepath.Join(dir, "input.txt")
	stdoutPath := filepath.Join(dir, "nested", "output.txt")
	stderrPath := filepath.Join(dir, "nested", "error.txt")
	if err := os.WriteFile(stdinPath, []byte("hello from stdin"), 0o600); err != nil {
		t.Fatalf("write stdin file: %v", err)
	}

	if err := runTargetOnceWithOptions(
		context.Background(),
		[]string{shell, "-c", "cat; printf 'stderr-line' >&2"},
		"",
		nil,
		nil,
		nil,
		nil,
		runTargetOptions{
			UseProxy:  false,
			PrepareIO: newFileRunTargetIO(stdinPath, stdoutPath, stderrPath),
		},
	); err != nil {
		t.Fatalf("runTargetOnceWithOptions error: %v", err)
	}

	stdoutData, err := os.ReadFile(stdoutPath)
	if err != nil {
		t.Fatalf("read stdout file: %v", err)
	}
	if string(stdoutData) != "hello from stdin" {
		t.Fatalf("unexpected stdout file content %q", string(stdoutData))
	}

	stderrData, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatalf("read stderr file: %v", err)
	}
	if string(stderrData) != "stderr-line" {
		t.Fatalf("unexpected stderr file content %q", string(stderrData))
	}
}

func TestRunTargetOnceWithPrepareIODisablesTTYPreserve(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script execution on windows")
	}

	dir := t.TempDir()
	shell := requireShell(t)
	stdoutPath := filepath.Join(dir, "status.txt")

	withPseudoTTY(t, func() {
		err := runTargetOnceWithOptions(
			context.Background(),
			[]string{shell, "-c", "if [ -t 1 ]; then printf tty; else printf notty; fi"},
			"",
			nil,
			nil,
			nil,
			nil,
			runTargetOptions{
				UseProxy:    false,
				PreserveTTY: true,
				PrepareIO:   newFileRunTargetIO("", stdoutPath, ""),
			},
		)
		if err != nil {
			t.Fatalf("runTargetOnceWithOptions error: %v", err)
		}
	})

	data, err := os.ReadFile(stdoutPath)
	if err != nil {
		t.Fatalf("read status file: %v", err)
	}
	if strings.TrimSpace(string(data)) != "notty" {
		t.Fatalf("expected notty, got %q", string(data))
	}
}

func TestRunTargetOnceWithHeadlessFileRunTargetIOUsesDevNullDefaults(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script execution on windows")
	}

	dir := t.TempDir()
	shell := requireShell(t)
	stdoutPath := filepath.Join(dir, "status.txt")

	withPseudoTTY(t, func() {
		err := runTargetOnceWithOptions(
			context.Background(),
			[]string{shell, "-c", "if [ -t 0 ] || [ -t 1 ] || [ -t 2 ]; then printf tty; else if IFS= read -r line; then printf stdin:%s \"$line\"; else printf stdin:eof; fi; fi"},
			"",
			nil,
			nil,
			nil,
			nil,
			runTargetOptions{
				UseProxy: false,
				PrepareIO: newFileRunTargetIOWithOptions(
					"",
					stdoutPath,
					"",
					fileRunTargetIOOptions{Headless: true},
				),
			},
		)
		if err != nil {
			t.Fatalf("runTargetOnceWithOptions error: %v", err)
		}
	})

	data, err := os.ReadFile(stdoutPath)
	if err != nil {
		t.Fatalf("read status file: %v", err)
	}
	if strings.TrimSpace(string(data)) != "stdin:eof" {
		t.Fatalf("expected stdin:eof, got %q", string(data))
	}
}

func TestRunTargetWithFallbackArchivesRetryOutputs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script execution on windows")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "retry.sh")
	body := "#!/bin/sh\n" +
		"for arg in \"$@\"; do\n" +
		"  if [ \"$arg\" = \"--permission-mode\" ]; then\n" +
		"    printf 'first-out'\n" +
		"    printf 'unknown flag: --permission-mode\\nfirst-err' >&2\n" +
		"    exit 2\n" +
		"  fi\n" +
		"done\n" +
		"printf 'second-out'\n" +
		"printf 'second-err' >&2\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatalf("write retry script: %v", err)
	}

	stdoutPath := filepath.Join(dir, "out.txt")
	stderrPath := filepath.Join(dir, "err.txt")
	err := runTargetWithFallbackWithOptions(
		context.Background(),
		[]string{script, "--permission-mode", "bypassPermissions"},
		"",
		nil,
		nil,
		nil,
		runTargetOptions{
			UseProxy:    false,
			YoloEnabled: true,
			PrepareIO: newFileRunTargetIOWithOptions(
				"",
				stdoutPath,
				stderrPath,
				fileRunTargetIOOptions{ArchiveRetryOutputs: true},
			),
		},
	)
	if err != nil {
		t.Fatalf("runTargetWithFallbackWithOptions error: %v", err)
	}

	stdoutData, err := os.ReadFile(stdoutPath)
	if err != nil {
		t.Fatalf("read stdout file: %v", err)
	}
	if string(stdoutData) != "second-out" {
		t.Fatalf("unexpected final stdout %q", string(stdoutData))
	}

	stderrData, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatalf("read stderr file: %v", err)
	}
	if string(stderrData) != "second-err" {
		t.Fatalf("unexpected final stderr %q", string(stderrData))
	}

	archivedStdout, err := os.ReadFile(stdoutPath + ".attempt-1")
	if err != nil {
		t.Fatalf("read archived stdout file: %v", err)
	}
	if string(archivedStdout) != "first-out" {
		t.Fatalf("unexpected archived stdout %q", string(archivedStdout))
	}

	archivedStderr, err := os.ReadFile(stderrPath + ".attempt-1")
	if err != nil {
		t.Fatalf("read archived stderr file: %v", err)
	}
	if !strings.Contains(string(archivedStderr), "unknown flag: --permission-mode") || !strings.Contains(string(archivedStderr), "first-err") {
		t.Fatalf("unexpected archived stderr %q", string(archivedStderr))
	}
}
