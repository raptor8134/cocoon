//go:build !js

package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Store is a minimal abstraction over where JSON wind files are
// loaded from and saved to. Desktop uses the real filesystem; web
// uses an IndexedDB-backed filesystem (see storage_js.go).
type Store interface {
	// Load returns the raw JSON bytes for the given logical name
	// (for example "test.json").
	Load(name string) ([]byte, error)

	// Save writes the given JSON bytes under the given logical name.
	Save(name string, data []byte) error

	// List returns the set of logical names available in this store.
	List() ([]string, error)
}

var defaultStore Store

// Default returns the process-wide Store instance.
func Default() Store {
	if defaultStore != nil {
		return defaultStore
	}
	defaultStore = &fsStore{root: "winds"}
	return defaultStore
}

// fsStore is a simple filesystem-backed Store for desktop platforms.
type fsStore struct {
	root string
}

func (s *fsStore) fullPath(name string) string {
	// Ensure .json suffix for consistency.
	if !strings.HasSuffix(strings.ToLower(name), ".json") {
		name = name + ".json"
	}
	return filepath.Join(s.root, name)
}

func (s *fsStore) ensureDir() error {
	return os.MkdirAll(s.root, 0o755)
}

func (s *fsStore) Load(name string) ([]byte, error) {
	if err := s.ensureDir(); err != nil {
		return nil, fmt.Errorf("storage: ensureDir: %w", err)
	}
	fp := s.fullPath(name)
	data, err := os.ReadFile(fp)
	if err != nil {
		return nil, fmt.Errorf("storage: read %q: %w", fp, err)
	}
	return data, nil
}

func (s *fsStore) Save(name string, data []byte) error {
	if err := s.ensureDir(); err != nil {
		return fmt.Errorf("storage: ensureDir: %w", err)
	}
	fp := s.fullPath(name)
	if err := os.WriteFile(fp, data, 0o644); err != nil {
		return fmt.Errorf("storage: write %q: %w", fp, err)
	}
	return nil
}

func (s *fsStore) List() ([]string, error) {
	if err := s.ensureDir(); err != nil {
		return nil, fmt.Errorf("storage: ensureDir: %w", err)
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, fmt.Errorf("storage: readdir %q: %w", s.root, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(strings.ToLower(name), ".json") {
			out = append(out, name)
		}
	}
	return out, nil
}

