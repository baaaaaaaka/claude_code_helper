//go:build !windows

package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceBinaryErrors(t *testing.T) {
	t.Run("source missing", func(t *testing.T) {
		dir := t.TempDir()
		_, err := replaceBinary(filepath.Join(dir, "missing"), filepath.Join(dir, "dest"))
		if err == nil {
			t.Fatalf("expected error for missing source")
		}
	})

	t.Run("dest directory missing", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "src")
		if err := os.WriteFile(src, []byte("data"), 0o600); err != nil {
			t.Fatalf("write src: %v", err)
		}
		dest := filepath.Join(dir, "missing", "dest")
		if _, err := replaceBinary(src, dest); err == nil {
			t.Fatalf("expected error for missing dest dir")
		}
	})
}
