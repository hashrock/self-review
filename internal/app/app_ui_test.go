package app

import (
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/hashrock/self-review/internal/config"
	"github.com/hashrock/self-review/internal/repo"
	"github.com/hashrock/self-review/internal/review"
	"github.com/hashrock/self-review/internal/testrepo"
)

func newApp(t *testing.T) (*App, []string) {
	t.Helper()
	dir, hs := testrepo.New(t)
	r, err := repo.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := review.Open(r, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	a := New(ws, "", "ja")
	a.reload()
	return a, hs
}

func click(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatalf("click %q: %v (texts %q)", label, err, tt.Texts())
	}
	tt.Frame()
}

func TestNavigateAndMarkViewed(t *testing.T) {
	a, hs := newApp(t)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()

	// The whole branch has no guide: every changed file's diff shows.
	if !tt.HasText("ブランチ全体の変更") || !tt.HasText("この対象のガイドはまだありません。差分だけを表示しています。") {
		t.Fatalf("branch view: %q", tt.Texts())
	}

	// Commit mode starts at the first commit not fully viewed: the one with a guide.
	click(t, tt, "コミット単位 2")
	if a.view != review.ViewCommit+hs[0] || !tt.HasText("A を足す") || !tt.HasText("新しい関数。") {
		t.Fatalf("commit 1: view %q, texts %q", a.view, tt.Texts())
	}
	// The excerpt is the added function, numbered as in the file.
	if !tt.HasText("@@ -1,1 +1,3 @@") {
		t.Errorf("hunk header missing: %q", tt.Texts())
	}

	click(t, tt, "▶")
	if a.view != review.ViewCommit+hs[1] || !tt.HasText("why b exists") {
		t.Fatalf("commit 2: view %q, texts %q", a.view, tt.Texts())
	}

	// Opening a file from Changes, then marking it viewed.
	click(t, tt, "b.go")
	if a.active != 0 || a.tabs[0].path != "b.go" {
		t.Fatalf("tabs: %+v active %d", a.tabs, a.active)
	}
	if !tt.HasText("×") {
		t.Fatalf("no close mark on the file tab: %q", tt.Texts())
	}
	click(t, tt, "既読")
	f := a.file("b.go")
	if !f.Viewed || !a.ws.State.Viewed(f.Key) {
		t.Fatal("b.go not marked viewed")
	}
	if !a.list[1].Done() {
		t.Fatalf("commit entry not done: %+v", a.list[1])
	}

	// Changes folds away and back.
	click(t, tt, "▾ Changes 1/1")
	if a.changesOpen {
		t.Fatal("changes still open")
	}
	click(t, tt, "◀")
	if a.view != review.ViewCommit+hs[0] {
		t.Fatalf("back: %q", a.view)
	}

	// The switch's label turns commit mode off again.
	click(t, tt, "コミット単位 2")
	if a.view != review.ViewBranch {
		t.Fatalf("switch off: %q", a.view)
	}
}

func TestFileTabsClose(t *testing.T) {
	a, _ := newApp(t)
	a.openFile("a.go", 0, 0)
	a.openFile("b.go", 0, 0)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	if len(a.tabs) != 2 || a.active != 1 {
		t.Fatalf("tabs %d active %d", len(a.tabs), a.active)
	}
	click(t, tt, "a.go") // choose the first tab
	if a.active != 0 {
		t.Fatalf("active %d after choosing a.go", a.active)
	}
	click(t, tt, "×")
	if len(a.tabs) != 1 || a.tabs[0].path != "b.go" {
		t.Fatalf("after close: %+v", a.tabs)
	}
}

func TestEnglishTexts(t *testing.T) {
	dir, _ := testrepo.New(t)
	r, _ := repo.Open(dir)
	ws, _ := review.Open(r, "", "", "")
	a := New(ws, "", "en")
	a.reload()
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	for _, s := range []string{"By commit 2", "Changes on this branch", "Viewed"} {
		if !tt.HasText(s) {
			t.Errorf("missing %q in %q", s, tt.Texts())
		}
	}
}

func TestWelcomeDrop(t *testing.T) {
	dir, _ := testrepo.New(t)
	a := New(nil, "", "en")
	var opened string
	a.openDir = func(d string) {
		opened = d
		r, err := repo.Open(d)
		if err != nil {
			a.errorMsg = err.Error()
			return
		}
		ws, _ := review.Open(r, "", "", "")
		a.setWorkspace(ws, "")
		a.reload()
	}
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	for _, s := range []string{"Drop a Git repository folder here", "Open Folder…"} {
		if !tt.HasText(s) {
			t.Fatalf("welcome missing %q: %q", s, tt.Texts())
		}
	}
	if !tt.DropFiles(720, 450, dir) {
		t.Fatal("drop not taken")
	}
	tt.Frame()
	tt.Frame()
	if opened != dir || a.ws == nil || !tt.HasText("By commit 2") {
		t.Fatalf("after drop: opened %q, texts %q", opened, tt.Texts())
	}
}

func TestWelcomeRecent(t *testing.T) {
	t.Setenv("SELF_REVIEW_CONFIG", t.TempDir()+"/config.json")
	dir, _ := testrepo.New(t)
	other := t.TempDir()
	_ = config.AddRecent(other)
	_ = config.AddRecent(dir)

	a := New(nil, "", "en")
	var opened string
	a.openDir = func(d string) { opened = d }
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	if !tt.HasText("Recent repositories") || !tt.HasText(filepath.Base(dir)) {
		t.Fatalf("recent missing: %q", tt.Texts())
	}
	click(t, tt, filepath.Base(dir))
	if opened != dir {
		t.Fatalf("opened %q, want %q", opened, dir)
	}

	// × forgets the first entry without opening it.
	opened = ""
	click(t, tt, "×")
	if opened != "" || len(a.recent) != 1 || a.recent[0].Path != other {
		t.Fatalf("after forget: opened %q, recent %+v", opened, a.recent)
	}
}
