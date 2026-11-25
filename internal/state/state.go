// Package state stores review progress (which file changes were viewed) in a
// small JSON file next to the guides.
package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Store is a JSON-backed set of viewed change keys. It is safe for concurrent use.
type Store struct {
	path string
	mu   sync.Mutex
	data fileData
}

type fileData struct {
	Version int                  `json:"version"`
	Viewed  map[string]time.Time `json:"viewed"`
	// ChangesClosed remembers that the Changes list was folded away.
	ChangesClosed bool `json:"changesClosed,omitempty"`
}

// ChangesClosed reports whether the Changes list was last folded away.
func (s *Store) ChangesClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.ChangesClosed
}

// SetChangesClosed remembers whether the Changes list is folded away.
func (s *Store) SetChangesClosed(closed bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.ChangesClosed == closed {
		return nil
	}
	s.data.ChangesClosed = closed
	return s.save()
}

// Open loads the store at path. A missing file is an empty store.
func Open(path string) (*Store, error) {
	s := &Store{path: path, data: fileData{Version: 1, Viewed: map[string]time.Time{}}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return nil, err
	}
	if s.data.Viewed == nil {
		s.data.Viewed = map[string]time.Time{}
	}
	return s, nil
}

// Viewed reports whether key is marked viewed.
func (s *Store) Viewed(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.data.Viewed[key]
	return ok
}

// Set marks or unmarks key and writes the file.
func (s *Store) Set(key string, viewed bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if viewed {
		s.data.Viewed[key] = time.Now().UTC().Truncate(time.Second)
	} else {
		delete(s.data.Viewed, key)
	}
	return s.save()
}

// save writes atomically so a crash never leaves a half-written file.
func (s *Store) save() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".state-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
