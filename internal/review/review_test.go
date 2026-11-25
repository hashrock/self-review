package review

import (
	"path/filepath"
	"testing"

	"github.com/hashrock/self-review/internal/repo"
	"github.com/hashrock/self-review/internal/testrepo"
)

func fixture(t *testing.T) (*Workspace, []string) {
	t.Helper()
	dir, hs := testrepo.New(t)
	r, err := repo.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := Open(r, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return ws, hs
}

func TestViewsAndLoad(t *testing.T) {
	ws, hs := fixture(t)

	whole, commits, base, err := ws.Views()
	if err != nil || base != "main" || len(commits) != 2 {
		t.Fatalf("views: %+v %q %v", commits, base, err)
	}
	if whole.HasGuide || whole.Files != 2 || !commits[0].HasGuide || commits[1].HasGuide || commits[1].Files != 1 {
		t.Fatalf("entries: %+v %+v", whole, commits)
	}
	if commits[1].Title != "add b" || commits[1].Short == "" {
		t.Fatalf("commit entry: %+v", commits[1])
	}

	l, err := ws.Load(ViewCommit + hs[0])
	if err != nil {
		t.Fatal(err)
	}
	if l.Guide == nil || l.Guide.Title != "A を足す" || len(l.Problems) != 0 || l.Comparison.New != hs[0] {
		t.Fatalf("commit guide: %+v", l)
	}

	// No guide: the commit body and where a guide would go.
	l, err = ws.Load(ViewCommit + hs[1])
	if err != nil {
		t.Fatal(err)
	}
	if l.Guide != nil || l.Message != "why b exists" || l.Title(ws) != "add b" || l.GuidePath != ".self-review/commits/"+hs[1]+".md" {
		t.Fatalf("fallback: %+v", l)
	}

	if _, err := ws.Load("nope"); err == nil {
		t.Fatal("unknown view loaded")
	}

	// Uncommitted changes appear as the last entry.
	testrepo.Write(t, filepath.Join(ws.Repo.Root, "c.go"), "package a\n")
	if _, commits, _, _ = ws.Views(); len(commits) != 3 || commits[2].View != ViewWorktree {
		t.Fatalf("worktree entry missing: %+v", commits)
	}
}

func TestViewed(t *testing.T) {
	ws, _ := fixture(t)
	l, _ := ws.Load(ViewBranch)
	f := l.Files[0]
	if err := ws.State.Set(f.Key, true); err != nil {
		t.Fatal(err)
	}

	// Marks survive reopening the workspace.
	ws2, err := Open(ws.Repo, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	l, _ = ws2.Load(ViewBranch)
	if !l.Files[0].Viewed || l.Files[1].Viewed {
		t.Fatalf("viewed flags: %+v", l.Files)
	}
	if whole, _, _, _ := ws2.Views(); whole.ViewedFiles != 1 || whole.Done() {
		t.Fatalf("whole entry: %+v", whole)
	}

	// Editing the file makes it unread again.
	testrepo.Write(t, filepath.Join(ws.Repo.Root, f.Path), "package a\n// edited\n")
	l, _ = ws2.Load(ViewBranch)
	if l.Files[0].Viewed {
		t.Fatal("still viewed after the file changed")
	}
}
