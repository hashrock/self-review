// Package app shows a review in a native MyGo window: no server, no web page.
// The window's lifetime is the process's: closing it ends the review.
package app

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/hashrock/self-review/internal/guide"
	"github.com/hashrock/self-review/internal/repo"
	"github.com/hashrock/self-review/internal/review"
)

// App is the state the window shows. Its methods other than load run on the
// main thread, in the view function or its event handlers.
type App struct {
	ws   *review.Workspace
	win  *mygo.Window
	lang string
	tx   *text

	view  string // review.View*
	whole review.Entry
	list  []review.Entry // commit mode entries
	base  string

	page     *page // the loaded view; nil until the first load
	loading  bool
	loadSeq  int
	errorMsg string

	split       float32 // left pane's percentage
	changesOpen bool
	tabs        []*tab
	active      int // index into tabs, -1 when none

	md markdown.Options

	// pendingFile is opened on the right once the next load is done.
	pendingFile              string
	pendingStart, pendingEnd int
}

// page is a loaded view, with the diff states its widgets keep.
type page struct {
	loaded *review.Loaded
	title  string
	blocks []block
	added  int
	del    int
}

// kindMessage is a commit message body, shown as plain text.
const kindMessage guide.BlockKind = "message"

type block struct {
	kind   guide.BlockKind
	src    string // markdown, or the excerpt's introduction
	path   string
	label  string
	start  int
	end    int
	status string
	diff   *editor.DiffState
	err    string
	open   bool // excerpt shown (not folded)
}

type tab struct {
	path       string
	start, end int
	diff       *editor.DiffState
	err        string
	scrollTo   int // line (from 1) to bring into view on the next frame, 0 for none
}

// New creates the app for a workspace, showing its texts in lang.
func New(ws *review.Workspace, initialView, lang string) *App {
	if initialView == "" {
		initialView = review.ViewBranch
	}
	return &App{ws: ws, lang: lang, tx: textFor(lang), view: initialView, split: 50, changesOpen: !ws.State.ChangesClosed(), active: -1}
}

// ErrCanceled is what an Opener returns when the user chose nothing to open.
var ErrCanceled = errors.New("canceled")

func anyOpen(apps []*App) bool {
	for _, a := range apps {
		if a.win != nil && !a.win.IsDestroyed() {
			return true
		}
	}
	return false
}

// ChooseRepository asks for a repository folder, as when the app is opened
// from Finder without a command line. It returns ErrCanceled when none is
// chosen.
func ChooseRepository() (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:       "self-review",
		Message:     "Choose a Git repository to review",
		ButtonLabel: "Review",
		Directory:   true,
	})
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", ErrCanceled
	}
	return paths[0], nil
}

// Opener turns a command line (of this launch, or of a later one forwarded
// to this process) into the workspace and view to show.
type Opener func(args []string, workingDir string) (*Request, error)

// Request is what to show: a workspace, the view to start at, the reader's
// language, and optionally a file to open on the right with a line range.
type Request struct {
	Workspace  *review.Workspace
	View, Lang string
	File       string
	Start, End int
}

// Main runs the app: one process for every review window. A later launch
// hands its command line to the running process, which reloads and focuses
// the window of the same guides, or opens a window for others, and exits.
// It returns when the last window is closed.
func Main(args []string, open Opener) error {
	if !mygo.App.RequestSingleInstanceLock() {
		return nil // the running instance took the command line
	}
	var apps []*App
	show := func(args []string, wd string) {
		req, err := open(args, wd)
		if errors.Is(err, ErrCanceled) {
			if !anyOpen(apps) {
				mygo.App.Quit() // launched from Finder and nothing chosen
			}
			return
		}
		if err != nil {
			mygo.Dialog.Error("self-review", err.Error())
			if !anyOpen(apps) {
				mygo.App.Quit()
			}
			return
		}
		for _, a := range apps {
			if a.ws.Dir == req.Workspace.Dir && a.win != nil && !a.win.IsDestroyed() {
				a.win.Update(func() {
					a.pendingFile, a.pendingStart, a.pendingEnd = req.File, req.Start, req.End
					if req.View != "" && req.View != a.view {
						a.selectView(req.View)
					} else {
						a.reload()
					}
				})
				a.win.Show()
				a.win.Focus()
				return
			}
		}
		mygo.App.SetLocale(req.Lang) // MyGo's own controls and menus follow
		a := New(req.Workspace, req.View, req.Lang)
		a.pendingFile, a.pendingStart, a.pendingEnd = req.File, req.Start, req.End
		a.open()
		apps = append(apps, a)
	}
	mygo.App.SetLanguages("en", "ja")
	mygo.App.OnSecondInstance(show)
	wd, _ := os.Getwd()
	mygo.App.WhenReady(func() { show(args, wd) })
	return mygo.App.Run()
}

