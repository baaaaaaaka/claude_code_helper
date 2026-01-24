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
