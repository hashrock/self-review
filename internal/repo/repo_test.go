package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func lines(spec string) []Line {
	// spec: one char per line: ' ' context, '+' add, '-' del
	var out []Line
	o, n := 1, 1
	for _, c := range spec {
		switch c {
		case ' ':
			out = append(out, Line{Type: Context, Old: o, New: n})
			o++
			n++
		case '+':
			out = append(out, Line{Type: Add, New: n})
			n++
		case '-':
			out = append(out, Line{Type: Del, Old: o})
			o++
		}
	}
	return out
}

func TestExcerpt(t *testing.T) {
	//          idx: 0123456789012345
	ls := lines("     -+     +    ")
	// idx5: del old6, idx6: add new6, idx12: add new12
	tests := []struct {
		name       string
		start, end int
		ctx        int
		want       [][]int
	}{
		{"all changes", 0, 0, 1, [][]int{{4, 5, 6, 7}, {11, 12, 13}}},
		{"replacement kept whole", 6, 6, 0, [][]int{{5, 6}}},
		{"second only", 10, 20, 0, [][]int{{12}}},
		{"no change in range shows code", 1, 2, 3, [][]int{{0, 1}}},
		{"pure deletion by old number", 6, 6, 0, [][]int{{5, 6}}},
	}
	for _, tt := range tests {
		got := Excerpt(ls, tt.start, tt.end, tt.ctx)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}

	// A range inside a run of additions stays that range (as in a new file).
	added := lines("++++++++++")
	if got := Excerpt(added, 4, 5, 1); !reflect.DeepEqual(got, [][]int{{2, 3, 4, 5}}) {
		t.Errorf("range in additions: got %v", got)
	}
	// A replacement block brings the other side's lines.
	repl := lines(" ---+++ ")
	if got := Excerpt(repl, 3, 3, 0); !reflect.DeepEqual(got, [][]int{{1, 2, 3, 4, 5, 6}}) {
		t.Errorf("replacement: got %v", got)
	}

	// A deletion at the end of the file anchors to nothing but its old line.
	del := lines("  --")
	if got := Excerpt(del, 3, 4, 0); !reflect.DeepEqual(got, [][]int{{2, 3}}) {
		t.Errorf("trailing deletion: got %v", got)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRepo(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.txt", "1\n2\n3\n")
	write(t, dir, "gone.txt", "x\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "init")
	git(t, dir, "checkout", "-q", "-b", "feature")
	write(t, dir, "a.txt", "1\nTWO\n3\n4\n")
	git(t, dir, "commit", "-q", "-am", "change")
	os.Remove(filepath.Join(dir, "gone.txt"))
	write(t, dir, "new/b.txt", "b\n")

	r, err := Open(filepath.Join(dir, "new"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := r.Resolve(Spec{Mode: "branch"})
	if err != nil {
		t.Fatal(err)
	}
	files, err := r.Changes(c)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]FileStat{}
	keys := map[string]string{}
	for _, f := range files {
		if f.Key == "" || !strings.HasPrefix(f.Key, f.Path+"@") {
			t.Errorf("bad key %q for %s", f.Key, f.Path)
		}
		keys[f.Path] = f.Key
		f.Key = ""
		got[f.Path] = f
	}
	want := map[string]FileStat{
		"a.txt":     {Path: "a.txt", Status: "modified", Added: 2, Deleted: 1},
		"gone.txt":  {Path: "gone.txt", Status: "deleted", Deleted: 1},
		"new/b.txt": {Path: "new/b.txt", Status: "added", Added: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changes:\n got %+v\nwant %+v", got, want)
	}

	d, err := r.Diff(c, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	wantLines := []Line{
		{Context, 1, 1, "1"}, {Del, 2, 0, "2"}, {Add, 0, 2, "TWO"}, {Context, 3, 3, "3"}, {Add, 0, 4, "4"},
	}
	if !reflect.DeepEqual(d.Lines, wantLines) {
		t.Fatalf("diff lines: %+v", d.Lines)
	}

	u, err := r.Resolve(Spec{Mode: "uncommitted"})
	if err != nil {
		t.Fatal(err)
	}
	d, err = r.Diff(u, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "unchanged" {
		t.Fatalf("uncommitted a.txt status = %s", d.Status)
	}
	if d, _ := r.Diff(u, "nope.txt"); d.Status != "missing" {
		t.Fatalf("missing status = %s", d.Status)
	}
	if _, err := r.Diff(u, "../etc/passwd"); err == nil {
		t.Fatal("path traversal accepted")
	}

	// Keys change when the working tree content changes.
	write(t, dir, "new/b.txt", "b2\n")
	files, _ = r.Changes(c)
	for _, f := range files {
		if f.Path == "new/b.txt" && f.Key == keys["new/b.txt"] {
			t.Error("key did not change after edit")
		}
		if f.Path == "a.txt" && f.Key != keys["a.txt"] {
			t.Error("key of untouched file changed")
		}
	}

	// Commit mode and the commit list.
	write(t, dir, "a.txt", "1\nTWO\n3\n4\n5\n")
	git(t, dir, "commit", "-q", "-am", "second\n\nbody text")
	commits, base, err := r.Commits("")
	if err != nil || base != "main" || len(commits) != 2 || commits[0].Subject != "change" || commits[1].Subject != "second" {
		t.Fatalf("commits = %+v, base %q, err %v", commits, base, err)
	}
	cc, err := r.Resolve(Spec{Mode: "commit", Commit: commits[1].Hash})
	if err != nil {
		t.Fatal(err)
	}
	files, err = r.Changes(cc)
	// commit -a also records the deletion of gone.txt.
	if err != nil || len(files) != 2 || files[0].Path != "a.txt" || files[0].Added != 1 || files[1].Status != "deleted" {
		t.Fatalf("commit changes = %+v, %v", files, err)
	}
	if msg, _ := r.CommitMessage(commits[1].Hash); msg != "second\n\nbody text" {
		t.Fatalf("message = %q", msg)
	}
	// The commit's own diff has its own key, distinct from the branch diff.
	bf, _ := r.Changes(c)
	for _, f := range bf {
		if f.Path == "a.txt" && f.Key == files[0].Key {
			t.Error("commit and branch diffs share a key")
		}
	}
	if dirty, _ := r.Dirty(); !dirty {
		t.Error("untracked file not reported as dirty")
	}

	// A root commit compares against the empty tree.
	root, _ := r.git("rev-list", "--max-parents=0", "HEAD")
	rc, err := r.Resolve(Spec{Mode: "commit", Commit: strings.TrimSpace(root)})
	if err != nil || rc.Old != EmptyTree {
		t.Fatalf("root commit: %+v %v", rc, err)
	}
	if files, err := r.Changes(rc); err != nil || len(files) != 2 {
		t.Fatalf("root changes = %+v %v", files, err)
	}
}
