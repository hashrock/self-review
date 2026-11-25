package app

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/hashrock/self-review/internal/repo"
	"github.com/hashrock/self-review/internal/review"
)

// load builds the page synchronously, as reload does off the main thread.
func load(t *testing.T, a *App, view string) {
	t.Helper()
	a.view = view
	whole, list, base, _ := a.ws.Views()
	p, err := a.buildPage(view)
	if err != nil {
		t.Fatal(err)
	}
	a.whole, a.list, a.base, a.page = whole, list, base, p
	a.reopenTabs()
}

// TestRender draws the window for the repository in SELF_REVIEW_REPO into
// PNGs in SELF_REVIEW_SHOTS, for looking at the layout without a window
// (in SELF_REVIEW_LANG, English by default).
func TestRender(t *testing.T) {
	root, out := os.Getenv("SELF_REVIEW_REPO"), os.Getenv("SELF_REVIEW_SHOTS")
	if root == "" || out == "" {
		t.Skip("set SELF_REVIEW_REPO and SELF_REVIEW_SHOTS")
	}
	r, err := repo.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := review.Open(r, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	lang := os.Getenv("SELF_REVIEW_LANG")
	if lang == "" {
		lang = "en"
	}
	a := New(ws, "", lang)
	shot := func(name string, tt *ui.Tester) {
		tt.Advance(time.Second)
		tt.Frame()
		f, err := os.Create(filepath.Join(out, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}

	load(t, a, review.ViewBranch)
	tt := ui.NewTester(a.View, 1440, 900)
	shot("branch", tt)

	if len(a.list) > 0 {
		load(t, a, a.list[0].View)
		if files := a.page.loaded.Files; len(files) > 0 {
			a.openFile(files[0].Path, 0, 0)
		}
		tt.Frame()
		shot("commit1", tt)
		if len(a.list) > 1 {
			load(t, a, a.list[1].View)
			a.tabs, a.active = nil, -1
			shot("commit2", tt)
		}
	}
}

// TestFrameTime measures building and painting the window for the guide of
// the repository in SELF_REVIEW_REPO.
func TestFrameTime(t *testing.T) {
	root := os.Getenv("SELF_REVIEW_REPO")
	if root == "" {
		t.Skip("set SELF_REVIEW_REPO")
	}
	r, _ := repo.Open(root)
	ws, _ := review.Open(r, "", "", "")
	a := New(ws, "", "ja")
	start := time.Now()
	load(t, a, review.ViewBranch)
	t.Logf("load: %v, %d blocks", time.Since(start), len(a.page.blocks))
	tt := ui.NewTester(a.View, 1440, 900)
	start = time.Now()
	tt.Frame()
	t.Logf("first frame: %v", time.Since(start))
	start = time.Now()
	for i := 0; i < 20; i++ {
		tt.Wheel(300, 400, 0, -400)
		tt.Frame()
	}
	t.Logf("scroll frame avg: %v", time.Since(start)/20)
	if out := os.Getenv("SELF_REVIEW_SHOTS"); out != "" {
		f, _ := os.Create(filepath.Join(out, "self.png"))
		png.Encode(f, tt.Image())
		f.Close()
	}
}
