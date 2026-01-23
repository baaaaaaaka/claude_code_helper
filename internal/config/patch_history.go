package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

const PatchHistoryVersion = 1

type PatchHistoryEntry struct {
	Path          string    `json:"path"`
	SpecsSHA256   string    `json:"specsSha256"`
	PatchedSHA256 string    `json:"patchedSha256"`
	PatchedAt     time.Time `json:"patchedAt"`
}

type PatchHistory struct {
	Version int                 `json:"version"`
	Entries []PatchHistoryEntry `json:"entries"`
}

func (h PatchHistory) IsPatched(path, specsSHA256, patchedSHA256 string) bool {
	for _, entry := range h.Entries {
		if entry.Path == path && entry.SpecsSHA256 == specsSHA256 && entry.PatchedSHA256 == patchedSHA256 {
			return true
		}
	}
	return false
}

func (h *PatchHistory) Remove(path, specsSHA256 string) bool {
	for i := 0; i < len(h.Entries); i++ {
		if h.Entries[i].Path == path && h.Entries[i].SpecsSHA256 == specsSHA256 {
			h.Entries = append(h.Entries[:i], h.Entries[i+1:]...)
			return true
		}
	}
	return false
}

func (h *PatchHistory) Upsert(entry PatchHistoryEntry) {
	for i := range h.Entries {
		if h.Entries[i].Path == entry.Path && h.Entries[i].SpecsSHA256 == entry.SpecsSHA256 {
			h.Entries[i] = entry
			return
		}
	}
	h.Entries = append(h.Entries, entry)
}

type PatchHistoryStore struct {
	mu   sync.Mutex
	path string
	lock *flock.Flock
}

func PatchHistoryPath(configPathOverride string) (string, error) {
	path := configPathOverride
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return "", err
		}
		path = p
	}
	return filepath.Join(filepath.Dir(path), "patch_history.json"), nil
}

func NewPatchHistoryStore(configPathOverride string) (*PatchHistoryStore, error) {
	path, err := PatchHistoryPath(configPathOverride)
	if err != nil {
		return nil, err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	return &PatchHistoryStore{
		path: path,
		lock: flock.New(path + ".lock"),
	}, nil
}

func (s *PatchHistoryStore) Load() (PatchHistory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.lock.Lock(); err != nil {
		return PatchHistory{}, fmt.Errorf("lock patch history: %w", err)
	}
	defer func() { _ = s.lock.Unlock() }()

	return s.loadUnlocked()
}

func (s *PatchHistoryStore) Update(fn func(*PatchHistory) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.lock.Lock(); err != nil {
		return fmt.Errorf("lock patch history: %w", err)
	}
	defer func() { _ = s.lock.Unlock() }()

	history, err := s.loadUnlocked()
	if err != nil {
		return err
	}
	if err := fn(&history); err != nil {
		return err
	}
	return s.saveUnlocked(history)
}

func (s *PatchHistoryStore) loadUnlocked() (PatchHistory, error) {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return PatchHistory{Version: PatchHistoryVersion}, nil
		}
		return PatchHistory{}, fmt.Errorf("read patch history: %w", err)
	}

	var history PatchHistory
	if err := json.Unmarshal(b, &history); err != nil {
		return PatchHistory{}, fmt.Errorf("parse patch history: %w", err)
	}
	if history.Version == 0 {
		history.Version = PatchHistoryVersion
	}
	if history.Version != PatchHistoryVersion {
		return PatchHistory{}, fmt.Errorf("unsupported patch history version %d (expected %d)", history.Version, PatchHistoryVersion)
	}
	return history, nil
}

func (s *PatchHistoryStore) saveUnlocked(history PatchHistory) error {
	if history.Version == 0 {
		history.Version = PatchHistoryVersion
	}
	if history.Version != PatchHistoryVersion {
		return fmt.Errorf("refuse to write patch history version %d (expected %d)", history.Version, PatchHistoryVersion)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	b, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal patch history: %w", err)
	}
	b = append(b, '\n')

	if err := atomicWriteFile(s.path, b, 0o600); err != nil {
		return fmt.Errorf("atomic write patch history: %w", err)
	}
	return nil
}
