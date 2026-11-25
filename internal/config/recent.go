package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// MaxRecent is how many recently opened repositories are kept.
const MaxRecent = 10

// Recent is a repository opened in the window.
type Recent struct {
	Path   string    `json:"path"`
	Opened time.Time `json:"opened"`
}

func recentPath() (string, error) {
	p, err := Path()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(p), "recent.json"), nil
}

// RecentRepos lists recently opened repositories, newest first, leaving out
// those no longer on disk.
func RecentRepos() []Recent {
	all, _ := loadRecent()
	var out []Recent
	for _, r := range all {
		if st, err := os.Stat(r.Path); err == nil && st.IsDir() {
			out = append(out, r)
		}
	}
	return out
}

// AddRecent records that the repository at path was opened now.
func AddRecent(path string) error {
	all, _ := loadRecent()
	list := []Recent{{Path: path, Opened: time.Now().UTC().Truncate(time.Second)}}
	for _, r := range all {
		if r.Path != path && len(list) < MaxRecent {
			list = append(list, r)
		}
	}
	return saveRecent(list)
}

// RemoveRecent forgets the repository at path.
func RemoveRecent(path string) error {
	all, _ := loadRecent()
	var list []Recent
	for _, r := range all {
		if r.Path != path {
			list = append(list, r)
		}
	}
	return saveRecent(list)
}

func loadRecent() ([]Recent, error) {
	p, err := recentPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var list []Recent
	return list, json.Unmarshal(b, &list)
}

func saveRecent(list []Recent) error {
	p, err := recentPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if list == nil {
		list = []Recent{}
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0o644)
}