// open creates the window; call on the main thread.
func (a *App) open() {
	a.win = mygo.NewWindow(mygo.WindowOptions{
		Title:   "self-review — " + a.ws.Repo.Name(),
		Width:   1440,
		Height:  900,
		Content: ui.View(a.View),
	})
	a.win.Focus()
	a.reload()
}

// Reload reads the views and the current view again, as after the guide changed.
func (a *App) Reload() {
	if a.win != nil {
		a.win.Update(a.reload)
	}
}

// reload loads off the main thread and swaps the result in on it.
func (a *App) reload() {
	a.loadSeq++
	seq, view := a.loadSeq, a.view
	a.loading = true
	work := func() {
		whole, list, base, verr := a.ws.Views()
		p, err := a.buildPage(view)
		done := func() {
			if seq != a.loadSeq {
				return
			}
			a.loading = false
			a.whole, a.list, a.base = whole, list, base
			if err != nil {
				a.errorMsg = err.Error()
				return
			}
			a.errorMsg = ""
			if verr != nil {
				a.errorMsg = verr.Error()
			}
			a.page = p
			a.reopenTabs()
			if a.pendingFile != "" {
				a.openFile(a.pendingFile, a.pendingStart, a.pendingEnd)
				a.pendingFile = ""
			}
		}
		if a.win != nil {
			a.win.Update(done)
		} else {
			done()
		}
	}
	if a.win == nil {
		work() // no window (tests): load in place
		return
	}
	go work()
}

// buildPage loads a view and prepares a diff state for every excerpt.
func (a *App) buildPage(view string) (*page, error) {
	l, err := a.ws.Load(view)
	if err != nil {
		return nil, err
	}
	p := &page{loaded: l, title: l.Title(a.ws)}
	if p.title == "" {
		p.title = a.tx.WholeTitle
		if view == review.ViewWorktree {
			p.title = a.tx.WorktreeTitle
		}
	}
	for _, f := range l.Files {
		p.added += f.Added
		p.del += f.Deleted
	}
	diffs := map[string]*repo.FileDiff{}
	diffOf := func(path string) (*repo.FileDiff, error) {
		if d, ok := diffs[path]; ok {
			return d, nil
		}
		d, err := a.ws.Repo.Diff(l.Comparison, path)
		if err == nil {
			diffs[path] = d
		}
		return d, err
	}
	excerpt := func(b guide.Block) block {
		bl := block{kind: guide.KindExcerpt, src: b.Markdown, path: b.Path, start: b.StartLine, end: b.EndLine, open: true}
		fd, err := diffOf(b.Path)
		switch {
		case err != nil:
			bl.err = err.Error()
		case fd.Status == "missing":
			bl.err = a.tx.FileNotFound
		case fd.Binary:
			bl.err = a.tx.Binary
		default:
			bl.status = fd.Status
			groups := repo.Excerpt(fd.Lines, b.StartLine, b.EndLine, repo.ExcerptContext)
			if groups == nil {
				bl.err = a.tx.NoLinesInRange
				break
			}
			bl.diff = excerptDiff(b.Path, fd.Lines, groups)
		}
		return bl
	}

	if l.Guide == nil {
		if l.Message != "" {
			p.blocks = append(p.blocks, block{kind: kindMessage, src: l.Message})
		}
		for _, f := range l.Files {
			p.blocks = append(p.blocks, excerpt(guide.Block{Kind: guide.KindExcerpt, Path: f.Path}))
		}
		return p, nil
	}
	for _, b := range l.Guide.Blocks {
		switch b.Kind {
		case guide.KindMarkdown:
			p.blocks = append(p.blocks, block{kind: b.Kind, src: b.Markdown})
		case guide.KindExcerpt:
			p.blocks = append(p.blocks, excerpt(b))
		case guide.KindRelated:
			p.blocks = append(p.blocks, block{kind: b.Kind, path: b.Path, label: b.Label, start: b.StartLine, end: b.EndLine})
		}
	}
	return p, nil
}

