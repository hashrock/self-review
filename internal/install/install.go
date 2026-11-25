// Package install puts the app's command-line tool on the user's PATH, as a
// symlink to the binary inside the app bundle, so updating the app updates
// the command too.
package install

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hashrock/self-review/internal/shellenv"
)

// Name is the command's name.
const Name = "self-review"

// pathDirs and binary are the login PATH and the running binary; tests
// replace them.
var (
	pathDirs = shellenv.Dirs
	binary   = Binary
)

// Binary is the running binary, symlinks resolved, and whether it is inside
// an app bundle (only then is there something to install).
func Binary() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	return exe, strings.Contains(exe, ".app/Contents/MacOS/")
}

// Status is where the command stands.
type Status struct {
	// Found is the first self-review on the login PATH, "" when none.
	Found string
	// Ours reports whether Found runs this app's binary.
	Ours bool
}

// Check looks the command up on the login shell's PATH.
func Check() Status {
	exe, _ := binary()
	for _, dir := range pathDirs() {
		p := filepath.Join(dir, Name)
		st, err := os.Stat(p)
		if err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
			continue
		}
		real, err := filepath.EvalSymlinks(p)
		return Status{Found: p, Ours: err == nil && real == exe}
	}
	return Status{}
}

// Result is what Install did.
type Result struct {
	Link string // the symlink made
	// Shadowed names another self-review that comes first on PATH, which
	// runs instead of the link; "" when the link is what runs.
	Shadowed string
}

// candidates are where the link may go, most preferred first: directories
// the user owns, then Homebrew's, then /usr/local/bin.
func candidates() []string {
	home, _ := os.UserHomeDir()
	return []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, "bin"),
		"/opt/homebrew/bin",
		"/usr/local/bin",
	}
}

// Target picks the directory for the link: the first candidate on the
// login PATH that the user can write to, else /usr/local/bin, which then
// needs an administrator's password.
func Target() (dir string, admin bool) {
	onPath := map[string]bool{}
	for _, d := range pathDirs() {
		onPath[filepath.Clean(d)] = true
	}
	for _, d := range candidates() {
		if onPath[d] && writable(d) {
			return d, false
		}
	}
	return "/usr/local/bin", !writable("/usr/local/bin")
}

func writable(dir string) bool {
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return false
	}
	f, err := os.CreateTemp(dir, ".self-review-check-*")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}

// ErrNotApp is returned when the binary is not inside the app bundle, as a
// go install'ed one, which is on PATH already.
var ErrNotApp = errors.New("run this from the app (self-review.app/Contents/MacOS/self-review): a binary installed with go install is on your PATH already")

// Install links the command into Target. It replaces a symlink already
// there, but never a regular file.
func Install() (Result, error) {
	exe, inApp := binary()
	if !inApp {
		return Result{}, ErrNotApp
	}
	dir, admin := Target()
	link := filepath.Join(dir, Name)
	if st, err := os.Lstat(link); err == nil && st.Mode()&os.ModeSymlink == 0 {
		return Result{}, fmt.Errorf("%s exists and is not a symlink; remove it first", link)
	}
	if admin {
		if err := linkAsAdmin(exe, link); err != nil {
			return Result{}, err
		}
	} else {
		_ = os.Remove(link)
		if err := os.Symlink(exe, link); err != nil {
			return Result{}, err
		}
	}
	res := Result{Link: link}
	if s := Check(); s.Found != "" && s.Found != link {
		res.Shadowed = s.Found
	}
	return res, nil
}

// linkAsAdmin makes the link with an administrator's password, asked by
// macOS.
func linkAsAdmin(exe, link string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("cannot write to %s: run with sudo", filepath.Dir(link))
	}
	sh := fmt.Sprintf("mkdir -p %s && ln -sf %s %s", quote(filepath.Dir(link)), quote(exe), quote(link))
	script := fmt.Sprintf("do shell script %q with administrator privileges", sh)
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		return fmt.Errorf("could not install to %s: %s", link, strings.TrimSpace(string(out)))
	}
	return nil
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
