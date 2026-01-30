package cli

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

func setStubPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if runtime.GOOS == "windows" {
		t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	}
}

func writeStub(t *testing.T, dir, name, unixBody, windowsBody string) {
	t.Helper()
	var path string
	var body string
	if runtime.GOOS == "windows" {
		path = filepath.Join(dir, name+".cmd")
		body = windowsBody
	} else {
		path = filepath.Join(dir, name)
		body = unixBody
	}
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestSSHOpsProbe(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		dir := t.TempDir()
		writeStub(t, dir, "ssh", "#!/bin/sh\nexit 0\n", "@echo off\r\nexit /b 0\r\n")
		setStubPath(t, dir)

		prof := config.Profile{Host: "host", Port: 22, User: "user"}
		if err := (defaultSSHOps{}).probe(context.Background(), prof, false); err != nil {
			t.Fatalf("probe error: %v", err)
		}
	})

	t.Run("error includes output", func(t *testing.T) {
		dir := t.TempDir()
		writeStub(t, dir, "ssh", "#!/bin/sh\necho \"bad\" >&2\nexit 1\n", "@echo off\r\necho bad 1>&2\r\nexit /b 1\r\n")
		setStubPath(t, dir)

		prof := config.Profile{Host: "host", Port: 22, User: "user"}
		err := (defaultSSHOps{}).probe(context.Background(), prof, false)
		if err == nil || !strings.Contains(err.Error(), "bad") {
			t.Fatalf("expected error with output, got %v", err)
		}
	})
}

func TestGenerateKeypairCreatesFile(t *testing.T) {
	dir := t.TempDir()
	unix := "#!/bin/sh\nkey=\"\"\nwhile [ $# -gt 0 ]; do\n  if [ \"$1\" = \"-f\" ]; then\n    key=\"$2\"\n    shift 2\n    continue\n  fi\n  shift\n done\nif [ -z \"$key\" ]; then\n  exit 1\nfi\nprintf \"key\" > \"$key\"\nprintf \"pub\" > \"$key.pub\"\nexit 0\n"
	win := "@echo off\r\nset key=\r\n:loop\r\nif \"%~1\"==\"\" goto done\r\nif \"%~1\"==\"-f\" (\r\n  set key=%~2\r\n  shift\r\n  shift\r\n  goto loop\r\n)\r\nshift\r\ngoto loop\r\n:done\r\nif \"%key%\"==\"\" exit /b 1\r\necho key> \"%key%\"\r\necho pub> \"%key%.pub\"\r\nexit /b 0\r\n"
	writeStub(t, dir, "ssh-keygen", unix, win)
	setStubPath(t, dir)

	store := newTempStore(t)
	prof := config.Profile{ID: "p1", Name: "user@host", Host: "host", Port: 22, User: "user"}
	keyPath, err := (defaultSSHOps{}).generateKeypair(context.Background(), store, prof)
	if err != nil {
		t.Fatalf("generateKeypair error: %v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("expected key file: %v", err)
	}
	if _, err := os.Stat(keyPath + ".pub"); err != nil {
		t.Fatalf("expected public key file: %v", err)
	}
}

func TestInstallPublicKeyAddsNewline(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "received.pub")
	unix := "#!/bin/sh\ncat - > \"$OUT_FILE\"\nexit 0\n"
	win := "@echo off\r\nmore > \"%OUT_FILE%\"\r\nexit /b 0\r\n"
	writeStub(t, dir, "ssh", unix, win)
	setStubPath(t, dir)
	t.Setenv("OUT_FILE", outPath)

	pubPath := filepath.Join(dir, "id_ed25519.pub")
	if err := os.WriteFile(pubPath, []byte("ssh-ed25519 AAAA"), 0o600); err != nil {
		t.Fatalf("write pub key: %v", err)
	}

	prof := config.Profile{Host: "host", Port: 22, User: "user"}
	if err := (defaultSSHOps{}).installPublicKey(context.Background(), prof, pubPath); err != nil {
		t.Fatalf("installPublicKey error: %v", err)
	}
	content, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(content) != "ssh-ed25519 AAAA\n" {
		t.Fatalf("expected newline-appended key, got %q", string(content))
	}
}

func TestInitProfileInteractiveUsesDefaultOps(t *testing.T) {
	dir := t.TempDir()
	writeStub(t, dir, "ssh", "#!/bin/sh\nexit 0\n", "@echo off\r\nexit /b 0\r\n")
	setStubPath(t, dir)

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	prevStdin := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() { os.Stdin = prevStdin })
	if _, err := writer.Write([]byte("host.example\n22\nalice\n")); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	_ = writer.Close()

	store := newTempStore(t)
	prof, err := initProfileInteractive(context.Background(), store)
	if err != nil {
		t.Fatalf("initProfileInteractive error: %v", err)
	}
	if prof.Name != "alice@host.example" {
		t.Fatalf("expected name alice@host.example, got %q", prof.Name)
	}
}