// excerptDiff shows the chosen line groups as hunks of a patch, so the viewer
// numbers them as in the files and highlights them in the file's language.
func excerptDiff(path string, lines []repo.Line, groups [][]int) *editor.DiffState {
	fp := editor.FilePatch{OldName: path, NewName: path}
	for _, g := range groups {
		h := editor.PatchHunk{}
		prevOld, prevNew := 0, 0
		for i := g[0] - 1; i >= 0; i-- {
			if prevOld == 0 && lines[i].Old > 0 {
				prevOld = lines[i].Old
			}
			if prevNew == 0 && lines[i].New > 0 {
				prevNew = lines[i].New
			}
			if prevOld > 0 && prevNew > 0 {
				break
			}
		}
		for _, idx := range g {
			l := lines[idx]
			switch l.Type {
			case repo.Context:
				h.Lines = append(h.Lines, " "+l.Text)
				h.OldLines++
				h.NewLines++
			case repo.Add:
				h.Lines = append(h.Lines, "+"+l.Text)
				h.NewLines++
			case repo.Del:
				h.Lines = append(h.Lines, "-"+l.Text)
				h.OldLines++
			}
			if h.OldStart == 0 && l.Old > 0 {
				h.OldStart = l.Old
			}
			if h.NewStart == 0 && l.New > 0 {
				h.NewStart = l.New
			}
		}
		// A side with no lines in the hunk starts after the line before it, as git writes it.
		if h.OldLines == 0 {
			h.OldStart = prevOld
		}
		if h.NewLines == 0 {
			h.NewStart = prevNew
		}
		fp.Hunks = append(fp.Hunks, h)
	}
	d := &editor.DiffState{Layout: editor.DiffUnified, FitContent: true, Indicators: editor.DiffClassic}
	d.SetPatch(fp)
	return d
}

// ---------- tabs and files ----------

func (a *App) openFile(path string, start, end int) {
	if end == 0 {
		end = start
	}
	for i, t := range a.tabs {
		if t.path == path {
			a.active = i
			if start > 0 {
				t.start, t.end, t.scrollTo = start, end, start
				t.diff.SetSelectedLines(editor.DiffLineRange{Side: editor.DiffNew, Start: start - 1, End: end - 1}, true)
			}
			return
		}
	}
	t := &tab{path: path, start: start, end: end, scrollTo: start}
	a.loadTab(t)
	a.tabs = append(a.tabs, t)
	a.active = len(a.tabs) - 1
}

func (a *App) loadTab(t *tab) {
	t.err, t.diff = "", nil
	if a.page == nil {
		return
	}
	fd, err := a.ws.Repo.Diff(a.page.loaded.Comparison, t.path)
	switch {
	case err != nil:
		t.err = err.Error()
		return
	case fd.Status == "missing":
		t.err = a.tx.NotInComparison
		return
	case fd.Binary:
		t.err = a.tx.Binary
		return
	}
	old, nw := editor.NewBuffer(fd.OldContent), editor.NewBuffer(fd.NewContent)
	if l := editor.LanguageFor(t.path); l != nil {
		old.SetLanguage(l)
		nw.SetLanguage(l)
	}
	t.diff = &editor.DiffState{Old: old, New: nw, Layout: editor.DiffUnified, ExpandUnchanged: true, Indicators: editor.DiffClassic}
	if t.start > 0 {
		t.diff.SetSelectedLines(editor.DiffLineRange{Side: editor.DiffNew, Start: t.start - 1, End: t.end - 1}, true)
	}
}

