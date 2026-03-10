//go:build !windows

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data := []byte("hello")

	if err := atomicWriteFile(path, data, 0o600); err != nil {
		t.Fatalf("atomicWriteFile error: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("expected %q, got %q", data, got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if info.Mode().Perm()&0o777 != 0o600 {
		t.Fatalf("expected perms 600, got %o", info.Mode().Perm()&0o777)
	}
}

func TestAtomicWriteFileReadOnlyDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := atomicWriteFile(path, []byte("data"), 0o600); err == nil {
		t.Fatalf("expected error for read-only dir")
	}
}

func TestAtomicWriteFileOverwritesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatalf("write original file: %v", err)
	}

	if err := atomicWriteFile(path, []byte("new"), 0o644); err != nil {
		t.Fatalf("atomicWriteFile overwrite error: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read overwritten file: %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("expected overwritten content, got %q", got)
	}
}

func TestAtomicWriteFileRenameError(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "config-dir")
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}

	if err := atomicWriteFile(targetDir, []byte("data"), 0o600); err == nil {
		t.Fatalf("expected rename error when target path is a directory")
	}
}
