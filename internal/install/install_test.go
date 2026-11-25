package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setup(t *testing.T) (home, app string, path *[]string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	app = filepath.Join(home, "Apps", "self-review.app", "Contents", "MacOS", "self-review")
	if err := os.MkdirAll(filepath.Dir(app), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(app, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	app, _ = filepath.EvalSymlinks(app)
	home, _ = filepath.EvalSymlinks(home)
	t.Setenv("HOME", home)
	dirs := []string{}
	path = &dirs
	oldDirs, oldBin := pathDirs, binary
	pathDirs = func() []string { return *path }
	binary = func() (string, bool) { return app, true }
	t.Cleanup(func() { pathDirs, binary = oldDirs, oldBin })
	return home, app, path
}

func TestInstallIntoLocalBin(t *testing.T) {
	home, app, path := setup(t)
	local := filepath.Join(home, ".local", "bin")
	os.MkdirAll(local, 0o755)
	*path = []string{"/usr/bin", local}

	if s := Check(); s.Found != "" {
		t.Fatalf("found before install: %+v", s)
	}
	res, err := Install()
	if err != nil {
		t.Fatal(err)
	}
	if res.Link != filepath.Join(local, Name) || res.Shadowed != "" {
		t.Fatalf("result: %+v", res)
	}
	if real, _ := filepath.EvalSymlinks(res.Link); real != app {
		t.Fatalf("link points to %q", real)
	}
	if s := Check(); !s.Ours {
		t.Fatalf("check after install: %+v", s)
	}
	// Installing again replaces the link.
	if _, err := Install(); err != nil {
		t.Fatal(err)
	}
}

func TestInstallWarnsWhenShadowed(t *testing.T) {
	home, _, path := setup(t)
	gobin := filepath.Join(home, "go", "bin")
	local := filepath.Join(home, ".local", "bin")
	os.MkdirAll(gobin, 0o755)
	os.MkdirAll(local, 0o755)
	os.WriteFile(filepath.Join(gobin, Name), []byte("#!/bin/sh\n"), 0o755)
	*path = []string{gobin, local}

	res, err := Install()
	if err != nil {
		t.Fatal(err)
	}
	if res.Shadowed != filepath.Join(gobin, Name) {
		t.Fatalf("shadow not reported: %+v", res)
	}
}

func TestInstallNeverReplacesAFile(t *testing.T) {
	home, _, path := setup(t)
	local := filepath.Join(home, ".local", "bin")
	os.MkdirAll(local, 0o755)
	os.WriteFile(filepath.Join(local, Name), []byte("mine"), 0o755)
	*path = []string{local}
	if _, err := Install(); err == nil || !strings.Contains(err.Error(), "not a symlink") {
		t.Fatalf("err = %v", err)
	}
}

func TestNotFromApp(t *testing.T) {
	_, _, _ = setup(t)
	binary = func() (string, bool) { return "/usr/local/go/bin/x", false }
	if _, err := Install(); !errors.Is(err, ErrNotApp) {
		t.Fatalf("err = %v", err)
	}
}

func TestQuote(t *testing.T) {
	if got := quote("a'b c"); got != `'a'\''b c'` {
		t.Fatal(got)
	}
}

func TestOutsideApplications(t *testing.T) {
	home, _, _ := setup(t)
	for exe, want := range map[string]string{
		"/Applications/self-review.app/Contents/MacOS/self-review":                               "",
		home + "/Applications/self-review.app/Contents/MacOS/self-review":                        "",
		home + "/Downloads/self-review.app/Contents/MacOS/self-review":                           home + "/Downloads/self-review.app",
		"/private/var/folders/x/AppTranslocation/1/d/self-review.app/Contents/MacOS/self-review": "/private/var/folders/x/AppTranslocation/1/d/self-review.app",
	} {
		e := exe
		binary = func() (string, bool) { return e, true }
		got, ok := OutsideApplications()
		if got != want || ok != (want != "") {
			t.Errorf("%s: got %q %v", exe, got, ok)
		}
	}
	binary = func() (string, bool) { return "/usr/local/bin/self-review", false }
	if _, ok := OutsideApplications(); ok {
		t.Error("a plain binary is not an app to move")
	}
}
