package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/config"
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