// reopenTabs reloads open tabs against the newly loaded view.
func (a *App) reopenTabs() {
	for _, t := range a.tabs {
		a.loadTab(t)
	}
}

func (a *App) closeTab(i int) {
	a.tabs = append(a.tabs[:i], a.tabs[i+1:]...)
	if a.active >= len(a.tabs) {
		a.active = len(a.tabs) - 1
	}
}

func (a *App) selectView(view string) {
	if view == a.view {
		return
	}
	a.view = view
	a.tabs, a.active = nil, -1
	a.page = nil
	a.reload()
}

func (a *App) setViewed(f *review.File, viewed bool) {
	if err := a.ws.State.Set(f.Key, viewed); err != nil {
		a.errorMsg = a.tx.CannotSaveViewed + err.Error()
		return
	}
	f.Viewed = viewed
	n := 0
	for _, g := range a.page.loaded.Files {
		if g.Viewed {
			n++
		}
	}
	for i := range a.list {
		if a.list[i].View == a.view {
			a.list[i].ViewedFiles = n
		}
	}
}

func (a *App) file(path string) *review.File {
	if a.page == nil {
		return nil
	}
	for i := range a.page.loaded.Files {
		if a.page.loaded.Files[i].Path == path {
			return &a.page.loaded.Files[i]
		}
	}
	return nil
}

func (a *App) toggleChanges() {
	a.changesOpen = !a.changesOpen
	_ = a.ws.State.SetChangesClosed(!a.changesOpen) // a preference: losing it is harmless
}

func (a *App) commitMode() bool { return a.view != review.ViewBranch }

func (a *App) step(d int) {
	for i, e := range a.list {
		if e.View == a.view && i+d >= 0 && i+d < len(a.list) {
			a.selectView(a.list[i+d].View)
			return
		}
	}
}

// ---------- view ----------

// View builds the window from the state, every frame.
func (a *App) View(c *ui.Context) {
	c.SetTheme(ui.LightTheme())
	t := c.Theme()

	if c.Shortcut(0, ui.KeyR) {
		a.reload()
	}
	if c.Shortcut(0, ui.KeyC) {
		a.toggleChanges()
	}
	if c.Shortcut(0, ui.KeyBracketLeft) {
		a.step(-1)
	}
	if c.Shortcut(0, ui.KeyBracketRight) {
		a.step(1)
	}
	if c.Shortcut(0, ui.KeyV) && a.active >= 0 {
		if f := a.file(a.tabs[a.active].path); f != nil {
			a.setViewed(f, !f.Viewed)
		}
	}

	ui.Column(c).Fill().Background(t.Background).Children(func() {
		a.topBar(c)
		ui.Divider(c)
		ui.SplitPercent(c, &a.split, func() { a.leftPane(c) }, func() { a.rightPane(c) }).Grow(1)
		ui.Divider(c)
		a.statusBar(c)
	})
}

func (a *App) topBar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).PaddingX(12).PaddingY(6).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Children(func() {
			if toggleButton(c.Key("mode-branch"), a.tx.Whole, !a.commitMode()).Clicked() {
				a.selectView(review.ViewBranch)
			}
			label := fmt.Sprintf(a.tx.CommitsMode, len(a.list))
			if toggleButton(c.Key("mode-commits"), label, a.commitMode()).Clicked() && !a.commitMode() && len(a.list) > 0 {
				pick := a.list[0]
				for _, e := range a.list {
					if !e.Done() {
						pick = e
						break
					}
				}
				a.selectView(pick.View)
			}
		})
		title := a.tx.Loading
		label := ""
		if a.page != nil {
			title = a.page.title
			cmp := a.page.loaded.Comparison
			label = a.tx.comparison(cmp.Mode, cmp.Base, cmp.Old, cmp.New, repo.Short)
		}
		ui.Text(c, title).Bold().FontSize(14).Ellipsis("…")
		ui.Text(c, label).TextColor(t.TextMuted).FontSize(12).Ellipsis("…").Grow(1)
		if a.loading {
			ui.Text(c, a.tx.Loading).TextColor(t.TextMuted).FontSize(12)
		}
		if ui.Button(c.Key("reload"), "↻").Tooltip(a.tx.Reload).Clicked() {
			a.reload()
		}
	})
}

