//go:build !windows

package claudehistory

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/baaaaaaaka/claude_code_helper/internal/diskspace"
)

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := diskspace.EnsureAvailable(path, uint64(len(data))); err != nil {
		return err
	}

	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", diskspace.AnnotateWriteError(path, err))
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()

	if err := f.Chmod(perm); err != nil {
		_ = f.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write temp file: %w", diskspace.AnnotateWriteError(tmp, err))
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync temp file: %w", diskspace.AnnotateWriteError(tmp, err))
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", diskspace.AnnotateWriteError(tmp, err))
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename temp file: %w", diskspace.AnnotateWriteError(path, err))
	}
	return nil
}
