package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
	"github.com/spf13/cobra"
)

func TestSelectProfile(t *testing.T) {
	cfg := config.Config{
		Profiles: []config.Profile{
			{ID: "one", Name: "first"},
			{ID: "two", Name: "second"},
		},
	}

	if _, err := selectProfile(cfg, "one"); err != nil {
		t.Fatalf("expected profile by ID, got error %v", err)
	}
	if _, err := selectProfile(cfg, "second"); err != nil {
		t.Fatalf("expected profile by name, got error %v", err)
	}
	if _, err := selectProfile(cfg, "missing"); err == nil {
		t.Fatalf("expected missing profile error")
	}
	if _, err := selectProfile(cfg, ""); err == nil {
		t.Fatalf("expected error when multiple profiles exist without ref")
	}
}

func TestRunTargetSupervisedSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "ok.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}

	if err := runTargetSupervised(context.Background(), []string{script}, "", nil, nil, nil); err != nil {
		t.Fatalf("runTargetSupervised error: %v", err)
	}
}

func TestRunTargetOnceWithOptionsNoProxyKeepsEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	dir := t.TempDir()
	outFile := filepath.Join(dir, "env.txt")
	script := filepath.Join(dir, "print.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf \"%s\" \"$HTTP_PROXY\" > \"$OUT_FILE\"\n"), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}

	t.Setenv("HTTP_PROXY", "http://example.com")
	opts := runTargetOptions{
		ExtraEnv: []string{"OUT_FILE=" + outFile},
		UseProxy: false,
	}

	if err := runTargetOnceWithOptions(context.Background(), []string{script}, "http://127.0.0.1:9999", nil, nil, &bytes.Buffer{}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("runTargetOnceWithOptions error: %v", err)
	}
	content, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	if got := string(content); got != "http://example.com" {
		t.Fatalf("expected HTTP_PROXY preserved, got %q", got)
	}
}

func TestTerminateProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip process signal test on windows")
	}
	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	if err := terminateProcess(cmd.Process, 100*time.Millisecond); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("terminateProcess error: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected process to exit")
	}
}

func TestRunTargetWithFallbackDisablesYolo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "yolo.sh")
	content := "#!/bin/sh\nfor arg in \"$@\"; do\n  if [ \"$arg\" = \"--permission-mode\" ]; then\n    echo \"unknown flag: --permission-mode\" >&2\n    exit 2\n  fi\n done\nexit 0\n"
	if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}

	disabled := false
	opts := runTargetOptions{
		UseProxy:    false,
		PreserveTTY: false,
		YoloEnabled: true,
		OnYoloFallback: func() error {
			disabled = true
			return nil
		},
	}
	cmdArgs := []string{script, "--permission-mode", "bypassPermissions"}
	if err := runTargetWithFallbackWithOptions(context.Background(), cmdArgs, "", nil, nil, nil, opts); err != nil {
		t.Fatalf("runTargetWithFallbackWithOptions error: %v", err)
	}
	if !disabled {
		t.Fatalf("expected yolo to be disabled on failure")
	}
}

func TestRunTargetWithFallbackUsesLaunchArgsPrefix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	dir := t.TempDir()
	outFile := filepath.Join(dir, "args.txt")
	sourceScript := filepath.Join(dir, "source.sh")
	if err := os.WriteFile(sourceScript, []byte("#!/bin/sh\nexit 9\n"), 0o700); err != nil {
		t.Fatalf("write source script: %v", err)
	}
	launchScript := filepath.Join(dir, "launch.sh")
	content := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + outFile + "\"\n"
	if err := os.WriteFile(launchScript, []byte(content), 0o700); err != nil {
		t.Fatalf("write launch script: %v", err)
	}

	outcome := &patchOutcome{
		LaunchArgsPrefix: []string{launchScript, "--shim"},
	}
	if err := runTargetWithFallbackWithOptions(context.Background(), []string{sourceScript, "--resume", "abc"}, "", nil, outcome, nil, runTargetOptions{UseProxy: false}); err != nil {
		t.Fatalf("runTargetWithFallbackWithOptions error: %v", err)
	}
	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}
	if string(got) != "--shim\n--resume\nabc\n" {
		t.Fatalf("unexpected launch args: %q", string(got))
	}
}

func TestLimitedBufferWrite(t *testing.T) {
	buf := &limitedBuffer{max: 5}
	if _, err := buf.Write([]byte("abc")); err != nil {
		t.Fatalf("write error: %v", err)
	}
	if got := buf.String(); got != "abc" {
		t.Fatalf("expected %q, got %q", "abc", got)
	}
	if _, err := buf.Write([]byte("def")); err != nil {
		t.Fatalf("write error: %v", err)
	}
	if got := buf.String(); got != "bcdef" {
		t.Fatalf("expected %q, got %q", "bcdef", got)
	}

	buf = &limitedBuffer{max: 5}
	_, _ = buf.Write([]byte("0123456789"))
	if got := buf.String(); got != "56789" {
		t.Fatalf("expected %q, got %q", "56789", got)
	}

	buf = &limitedBuffer{max: 0}
	_, _ = buf.Write([]byte("abc"))
	if got := buf.String(); got != "" {
		t.Fatalf("expected empty buffer, got %q", got)
	}
}

func TestRunLikeRejectsMultipleProfiles(t *testing.T) {
	cmd := &cobra.Command{}
	if err := cmd.Flags().Parse([]string{"a", "b"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	root := &rootOptions{}
	if err := runLike(cmd, root, false); err == nil {
		t.Fatalf("expected error for multiple profile args")
	}
}

func TestRunLikePropagatesPatchError(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Parse([]string{"--", "echo"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	root := &rootOptions{
		exePatch: exePatchOptions{
			enabledFlag: true,
			regex1:      "(",
			regex2:      []string{"a"},
			regex3:      []string{"b"},
			replace:     []string{"c"},
		},
	}
	if err := runLike(cmd, root, false); err == nil {
		t.Fatalf("expected runLike to return patch error")
	}
}

func TestRunLikeReleasesPatchPrepMemoryBeforeProfileSelection(t *testing.T) {
	withExePatchTestHooks(t)

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Parse([]string{"--", "echo"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	dir := t.TempDir()
	releaseCalls := 0
	releasePatchPrepMemoryFn = func(cmdArgs []string, opts exePatchOptions, outcome *patchOutcome) {
		releaseCalls++
		if len(cmdArgs) != 1 || cmdArgs[0] != "echo" {
			t.Fatalf("unexpected command args: %#v", cmdArgs)
		}
		if outcome == nil || outcome.TargetPath != filepath.Join(dir, "claude") {
			t.Fatalf("unexpected patch outcome: %#v", outcome)
		}
	}
	maybePatchExecutableCtxFn = func(ctx context.Context, cmdArgs []string, opts exePatchOptions, configPath string, log io.Writer) (*patchOutcome, error) {
		return &patchOutcome{TargetPath: filepath.Join(dir, "claude")}, nil
	}

	root := &rootOptions{
		configPath: filepath.Join(dir, "config.json"),
		exePatch: exePatchOptions{
			enabledFlag:    true,
			policySettings: true,
		},
	}

	if err := runLike(cmd, root, false); err == nil {
		t.Fatalf("expected runLike to fail without profiles")
	}
	if releaseCalls != 1 {
		t.Fatalf("expected one release call, got %d", releaseCalls)
	}
}
