//go:build !windows

package shellenv

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// shellPath asks the login shell for its PATH; "" when it cannot. -i too:
// version managers such as mise and nvm hook into the interactive files.
func shellPath() string {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/zsh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, sh, "-ilc", `printf '__PATH__%s' "$PATH"`).Output()
	if err != nil {
		return ""
	}
	s := string(out)
	i := strings.LastIndex(s, "__PATH__")
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(s[i+len("__PATH__"):])
}
