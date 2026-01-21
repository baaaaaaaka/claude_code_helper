package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/gofrs/flock"
)

type Store struct {
	mu   sync.Mutex
	path string
	lock *flock.Flock
}

func DefaultPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("get user config dir: %w", err)
	}
	return filepath.Join(base, "claude-proxy", "config.json"), nil
}

func NewStore(pathOverride string) (*Store, error) {
	path := pathOverride
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	return &Store{
		path: path,
		lock: flock.New(path + ".lock"),
	}, nil
}

func (s *Store) Path() string { return s.path }

func (s *Store) Load() (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.lock.Lock(); err != nil {
		return Config{}, fmt.Errorf("lock config: %w", err)
	}
	defer func() { _ = s.lock.Unlock() }()

	return s.loadUnlocked()
}

func (s *Store) Save(cfg Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.lock.Lock(); err != nil {
		return fmt.Errorf("lock config: %w", err)
	}
	defer func() { _ = s.lock.Unlock() }()

	return s.saveUnlocked(cfg)
}

func (s *Store) Update(fn func(*Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.lock.Lock(); err != nil {
		return fmt.Errorf("lock config: %w", err)
	}
	defer func() { _ = s.lock.Unlock() }()

	cfg, err := s.loadUnlocked()
	if err != nil {
		return err
	}

	if err := fn(&cfg); err != nil {
		return err
	}

	return s.saveUnlocked(cfg)
}

func (s *Store) loadUnlocked() (Config, error) {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{Version: CurrentVersion}, nil
		}
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}

	if cfg.Version == 0 {
		cfg.Version = CurrentVersion
	}
	if cfg.Version != CurrentVersion {
		return Config{}, fmt.Errorf("unsupported config version %d (expected %d)", cfg.Version, CurrentVersion)
	}

	return cfg, nil
}

func (s *Store) saveUnlocked(cfg Config) error {
	if cfg.Version == 0 {
		cfg.Version = CurrentVersion
	}
	if cfg.Version != CurrentVersion {
		return fmt.Errorf("refuse to write config version %d (expected %d)", cfg.Version, CurrentVersion)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	b = append(b, '\n')

	if err := atomicWriteFile(s.path, b, 0o600); err != nil {
		return fmt.Errorf("atomic write config: %w", err)
	}

	return nil
}
