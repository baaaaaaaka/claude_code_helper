package claudehistory

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func collectSessionFiles(dir string, recursive bool) ([]string, error) {
	return collectSessionFilesContext(context.Background(), dir, recursive)
}

func collectSessionFilesContext(ctx context.Context, dir string, recursive bool) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !recursive {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		files := make([]string, 0, len(entries))
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			if isAgentSessionFileName(name) {
				continue
			}
			files = append(files, filepath.Join(dir, name))
		}
		return files, nil
	}

	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), ".jsonl") && !isAgentSessionFileName(d.Name()) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func collectAgentSessionFiles(dir string, recursive bool) ([]string, error) {
	return collectAgentSessionFilesContext(context.Background(), dir, recursive)
}

func collectAgentSessionFilesContext(ctx context.Context, dir string, recursive bool) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !recursive {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		files := make([]string, 0, len(entries))
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if isAgentSessionFileName(name) {
				files = append(files, filepath.Join(dir, name))
			}
		}
		return files, nil
	}

	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if isAgentSessionFileName(d.Name()) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func isAgentSessionFileName(name string) bool {
	return strings.HasPrefix(name, "agent-") && strings.HasSuffix(name, ".jsonl")
}

func resolveSessionFilePath(dir string, sessionID string, recursive bool) (string, error) {
	return resolveSessionFilePathContext(context.Background(), dir, sessionID, recursive)
}

func resolveSessionFilePathContext(ctx context.Context, dir string, sessionID string, recursive bool) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", nil
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	target := sessionID + ".jsonl"
	candidate := filepath.Join(dir, target)
	if isFile(candidate) {
		return candidate, nil
	}
	if !recursive {
		return "", nil
	}

	var found string
	errFound := errors.New("found")
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() == target {
			found = path
			return errFound
		}
		return nil
	})
	if err != nil && !errors.Is(err, errFound) {
		return "", err
	}
	return found, nil
}

func rehydrateSessionsFromFiles(dir string, sessions []Session, recursive bool) ([]Session, int, error) {
	return rehydrateSessionsFromFilesContext(context.Background(), dir, sessions, recursive)
}

func rehydrateSessionsFromFilesContext(ctx context.Context, dir string, sessions []Session, recursive bool) ([]Session, int, error) {
	var firstErr error
	validFiles := 0
	updated := false

	for i := range sessions {
		if err := ctx.Err(); err != nil {
			return sessions, validFiles, err
		}
		sessionID := strings.TrimSpace(sessions[i].SessionID)
		if sessionID == "" {
			continue
		}

		filePath := strings.TrimSpace(sessions[i].FilePath)
		if filePath != "" && !isFile(filePath) {
			filePath = ""
		}
		if filePath == "" {
			resolved, err := resolveSessionFilePathContext(ctx, dir, sessionID, recursive)
			if err != nil && firstErr == nil {
				firstErr = err
			}
			if resolved != "" {
				filePath = resolved
				sessions[i].FilePath = resolved
				updated = true
			}
		}
		if filePath == "" || !isFile(filePath) {
			continue
		}

		validFiles++
		meta, err := readSessionFileMetaCachedContext(ctx, filePath)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("read session %s: %w", filePath, err)
			}
			continue
		}

		if sessions[i].FirstPrompt == "" && meta.FirstPrompt != "" {
			sessions[i].FirstPrompt = meta.FirstPrompt
			updated = true
		}
		if sessions[i].MessageCount == 0 && meta.MessageCount > 0 {
			sessions[i].MessageCount = meta.MessageCount
			updated = true
		}
		if sessions[i].CreatedAt.IsZero() && !meta.CreatedAt.IsZero() {
			sessions[i].CreatedAt = meta.CreatedAt
			updated = true
		}
		if sessions[i].ModifiedAt.IsZero() && !meta.ModifiedAt.IsZero() {
			sessions[i].ModifiedAt = meta.ModifiedAt
			updated = true
		}
		if sessions[i].ProjectPath == "" && meta.ProjectPath != "" {
			sessions[i].ProjectPath = meta.ProjectPath
			updated = true
		} else if sessions[i].ProjectPath != "" && !isDir(sessions[i].ProjectPath) && meta.ProjectPath != "" && isDir(meta.ProjectPath) {
			sessions[i].ProjectPath = meta.ProjectPath
			updated = true
		}
	}

	if updated {
		sort.Slice(sessions, func(i, j int) bool {
			return sessions[i].ModifiedAt.After(sessions[j].ModifiedAt)
		})
	}
	return sessions, validFiles, firstErr
}