// entryTitle names a commit-mode entry: its subject, or the worktree's name.
func (a *App) entryTitle(e review.Entry) string {
	if e.View == review.ViewWorktree {
		return a.tx.WorktreeTitle
	}
	return e.Title
}

// toggleButton is a button drawn as primary while on.
func toggleButton(c *ui.Context, label string, on bool) ui.Element {
	if on {
		return ui.PrimaryButton(c, label)
	}
	return ui.Button(c, label)
}

func (a *App) commitBar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).PaddingX(12).PaddingY(6).Gap(8).AlignItems(ui.Center).Background(t.Surface).Children(func() {
		idx := -1
		for i, e := range a.list {
			if e.View == a.view {
				idx = i
			}
		}
		if ui.Button(c.Key("prev"), "◀").Disabled(idx <= 0).Tooltip(a.tx.Prev).Clicked() {
			a.step(-1)
		}
		ui.Textf(c, "%d / %d", idx+1, len(a.list)).TextColor(t.TextMuted)
		if idx >= 0 {
			e := a.list[idx]
			mark := "  "
			if e.Done() {
				mark = "✓ "
			}
			s := mark + strings.TrimSpace(e.Short+" "+a.entryTitle(e))
			if !e.HasGuide {
				s += a.tx.NoGuideMark
			}
			ui.Text(c, s).Ellipsis("…").Grow(1).PopoverMenu(func(m *ui.Menu) {
				for _, e := range a.list {
					view := e.View
					label := a.entryTitle(e)
					if e.Short != "" {
						label = e.Short + " " + label
					}
					if e.Done() {
						label = "✓ " + label
					}
					if m.Item(label).Radio(view == a.view).Chosen() {
						a.selectView(view)
					}
				}
			})
		}
		if ui.Button(c.Key("next"), "▶").Disabled(idx < 0 || idx >= len(a.list)-1).Tooltip(a.tx.Next).Clicked() {
			a.step(1)
		}
	})
	ui.Divider(c)
}

func (a *App) leftPane(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Children(func() {
		if a.commitMode() {
			a.commitBar(c)
		}
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).PaddingX(28).PaddingY(16).Gap(12).FillWidth().Selectable().Children(func() {
				if a.errorMsg != "" {
					ui.Text(c, a.errorMsg).TextColor(t.Danger)
				}
				if a.page == nil {
					return
				}
				p := a.page
				if p.loaded.Guide == nil {
					ui.Column(c).Padding(12).Gap(4).Background(t.Surface).Radius(6).Children(func() {
						ui.Text(c, a.tx.NoGuide).FontSize(13)
						ui.Text(c, a.tx.GuideGoesAt+p.loaded.GuidePath).FontSize(12).TextColor(t.TextMuted)
					})
					ui.Text(c, p.title).FontSize(22).Bold()
				}
				var related []block
				flush := func() {
					if len(related) == 0 {
						return
					}
					rel := related
					related = nil
					ui.Column(c).Gap(2).Children(func() {
						ui.Text(c, a.tx.Related).FontSize(12).Bold().TextColor(t.TextMuted)
						for i, r := range rel {
							r := r
							loc := fmt.Sprintf("%s:%d", r.path, r.start)
							if r.end > r.start {
								loc += fmt.Sprintf("-%d", r.end)
							}
							ui.Row(c.Key(fmt.Sprintf("rel-%s-%d-%d", r.path, r.start, i))).Gap(8).Children(func() {
								if ui.Text(c, r.label).TextColor(t.Accent).Cursor(ui.CursorPointer).Clicked() {
									a.openFile(r.path, r.start, r.end)
								}
								ui.Text(c, loc).FontSize(12).TextColor(t.TextMuted)
							})
						}
					})
				}
				for i := range p.blocks {
					b := &p.blocks[i]
					if b.kind != guide.KindRelated {
						flush()
					}
					cc := c.Key(fmt.Sprintf("block-%d", i))
					switch b.kind {
					case guide.KindMarkdown:
						markdown.View(cc, b.src, &a.md).FillWidth()
					case kindMessage:
						ui.Text(cc, b.src).TextColor(t.TextMuted).FillWidth()
					case guide.KindExcerpt:
						a.excerptView(cc, b)
					case guide.KindRelated:
						related = append(related, *b)
					}
				}
				flush()
				if len(p.blocks) == 0 {
					ui.Text(c, a.tx.NoChanges).TextColor(t.TextMuted)
				}
			})
		})
	})
}

