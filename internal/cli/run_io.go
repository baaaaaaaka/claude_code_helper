package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/baaaaaaaka/claude_code_helper/internal/diskspace"
)

type fileRunTargetIOOptions struct {
	Headless            bool
	ArchiveRetryOutputs bool
}

func sameCleanPath(left string, right string) bool {
	if strings.TrimSpace(left) == "" || strings.TrimSpace(right) == "" {
		return false
	}
	if leftInfo, err := os.Stat(left); err == nil {
		if rightInfo, err := os.Stat(right); err == nil && os.SameFile(leftInfo, rightInfo) {
			return true
		}
	}
	left = resolvePathForComparison(left)
	right = resolvePathForComparison(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func resolvePathForComparison(path string) string {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}

	current := path
	suffix := []string{}
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			parts := append([]string{resolved}, suffix...)
			return filepath.Clean(filepath.Join(parts...))
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
		suffix = append([]string{filepath.Base(current)}, suffix...)
		current = parent
	}
}

func validateRunTargetIOPaths(stdinPath string, stdoutPath string, stderrPath string) error {
	stdinPath = strings.TrimSpace(stdinPath)
	stdoutPath = strings.TrimSpace(stdoutPath)
	stderrPath = strings.TrimSpace(stderrPath)
	if stdinPath != "" && stdoutPath != "" && sameCleanPath(stdinPath, stdoutPath) {
		return fmt.Errorf("stdinPath and stdoutPath must not point to the same file")
	}
	if stdinPath != "" && stderrPath != "" && sameCleanPath(stdinPath, stderrPath) {
		return fmt.Errorf("stdinPath and stderrPath must not point to the same file")
	}
	if stdoutPath != "" && stderrPath != "" && sameCleanPath(stdoutPath, stderrPath) {
		return fmt.Errorf("stdoutPath and stderrPath must not point to the same file")
	}
	return nil
}

func ensureRunTargetOutputDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

func newFileRunTargetIO(stdinPath string, stdoutPath string, stderrPath string) func() (*runTargetIO, error) {
	return newFileRunTargetIOWithOptions(stdinPath, stdoutPath, stderrPath, fileRunTargetIOOptions{})
}

func newFileRunTargetIOWithOptions(
	stdinPath string,
	stdoutPath string,
	stderrPath string,
	opts fileRunTargetIOOptions,
) func() (*runTargetIO, error) {
	stdinPath = strings.TrimSpace(stdinPath)
	stdoutPath = strings.TrimSpace(stdoutPath)
	stderrPath = strings.TrimSpace(stderrPath)
	if stdinPath == "" && stdoutPath == "" && stderrPath == "" && !opts.Headless {
		return nil
	}
	attempt := 0
	return func() (*runTargetIO, error) {
		attempt++
		if err := validateRunTargetIOPaths(stdinPath, stdoutPath, stderrPath); err != nil {
			return nil, err
		}
		if opts.ArchiveRetryOutputs && attempt > 1 {
			if err := archiveRunTargetOutput(stdoutPath, attempt-1); err != nil {
				return nil, err
			}
			if err := archiveRunTargetOutput(stderrPath, attempt-1); err != nil {
				return nil, err
			}
		}

		files := &runTargetIO{}
		addCloser := func(file *os.File) {
			if file != nil {
				files.Closers = append(files.Closers, file)
			}
		}

		if stdinPath != "" {
			file, err := os.Open(stdinPath)
			if err != nil {
				_ = files.Close()
				return nil, err
			}
			files.Stdin = file
			addCloser(file)
		} else if opts.Headless {
			file, err := os.Open(os.DevNull)
			if err != nil {
				_ = files.Close()
				return nil, err
			}
			files.Stdin = file
			addCloser(file)
		}

		if stdoutPath != "" {
			if err := ensureRunTargetOutputDir(stdoutPath); err != nil {
				_ = files.Close()
				return nil, err
			}
			file, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				_ = files.Close()
				return nil, diskspace.AnnotateWriteError(stdoutPath, err)
			}
			files.Stdout = file
			addCloser(file)
		} else if opts.Headless {
			file, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
			if err != nil {
				_ = files.Close()
				return nil, err
			}
			files.Stdout = file
			addCloser(file)
		}

		if stderrPath != "" {
			if err := ensureRunTargetOutputDir(stderrPath); err != nil {
				_ = files.Close()
				return nil, err
			}
			file, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				_ = files.Close()
				return nil, diskspace.AnnotateWriteError(stderrPath, err)
			}
			files.Stderr = file
			addCloser(file)
		} else if opts.Headless {
			file, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
			if err != nil {
				_ = files.Close()
				return nil, err
			}
			files.Stderr = file
			addCloser(file)
		}

		return files, nil
	}
}

func archiveRunTargetOutput(path string, attempt int) error {
	path = strings.TrimSpace(path)
	if path == "" || attempt <= 0 {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	archivePath, err := reserveRunTargetArchivePath(path, attempt)
	if err != nil {
		return err
	}
	if err := os.Rename(path, archivePath); err != nil {
		return diskspace.AnnotateWriteError(archivePath, err)
	}
	return nil
}

func reserveRunTargetArchivePath(path string, attempt int) (string, error) {
	base := fmt.Sprintf("%s.attempt-%d", path, attempt)
	candidate := base
	for suffix := 1; ; suffix++ {
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
		candidate = fmt.Sprintf("%s.%d", base, suffix)
	}
}

type appendFileWriter struct {
	path string
}

func newAppendFileWriter(path string) io.Writer {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	return &appendFileWriter{path: path}
}

func (w *appendFileWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := ensureRunTargetOutputDir(w.path); err != nil {
		return 0, err
	}
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, diskspace.AnnotateWriteError(w.path, err)
	}
	defer func() { _ = file.Close() }()
	if err := diskspace.EnsureAvailable(w.path, uint64(len(p))); err != nil {
		return 0, err
	}
	n, err := file.Write(p)
	if err != nil {
		return n, diskspace.AnnotateWriteError(w.path, err)
	}
	return n, nil
}
