//go:build windows

package claudehistory

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/baaaaaaaka/claude_code_helper/internal/diskspace"
	"golang.org/x/sys/windows"
)

func atomicWriteFile(path string, data []byte, _ os.FileMode) error {
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

	from, err := windows.UTF16PtrFromString(tmp)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}

	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return fmt.Errorf("replace file: %w", diskspace.AnnotateWriteError(path, err))
	}
	return nil
}
