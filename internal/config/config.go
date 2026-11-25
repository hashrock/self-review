// Package config holds the user's settings: the language the review window
// and the guides are written in. Agent-facing text (CLI help, check output,
// the skill) is always English.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Languages the window is translated into.
var Languages = []string{"en", "ja"}

// Auto follows the system's languages.
const Auto = "auto"

// Config is the settings file.
type Config struct {
	// Lang is the reader's language: "auto", or one of Languages.
	Lang string `json:"lang,omitempty"`
}

// Path is where the settings live: <user config dir>/self-review/config.json.
// SELF_REVIEW_CONFIG overrides it.
func Path() (string, error) {
	if p := os.Getenv("SELF_REVIEW_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "self-review", "config.json"), nil
}

// Load reads the settings; a missing file is the defaults.
func Load() (Config, error) {
	c := Config{Lang: Auto}
	p, err := Path()
	if err != nil {
		return c, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", p, err)
	}
	if c.Lang == "" {
		c.Lang = Auto
	}
	return c, nil
}

// Save writes the settings.
func Save(c Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0o644)
}

// ValidLang reports whether v can be set as the language.
func ValidLang(v string) bool {
	if v == Auto {
		return true
	}
	for _, l := range Languages {
		if v == l {
			return true
		}
	}
	return false
}

// ResolvedLang is the language to use: the setting, or with "auto" the first
// of the system's languages that the window is translated into, else English.
func (c Config) ResolvedLang() string {
	if c.Lang != "" && c.Lang != Auto && ValidLang(c.Lang) {
		return c.Lang
	}
	for _, tag := range systemLanguages() {
		parts := strings.FieldsFunc(tag, func(r rune) bool { return r == '-' || r == '_' || r == '.' })
		if len(parts) == 0 {
			continue
		}
		base := strings.ToLower(parts[0])
		for _, l := range Languages {
			if base == l {
				return l
			}
		}
	}
	return "en"
}

// systemLanguages lists the user's languages, most preferred first: on
// macOS those of System Settings, then the locale of the environment.
func systemLanguages() []string {
	var tags []string
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("defaults", "read", "-g", "AppleLanguages").Output(); err == nil {
			for _, f := range strings.FieldsFunc(string(out), func(r rune) bool {
				return r == '(' || r == ')' || r == ',' || r == '"' || r == ' ' || r == '\n'
			}) {
				tags = append(tags, f)
			}
		}
	}
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" && v != "C" && v != "POSIX" {
			tags = append(tags, v)
		}
	}
	return tags
}
