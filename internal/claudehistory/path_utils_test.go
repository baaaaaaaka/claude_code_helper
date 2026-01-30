package claudehistory

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPathUtils(t *testing.T) {
	t.Run("empty and missing paths", func(t *testing.T) {
		if isDir("") || isFile(" ") {
			t.Fatalf("expected empty paths to be false")
		}
		if isDir(filepath.Join(t.TempDir(), "missing")) {
			t.Fatalf("expected missing dir to be false")
		}
		if isFile(filepath.Join(t.TempDir(), "missing")) {
			t.Fatalf("expected missing file to be false")
		}
	})

	t.Run("symlink handling", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "file.txt")
		if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		dirLink := filepath.Join(dir, "dir-link")
		fileLink := filepath.Join(dir, "file-link")
		if err := os.Symlink(dir, dirLink); err != nil {
			t.Fatalf("symlink dir: %v", err)
		}
		if err := os.Symlink(file, fileLink); err != nil {
			t.Fatalf("symlink file: %v", err)
		}
		if !isDir(dirLink) {
			t.Fatalf("expected dir symlink to be dir")
		}
		if !isFile(fileLink) {
			t.Fatalf("expected file symlink to be file")
		}
	})

	t.Run("permission errors", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("skip chmod permission test on windows")
		}
		root := t.TempDir()
		locked := filepath.Join(root, "locked")
		if err := os.MkdirAll(locked, 0o700); err != nil {
			t.Fatalf("mkdir locked: %v", err)
		}
		file := filepath.Join(locked, "file.txt")
		if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
			t.Fatalf("write file: %v", err)
		}
		if err := os.Chmod(root, 0); err != nil {
			t.Fatalf("chmod root: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(root, 0o700) })

		if isDir(locked) {
			t.Fatalf("expected isDir to be false on permission error")
		}
		if isFile(file) {
			t.Fatalf("expected isFile to be false on permission error")
		}
	})
}