func (a *App) excerptView(c *ui.Context, b *block) {
	t := c.Theme()
	ui.Column(c).Gap(6).FillWidth().Children(func() {
		if b.src != "" {
			markdown.View(c.Key("intro"), b.src, &a.md).FillWidth()
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			arrow := "▾"
			if !b.open {
				arrow = "▸"
			}
			if ui.Text(c, arrow).TextColor(t.TextMuted).Cursor(ui.CursorPointer).Clicked() {
				b.open = !b.open
			}
			if ui.Text(c, b.path).Font("monospace").FontSize(12).TextColor(t.TextMuted).Cursor(ui.CursorPointer).Tooltip(a.tx.OpenRight).Clicked() {
				a.openFile(b.path, b.start, b.end)
			}
			switch b.status {
			case "added":
				ui.Badge(c, a.tx.Added)
			case "deleted":
				ui.Badge(c, a.tx.Deleted)
			case "unchanged":
				ui.Badge(c, a.tx.Unchanged)
			}
			ui.Spacer(c)
			a.viewedBox(c, b.path)
		})
		if !b.open {
			return
		}
		if b.err != "" {
			ui.Text(c, b.err).TextColor(t.Danger)
			return
		}
		ui.Box(c).FillWidth().Border(1, t.Border).Radius(6).Clip().Children(func() {
			editor.DiffView(c, b.diff).FillWidth()
		})
	})
}

func (a *App) viewedBox(c *ui.Context, path string) {
	f := a.file(path)
	if f == nil {
		return
	}
	v := f.Viewed
	if ui.Checkbox(c.Key("viewed-"+path), &v, a.tx.Viewed).Changed() {
		a.setViewed(f, v)
	}
}

func (a *App) rightPane(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Children(func() {
		// Tab bar: the Changes toggle, then the open files.
		ui.Row(c).Background(t.Surface).AlignItems(ui.Center).Gap(2).PaddingX(4).PaddingY(4).Children(func() {
			n, done := 0, 0
			if a.page != nil {
				n = len(a.page.loaded.Files)
				for _, f := range a.page.loaded.Files {
					if f.Viewed {
						done++
					}
				}
			}
			arrow := "▾"
			if !a.changesOpen {
				arrow = "▸"
			}
			if ui.Button(c.Key("changes"), fmt.Sprintf("%s Changes %d/%d", arrow, done, n)).Tooltip(a.tx.ToggleChanges).Clicked() {
				a.toggleChanges()
			}
			for i, tb := range a.tabs {
				i := i
				name := tb.path[strings.LastIndex(tb.path, "/")+1:]
				ui.Row(c.Key("tab-" + tb.path)).Gap(2).Children(func() {
					if toggleButton(c, name, i == a.active).Tooltip(tb.path).Clicked() {
						a.active = i
					}
					if ui.Button(c, "×").Tooltip(a.tx.Close).Clicked() {
						a.closeTab(i)
					}
				})
			}
		})
		ui.Divider(c)
		ui.Row(c).Grow(1).FillWidth().Children(func() {
			if a.changesOpen {
				a.changesList(c)
			}
			a.fileView(c)
		})
	})
}

