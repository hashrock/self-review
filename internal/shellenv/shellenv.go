// Package shellenv reads the user's login shell environment, which apps
// opened from Finder do not get: their PATH is minimal.
package shellenv

import (
	"os"
	"strings"
	"sync"
)

var (
	once sync.Once
	path string
)

// Path is the PATH of the user's login shell, or the process's own when the
// shell cannot tell (and on Windows).
func Path() string {
	once.Do(func() {
		path = shellPath()
		if path == "" {
			path = os.Getenv("PATH")
		}
	})
	return path
}

// Dirs is Path split into directories, in order.
func Dirs() []string {
	var out []string
	for _, d := range strings.Split(Path(), string(os.PathListSeparator)) {
		if d != "" {
			out = append(out, d)
		}
	}
	return out
}

// Environ is env with PATH replaced by the login shell's.
func Environ(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, "PATH=") {
			out = append(out, kv)
		}
	}
	return append(out, "PATH="+Path())
}
