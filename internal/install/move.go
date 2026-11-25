//go:build !windows

package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Bundle is the .app the running binary is in; ok is false outside one.
func Bundle() (string, bool) {
	exe, inApp := binary()
	if !inApp {
		return "", false
	}
	i := strings.LastIndex(exe, ".app/Contents/MacOS/")
	return exe[:i+len(".app")], true
}

// applicationsDirs are where an app counts as installed.
func applicationsDirs() []string {
	home, _ := os.UserHomeDir()
	return []string{"/Applications", filepath.Join(home, "Applications")}
}

// OutsideApplications reports the bundle when the app runs from somewhere
// other than an Applications folder (Downloads, a disk image, a build
// folder), which is when to offer moving it.
func OutsideApplications() (bundle string, ok bool) {
	b, inApp := Bundle()
	if !inApp {
		return "", false
	}
	for _, d := range applicationsDirs() {
		if filepath.Dir(b) == d {
			return "", false
		}
	}
	return b, true
}

// translocated reports whether macOS runs the app from a read-only copy,
// as it does for a downloaded app opened where it was unpacked.
func translocated(bundle string) bool { return strings.Contains(bundle, "/AppTranslocation/") }

// MoveToApplications copies bundle to /Applications, replacing an older
// copy, and removes the original when it can. It returns the new path.
func MoveToApplications(bundle string) (string, error) {
	dest := filepath.Join("/Applications", filepath.Base(bundle))
	steps := [][]string{
		{"rm", "-rf", dest},
		{"ditto", bundle, dest},
		// The copy is no longer "downloaded": without this, macOS would
		// keep running it from a read-only copy elsewhere.
		{"xattr", "-dr", "com.apple.quarantine", dest},
	}
	if writable("/Applications") {
		for _, s := range steps {
			if out, err := exec.Command(s[0], s[1:]...).CombinedOutput(); err != nil && s[0] != "xattr" {
				return "", fmt.Errorf("%s: %s", strings.Join(s, " "), strings.TrimSpace(string(out)))
			}
		}
	} else {
		var sh []string
		for _, s := range steps {
			q := make([]string, len(s))
			for i, a := range s {
				q[i] = quote(a)
			}
			sh = append(sh, strings.Join(q, " "))
		}
		script := fmt.Sprintf("do shell script %q with administrator privileges", strings.Join(sh, " && ")+"; true")
		if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
			return "", fmt.Errorf("could not copy to %s: %s", dest, strings.TrimSpace(string(out)))
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "Contents", "MacOS")); err != nil {
		return "", fmt.Errorf("copying to %s failed", dest)
	}
	if !translocated(bundle) {
		_ = os.RemoveAll(bundle) // the original; leaving it is harmless
	}
	return dest, nil
}

// Relaunch opens the app at path a moment after this process quits.
func Relaunch(path string) error {
	cmd := exec.Command("/bin/sh", "-c", "sleep 1; exec /usr/bin/open -n "+quote(path))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