func (a *App) changesList(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Width(280).FillHeight().Background(t.Background).Children(func() {
		ui.Column(c).PaddingY(4).FillWidth().Children(func() {
			if a.page == nil {
				return
			}
			if len(a.page.loaded.Files) == 0 {
				ui.Text(c, a.tx.NoChanges).TextColor(t.TextMuted).Padding(8)
				return
			}
			for i := range a.page.loaded.Files {
				f := &a.page.loaded.Files[i]
				activePath := ""
				if a.active >= 0 {
					activePath = a.tabs[a.active].path
				}
				row := ui.Row(c.Key("chg-" + f.Path)).Gap(6).PaddingX(8).PaddingY(3).AlignItems(ui.Center).FillWidth()
				if f.Path == activePath {
					row = row.Background(t.Selection)
				}
				row.Children(func() {
					v := f.Viewed
					if ui.Checkbox(c, &v, "").Tooltip(a.tx.Viewed).Changed() {
						a.setViewed(f, v)
					}
					mark, col := "M", t.Warning
					switch f.Status {
					case "added":
						mark, col = "A", t.Success
					case "deleted":
						mark, col = "D", t.Danger
					}
					ui.Text(c, mark).Font("monospace").FontSize(11).TextColor(col)
					name := ui.Text(c, f.Path).FontSize(12).TruncateStart().Grow(1).Cursor(ui.CursorPointer)
					if f.Viewed {
						name = name.TextColor(t.TextMuted)
					}
					if name.Clicked() {
						a.openFile(f.Path, 0, 0)
					}
					if f.Binary {
						ui.Text(c, "bin").FontSize(11).TextColor(t.TextMuted)
					} else {
						ui.Textf(c, "+%d", f.Added).FontSize(11).Font("monospace").TextColor(t.Success)
						ui.Textf(c, "−%d", f.Deleted).FontSize(11).Font("monospace").TextColor(t.Danger)
					}
				})
			}
		})
	})
	ui.Box(c).Width(1).FillHeight().Background(t.Border)
}

func (a *App) fileView(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).FillHeight().Children(func() {
		if a.active < 0 || a.active >= len(a.tabs) {
			ui.Text(c, a.tx.PickFile).TextColor(t.TextMuted).Padding(24)
			return
		}
		tb := a.tabs[a.active]
		ui.Row(c).PaddingX(12).PaddingY(6).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, tb.path).Font("monospace").FontSize(12).Grow(1).Ellipsis("…")
			a.viewedBox(c.Key("fv"), tb.path)
		})
		ui.Divider(c)
		if tb.err != "" {
			ui.Text(c, tb.err).TextColor(t.TextMuted).Padding(24)
			return
		}
		if tb.diff == nil {
			return
		}
		editor.DiffView(c.Key("file-"+tb.path), tb.diff).Grow(1).FillWidth()
		if tb.scrollTo > 0 && tb.diff.ViewportHeight() > 0 {
			y := tb.diff.LineTop(editor.DiffNew, tb.scrollTo-1) - tb.diff.ViewportHeight()/3
			tb.diff.SetScrollY(max(0, y))
			tb.scrollTo = 0
		}
	})
}

func (a *App) statusBar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).PaddingX(12).PaddingY(4).Gap(16).AlignItems(ui.Center).Children(func() {
		ui.Text(c, a.ws.Repo.Name()+" / "+a.ws.Repo.Branch()).FontSize(12).TextColor(t.TextMuted)
		if a.page != nil {
			ui.Textf(c, "+%d", a.page.added).FontSize(12).TextColor(t.Success)
			ui.Textf(c, "−%d", a.page.del).FontSize(12).TextColor(t.Danger)
			if n := len(a.page.loaded.Problems); n > 0 {
				ui.Textf(c, a.tx.Problems, n).FontSize(12).TextColor(t.Warning).Tooltip(strings.Join(a.page.loaded.Problems, "\n"))
			}
		}
	})
}
