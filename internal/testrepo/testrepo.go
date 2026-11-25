// Package testrepo builds small Git repositories for tests.
package testrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func Write(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// New builds a repository with a feature branch of two commits, the first
// of which has a guide in .self-review. It returns the root and the hashes.
func New(t testing.TB) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	Git(t, dir, "init", "-q", "-b", "main")
	Write(t, filepath.Join(dir, "a.go"), "package a\n")
	Git(t, dir, "add", ".")
	Git(t, dir, "commit", "-q", "-m", "init")
	Git(t, dir, "checkout", "-q", "-b", "feature")
	Write(t, filepath.Join(dir, "a.go"), "package a\n\nfunc A() {}\n")
	Git(t, dir, "commit", "-q", "-am", "add A")
	Write(t, filepath.Join(dir, "b.go"), "package a\n")
	Git(t, dir, "add", ".")
	Git(t, dir, "commit", "-q", "-m", "add b\n\nwhy b exists")
	h1 := Git(t, dir, "rev-parse", "HEAD~1")
	h2 := Git(t, dir, "rev-parse", "HEAD")

	gd := filepath.Join(dir, ".self-review")
	Write(t, filepath.Join(gd, "commits", h1+".md"), `<change-guide title="A を足す" comparison="commit" commit="`+h1+`">
# A を足す

`+"`<excerpt>`"+` は本文。

## 関数

<excerpt path="a.go" start-line="3" end-line="3">
新しい関数。
</excerpt>

## まとめ

以上。
</change-guide>
`)
	return dir, []string{h1, h2}
}
