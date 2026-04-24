package diskspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrInsufficient = errors.New("insufficient disk space")

var availableBytesFn = availableBytes

type InsufficientError struct {
	Path      string
	Required  uint64
	Available uint64
	Cause     error
}

func (e *InsufficientError) Error() string {
	path := strings.TrimSpace(e.Path)
	if path == "" {
		path = "."
	}
	if e.Required > 0 {
		return fmt.Sprintf("insufficient disk space at %s: need %d bytes, available %d bytes", path, e.Required, e.Available)
	}
	return fmt.Sprintf("insufficient disk space at %s", path)
}

func (e *InsufficientError) Unwrap() error {
	if e.Cause != nil {
		return e.Cause
	}
	return ErrInsufficient
}

func (e *InsufficientError) Is(target error) bool {
	return target == ErrInsufficient
}

func EnsureAvailable(path string, requiredBytes uint64) error {
	if requiredBytes == 0 {
		return nil
	}
	dir := existingParentDir(path)
	available, err := availableBytesFn(dir)
	if err != nil {
		return nil
	}
	if available < requiredBytes {
		return &InsufficientError{
			Path:      dir,
			Required:  requiredBytes,
			Available: available,
		}
	}
	return nil
}

func AnnotateWriteError(path string, err error) error {
	if err == nil {
		return nil
	}
	if !IsNoSpace(err) {
		return err
	}
	return &InsufficientError{
		Path:  existingParentDir(path),
		Cause: err,
	}
}

func IsNoSpace(err error) bool {
	if err == nil {
		return false
	}
	if isNoSpaceError(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no space left on device") ||
		strings.Contains(msg, "not enough space") ||
		strings.Contains(msg, "disk full") ||
		strings.Contains(msg, "disk is full") ||
		strings.Contains(msg, "disk quota exceeded")
}

func existingParentDir(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "."
	}
	candidate := filepath.Clean(path)
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate
	}
	dir := filepath.Dir(candidate)
	for {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}
