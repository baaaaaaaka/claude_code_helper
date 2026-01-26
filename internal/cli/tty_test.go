//go:build !windows

package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creack/pty"
	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/claudehistory"
	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/config"
)

func withPseudoTTY(t *testing.T, fn func()) {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("pty not available: %v", err)
		return
	}
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	oldStdin := os.Stdin
	os.Stdout = slave
	os.Stderr = slave
	os.Stdin = slave
	t.Cleanup(func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
		os.Stdin = oldStdin
		_ = slave.Close()
		_ = master.Close()
	})
	fn()
}

func writeTTYProbeScript(t *testing.T, outFile string) string {
	t.Helper()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "claude")
	script := fmt.Sprintf(`#!/bin/sh
out=%q
if [ -t 1 ]; then echo "stdout=tty" >> "$out"; else echo "stdout=notty" >> "$out"; fi
if [ -t 2 ]; then echo "stderr=tty" >> "$out"; else echo "stderr=notty" >> "$out"; fi
`, outFile)
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return scriptPath
}

func readTTYStatus(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read tty status: %v", err)
	}
	return string(data)
}

func TestRunClaudeNewSessionPreservesTTY(t *testing.T) {
	outFile := filepath.Join(t.TempDir(), "tty.txt")
	scriptPath := writeTTYProbeScript(t, outFile)
	cwd := t.TempDir()
	store, err := config.NewStore(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	withPseudoTTY(t, func() {
		err := runClaudeNewSession(
			context.Background(),
			&rootOptions{},
			store,
			nil,
			nil,
			cwd,
			scriptPath,
			"",
			false,
			false,
			io.Discard,
		)
		if err != nil {
			t.Fatalf("runClaudeNewSession error: %v", err)
		}
	})

	got := readTTYStatus(t, outFile)
	if !strings.Contains(got, "stdout=tty") || !strings.Contains(got, "stderr=tty") {
		t.Fatalf("expected tty output, got %q", got)
	}
}

func TestRunClaudeSessionPreservesTTY(t *testing.T) {
	outFile := filepath.Join(t.TempDir(), "tty.txt")
	scriptPath := writeTTYProbeScript(t, outFile)
	cwd := t.TempDir()
	store, err := config.NewStore(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	session := claudehistory.Session{
		SessionID:   "sess-tty",
		ProjectPath: cwd,
	}
	project := claudehistory.Project{Path: cwd}

	withPseudoTTY(t, func() {
		err := runClaudeSession(
			context.Background(),
			&rootOptions{},
			store,
			nil,
			nil,
			session,
			project,
			scriptPath,
			"",
			false,
			false,
			io.Discard,
		)
		if err != nil {
			t.Fatalf("runClaudeSession error: %v", err)
		}
	})

	got := readTTYStatus(t, outFile)
	if !strings.Contains(got, "stdout=tty") || !strings.Contains(got, "stderr=tty") {
		t.Fatalf("expected tty output, got %q", got)
	}
}
