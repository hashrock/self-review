// Package app shows a review in a native MyGo window: no server, no web page.
// The window's lifetime is the process's: closing it ends the review.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/hashrock/self-review/internal/agent"
	"github.com/hashrock/self-review/internal/config"
	"github.com/hashrock/self-review/internal/guide"
	"github.com/hashrock/self-review/internal/install"
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

	// The agent questions go to, started at the first one, and the
	// conversations with it (of every view; each shows in its own).
	agents        *agentHolder
	agentTitle    string
	threads       []*thread
	threadSeq     int
	questionsOpen bool // the right pane shows Questions rather than a file

	// cli is whether the command-line tool needs installing, for the
	// welcome screen: checked in the background, as it asks the login shell.
	cli cliState

	// recent lists recently opened repositories, for the welcome screen.
	recent []config.Recent

	// openDir opens a folder chosen or dropped in the window.
	openDir func(dir string)

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

// readingTheme is the light theme without its blue: the window is for
// reading, so controls and selections stay in quiet grays.
var readingTheme = func() *ui.Theme {
	th := ui.LightTheme()
	th.Accent = ui.Hex("#52525b")
	th.AccentHover = ui.Hex("#3f3f46")
	th.AccentPressed = ui.Hex("#27272a")
	th.Selection = ui.RGBA(113, 113, 122, 0.20)
	th.Focus = ui.RGBA(113, 113, 122, 0.50)
	return th
}()

// codeTheme colors the diffs: GitHub's code colors, with the selected
// lines in gray rather than blue.
var codeTheme = func() *editor.Theme {
	ct := editor.DefaultTheme(readingTheme)
	ct.Modified = ui.Hex("#a1a1aa")
	return ct
}()

// kindMessage is a commit message body, shown as plain text.
const kindMessage guide.BlockKind = "message"

type block struct {
	kind    guide.BlockKind
	src     string // markdown, or the excerpt's introduction
	path    string
	label   string
	start   int
	end     int
	status  string
	diff    *editor.DiffState
	lines   *lineMap // the diff's lines in the file
	section string   // markdown: the ## section this block starts, if any
	err     string
	open    bool // excerpt shown (not folded)
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
	a := &App{lang: lang, tx: textFor(lang), split: 50, changesOpen: true, active: -1}
	a.setWorkspace(ws, initialView)
	if ws == nil {
		a.recent = config.RecentRepos()
	}
	return a
}

type cliState struct {
	checked, needed bool
	message         string // after installing: where, or what went wrong
	failed          bool
}

// checkCLI finds out whether to offer installing the command, when running
// from the app bundle.
func (a *App) checkCLI() {
	if _, inApp := install.Binary(); !inApp {
		return
	}
	go func() {
		st := install.Check()
		a.update(func() { a.cli.checked, a.cli.needed = true, !st.Ours })
	}()
}

func (a *App) installCLI() {
	res, err := install.Install()
	switch {
	case err != nil:
		a.cli.message, a.cli.failed = err.Error(), true
	case res.Shadowed != "":
		a.cli.message, a.cli.failed = fmt.Sprintf(a.tx.CLIShadowed, res.Link, res.Shadowed), true
	default:
		a.cli.message, a.cli.failed, a.cli.needed = fmt.Sprintf(a.tx.CLIInstalled, res.Link), false, false
	}
}

// setWorkspace points the app at a workspace (nil for the welcome screen).
func (a *App) setWorkspace(ws *review.Workspace, view string) {
	if view == "" {
		view = review.ViewBranch
	}
	a.ws, a.view = ws, view
	a.page, a.tabs, a.active, a.errorMsg = nil, nil, -1, ""
	if ws != nil {
		a.changesOpen = !ws.State.ChangesClosed()
	}
}

func anyOpen(apps []*App) bool {
	for _, a := range apps {
		if a.win != nil && !a.win.IsDestroyed() {
			return true
		}
	}
	return false
}

// Opener turns a command line (of this launch, or of a later one forwarded
// to this process) into what to show. "open --repo <dir>" must work too: the
// window uses it for folders chosen or dropped in it.
type Opener func(args []string, workingDir string) (*Request, error)

// Request is what to show: a workspace (nil for the welcome screen, as when
// the app is opened from Finder), the view to start at, the reader's
// language, and optionally a file to open on the right with a line range.
type Request struct {
	Workspace  *review.Workspace
	View, Lang string
	File       string
	Start, End int
	Agent      agent.Spec // the coding agent questions go to
}

// launcher keeps the process's windows: one per guide directory, plus at
// most one welcome window.
type launcher struct {
	open Opener
	apps []*App
}

// Main runs the app: one process for every review window. A later launch
// hands its command line to the running process, which reloads and focuses
// the window of the same guides, or opens a window for others, and exits.
// It returns when the last window is closed.
func Main(args []string, open Opener) error {
	if !mygo.App.RequestSingleInstanceLock() {
		return nil // the running instance took the command line
	}
	l := &launcher{open: open}
	mygo.App.SetLanguages("en", "ja")
	mygo.App.OnSecondInstance(l.launch)
	mygo.App.OnQuit(func() {
		for _, a := range l.apps {
			if a.agents != nil {
				a.agents.close()
			}
		}
	})
	wd, _ := os.Getwd()
	mygo.App.WhenReady(func() {
		// Opened from Finder: offer to move into Applications first.
		if args == nil && offerMove() {
			return
		}
		l.launch(args, wd)
	})
	return mygo.App.Run()
}

// launch shows what a command line asks for.
func (l *launcher) launch(args []string, wd string) {
	req, err := l.open(args, wd)
	if err != nil {
		mygo.Dialog.Error("self-review", err.Error())
		if !anyOpen(l.apps) {
			mygo.App.Quit()
		}
		return
	}
	l.show(req, nil)
}

// show shows req in the window of the same guides if one is open, else in
// into (a welcome window to turn into a review), else in a new window.
func (l *launcher) show(req *Request, into *App) {
	mygo.App.SetLocale(req.Lang) // MyGo's own controls and menus follow
	if req.Workspace != nil {
		_ = config.AddRecent(req.Workspace.Repo.Root) // a convenience: losing it is harmless
	}
	if req.Workspace == nil {
		for _, a := range l.apps {
			if a.ws == nil && a.win != nil && !a.win.IsDestroyed() {
				a.win.Show()
				a.win.Focus()
				return
			}
		}
		l.newWindow(New(nil, "", req.Lang))
		return
	}
	for _, a := range l.apps {
		if a.ws != nil && a.ws.Dir == req.Workspace.Dir && a.win != nil && !a.win.IsDestroyed() {
			a.pendingFile, a.pendingStart, a.pendingEnd = req.File, req.Start, req.End
			if req.View != "" && req.View != a.view {
				a.selectView(req.View)
			} else {
				a.reload()
			}
			a.win.Show()
			a.win.Focus()
			return
		}
	}
	if into != nil && into.ws == nil {
		into.setWorkspace(req.Workspace, req.View)
		into.setAgent(req.Agent)
		into.pendingFile, into.pendingStart, into.pendingEnd = req.File, req.Start, req.End
		into.win.SetTitle(into.title())
		into.reload()
		return
	}
	a := New(req.Workspace, req.View, req.Lang)
	a.setAgent(req.Agent)
	a.pendingFile, a.pendingStart, a.pendingEnd = req.File, req.Start, req.End
	l.newWindow(a)
}

func (l *launcher) newWindow(a *App) {
	a.openDir = func(dir string) { l.openDir(dir, a) }
	a.open()
	if a.ws == nil {
		a.checkCLI()
	}
	l.apps = append(l.apps, a)
}

// openDir opens a folder chosen or dropped in a's window. A file stands for
// the folder it is in.
func (l *launcher) openDir(dir string, a *App) {
	if st, err := os.Stat(dir); err == nil && !st.IsDir() {
		dir = filepath.Dir(dir)
	}
	req, err := l.open([]string{"open", "--repo", dir}, "")
	if err != nil {
		a.errorMsg = err.Error()
		return
	}
	a.errorMsg = ""
	l.show(req, a)
}

// offerMove asks to move the app into Applications when it runs from
// elsewhere, as from Downloads. It reports whether the app is moving (and
// relaunching from there), when nothing else should be shown.
func offerMove() bool {
	bundle, ok := install.OutsideApplications()
	if !ok {
		return false
	}
	cfg, _ := config.Load()
	if cfg.NoMovePrompt {
		return false
	}
	tx := textFor(cfg.ResolvedLang())
	res, err := mygo.Dialog.Message(mygo.MessageOptions{
		Title:         "self-review",
		Message:       tx.MoveTitle,
		Detail:        tx.MoveDetail,
		Buttons:       []string{tx.MoveButton, tx.NotNow},
		DefaultButton: 0,
		CancelButton:  1,
		CheckboxLabel: tx.DontAskAgain,
	})
	if err != nil {
		return false
	}
	if res.CheckboxChecked {
		cfg.NoMovePrompt = true
		_ = config.Save(cfg)
	}
	if res.Button != 0 {
		return false
	}
	dest, err := install.MoveToApplications(bundle)
	if err != nil {
		mygo.Dialog.Error("self-review", err.Error())
		return false
	}
	if err := install.Relaunch(dest); err != nil {
		mygo.Dialog.Error("self-review", err.Error())
		return false
	}
	mygo.App.Quit()
	return true
}

// chooseFolder asks for a folder; "" when none is chosen.
func (a *App) chooseFolder() string {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:      a.win,
		Title:       "self-review",
		Message:     a.tx.ChooseRepo,
		ButtonLabel: a.tx.Review,
		Directory:   true,
	})
	if err != nil || len(paths) == 0 {
		return ""
	}
	return paths[0]
}

// open creates the window; call on the main thread.
func (a *App) open() {
	a.win = mygo.NewWindow(mygo.WindowOptions{
		Title:   a.title(),
		Width:   1440,
		Height:  900,
		Content: ui.View(a.View),
	})
	a.win.Focus()
	a.win.OnClosed(func() {
		if a.agents != nil {
			a.agents.close() // the agent ends with its window
		}
	})
	if a.ws != nil {
		a.reload()
	}
}

func (a *App) title() string {
	if a.ws == nil {
		return "self-review"
	}
	return "self-review — " + a.ws.Repo.Name()
}

// Reload reads the views and the current view again, as after the guide changed.
func (a *App) Reload() {
	if a.win != nil {
		a.win.Update(a.reload)
	}
}

// reload loads off the main thread and swaps the result in on it.
func (a *App) reload() {
	if a.ws == nil {
		return
	}
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
			bl.diff, bl.lines = excerptDiff(b.Path, fd.Lines, groups)
			a.wireDiff(bl.diff, b.Path, bl.lines)
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
			p.blocks = append(p.blocks, splitSections(b.Markdown)...)
		case guide.KindExcerpt:
			p.blocks = append(p.blocks, excerpt(b))
		case guide.KindRelated:
			p.blocks = append(p.blocks, block{kind: b.Kind, path: b.Path, label: b.Label, start: b.StartLine, end: b.EndLine})
		}
	}
	return p, nil
}

// splitSections cuts markdown at its ## headings, so each section's first
// block knows the section it starts.
func splitSections(src string) []block {
	var out []block
	var cur []string
	title := ""
	flush := func() {
		if text := strings.Trim(strings.Join(cur, "\n"), "\n"); text != "" || title != "" {
			out = append(out, block{kind: guide.KindMarkdown, src: text, section: title})
		}
		cur, title = nil, ""
	}
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(line, "## ") {
			flush()
			title = strings.TrimSpace(line[3:])
		}
		cur = append(cur, line)
	}
	flush()
	return out
}

// excerptDiff shows the chosen line groups as hunks of a patch, so the viewer
// numbers them as in the files and highlights them in the file's language.
func excerptDiff(path string, lines []repo.Line, groups [][]int) (*editor.DiffState, *lineMap) {
	fp := editor.FilePatch{OldName: path, NewName: path}
	m := &lineMap{}
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
			if l.Old > 0 {
				m.old = append(m.old, l.Old)
			}
			if l.New > 0 {
				m.new = append(m.new, l.New)
			}
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
	d := &editor.DiffState{Layout: editor.DiffUnified, FitContent: true, Indicators: editor.DiffClassic, Theme: codeTheme}
	d.SetPatch(fp)
	return d, m
}

// ---------- tabs and files ----------

func (a *App) openFile(path string, start, end int) {
	a.questionsOpen = false
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
	t.diff = &editor.DiffState{Old: old, New: nw, Layout: editor.DiffUnified, ExpandUnchanged: true, Indicators: editor.DiffClassic, Theme: codeTheme}
	a.wireDiff(t.diff, t.path, nil)
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
	c.SetTheme(readingTheme)
	t := c.Theme()
	if a.ws == nil {
		a.welcomeView(c)
		return
	}

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

	root := ui.Column(c).Fill().Background(t.Background)
	// Another repository dropped anywhere opens in its own window.
	if files := root.DroppedFiles(); len(files) > 0 && a.openDir != nil {
		a.openDir(files[0])
	}
	root.Children(func() {
		a.topBar(c)
		ui.Divider(c)
		ui.SplitPercent(c, &a.split, func() { a.leftPane(c) }, func() { a.rightPane(c) }).Grow(1)
		ui.Divider(c)
		a.statusBar(c)
	})
}

// welcomeView is the window of an app opened without a repository: a place
// to drop one, or to choose it.
func (a *App) welcomeView(c *ui.Context) {
	t := c.Theme()
	root := ui.Column(c).Fill().Background(t.Background).Justify(ui.Center).AlignItems(ui.Center).Padding(32)
	files := root.DroppedFiles()
	over := root.FileDragOver()
	if len(files) > 0 && a.openDir != nil {
		a.openDir(files[0])
	}
	root.Children(func() {
		zone := ui.Column(c).Width(520).Padding(40, 32).Gap(14).Radius(16).AlignItems(ui.Center).
			Background(t.Surface).Border(2, t.Border)
		if over {
			zone.Border(2, t.TextMuted).Background(t.SurfaceHover)
		}
		zone.Children(func() {
			ui.Text(c, "self-review").FontSize(26).Bold()
			ui.Text(c, a.tx.DropHere).FontSize(15).TextColor(t.TextMuted).TextAlign(ui.Center)
			if ui.Button(c.Key("open-folder"), a.tx.OpenFolder).Clicked() {
				if dir := a.chooseFolder(); dir != "" && a.openDir != nil {
					a.openDir(dir)
				}
			}
			ui.Text(c, a.tx.OrRun).FontSize(12).TextColor(t.TextMuted).TextAlign(ui.Center)
			if a.errorMsg != "" {
				ui.Text(c, a.errorMsg).FontSize(12).TextColor(t.Danger).TextAlign(ui.Center).MaxWidth(440)
			}
		})
		if len(a.recent) > 0 {
			a.recentList(c)
		}
		if a.cli.needed || a.cli.message != "" {
			ui.Column(c).Width(520).MarginY(8).Gap(6).AlignItems(ui.Center).Children(func() {
				if a.cli.needed {
					ui.Text(c, a.tx.CLIHint).FontSize(12).TextColor(t.TextMuted).TextAlign(ui.Center)
					if ui.Button(c.Key("install-cli"), a.tx.InstallCLI).Clicked() {
						a.installCLI()
					}
				}
				if a.cli.message != "" {
					col := t.TextMuted
					if a.cli.failed {
						col = t.Danger
					}
					ui.Text(c, a.cli.message).FontSize(12).TextColor(col).TextAlign(ui.Center).MaxWidth(500)
				}
			})
		}
	})
}

// recentList lists recently opened repositories: click one to open it, ×
// to forget it.
func (a *App) recentList(c *ui.Context) {
	t := c.Theme()
	home, _ := os.UserHomeDir()
	forget := ""
	ui.Column(c).Width(520).MarginY(24).Gap(2).Children(func() {
		ui.Text(c, a.tx.Recent).FontSize(12).Bold().TextColor(t.TextMuted).PaddingX(12).MarginY(4)
		for _, r := range a.recent {
			path := r.Path
			shown := path
			if home != "" && strings.HasPrefix(path, home+string(filepath.Separator)) {
				shown = "~" + path[len(home):]
			}
			ui.Row(c.Key("recent-" + path)).AlignItems(ui.Center).Children(func() {
				// The × sits beside the clickable row, so forgetting never opens.
				row := ui.Row(c).Gap(10).AlignItems(ui.Center).PaddingX(12).PaddingY(6).Radius(6).Grow(1).Cursor(ui.CursorPointer)
				if row.Hovered() {
					row.Background(t.SurfaceHover)
				}
				if row.Clicked() && a.openDir != nil {
					a.openDir(path)
				}
				row.Children(func() {
					ui.Text(c, filepath.Base(path)).FontSize(13).Bold()
					ui.Text(c, shown).FontSize(12).TextColor(t.TextMuted).SingleLine().TruncateStart().Grow(1).Shrink(1).MinWidth(0)
				})
				if ui.Text(c, "×").FontSize(12).TextColor(t.TextMuted).Padding(6).Cursor(ui.CursorPointer).Tooltip(a.tx.Forget).Clicked() {
					forget = path
				}
			})
		}
	})
	if forget != "" {
		_ = config.RemoveRecent(forget)
		a.recent = config.RecentRepos()
	}
}

func (a *App) topBar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).PaddingX(12).PaddingY(6).Gap(12).AlignItems(ui.Center).Children(func() {
		// One switch: off shows the whole branch, on steps through commits.
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			on := a.commitMode()
			label := fmt.Sprintf(a.tx.CommitsMode, len(a.list))
			sw := ui.Switch(c.Key("mode"), &on).Label(label).Disabled(len(a.list) == 0 && !a.commitMode())
			if sw.Changed() {
				if on {
					a.enterCommitMode()
				} else {
					a.selectView(review.ViewBranch)
				}
			}
			if ui.Text(c, label).FontSize(13).TextColor(t.TextMuted).Cursor(ui.CursorPointer).Clicked() {
				if a.commitMode() {
					a.selectView(review.ViewBranch)
				} else {
					a.enterCommitMode()
				}
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
								if ui.Text(c, r.label).Underline().Cursor(ui.CursorPointer).Clicked() {
									a.openFile(r.path, r.start, r.end)
								}
								ui.Text(c, loc).FontSize(12).TextColor(t.TextMuted)
							})
						}
					})
				}
				section := "" // the ## section being shown, to ask about at its end
				askSection := func() {
					if section == "" {
						return
					}
					title := section
					if ui.Text(c.Key("ask-"+title), "💬 "+a.tx.AskSection).FontSize(12).TextColor(t.TextMuted).Cursor(ui.CursorPointer).Clicked() {
						a.askSection(title)
					}
				}
				for i := range p.blocks {
					b := &p.blocks[i]
					if b.kind != guide.KindRelated {
						flush()
					}
					if b.section != "" {
						askSection()
						section = b.section
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
				askSection()
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
			a.annotate(b.diff, b.path, b.lines)
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

// enterCommitMode shows the first commit not fully viewed, else the first.
func (a *App) enterCommitMode() {
	if len(a.list) == 0 {
		return
	}
	pick := a.list[0]
	for _, e := range a.list {
		if !e.Done() {
			pick = e
			break
		}
	}
	a.selectView(pick.View)
}

// questionsTab is the pinned first tab: the conversations with the agent.
func (a *App) questionsTab(c *ui.Context) {
	t := c.Theme()
	label := a.tx.Questions
	if n := len(a.viewThreads()); n > 0 {
		label = fmt.Sprintf("%s %d", label, n)
	}
	tab := ui.Row(c.Key("questions-tab")).AlignItems(ui.Center).PaddingX(10).PaddingY(4).Radius(6).Cursor(ui.CursorPointer)
	if a.questionsOpen {
		tab.Background(t.Background).Border(1, t.Border)
	} else if tab.Hovered() {
		tab.Background(t.SurfaceHover)
	}
	if tab.Clicked() {
		a.questionsOpen = true
	}
	tab.Children(func() {
		col := t.TextMuted
		if a.questionsOpen {
			col = t.Text
		}
		ui.Text(c, "💬 "+label).FontSize(12).TextColor(col)
	})
}

// fileTabs are the open files, as tabs: the chosen one joins the page
// below it, the others sit on the bar. Each closes with its ×.
func (a *App) fileTabs(c *ui.Context) {
	t := c.Theme()
	if len(a.tabs) == 0 {
		return
	}
	closeAt := -1
	tabs := ui.TabsBase(c.Key("file-tabs"), &a.active, len(a.tabs))
	tabs.List.Gap(2).AlignItems(ui.Center).Children(func() {
		for i, tb := range a.tabs {
			name := tb.path[strings.LastIndex(tb.path, "/")+1:]
			tab := tabs.Tab(i).Row().Gap(6).AlignItems(ui.Center).PaddingX(10).PaddingY(4).Radius(6).Tooltip(tb.path)
			if tab.Clicked() {
				a.questionsOpen = false
			}
			if i == a.active && !a.questionsOpen {
				tab.Background(t.Background).Border(1, t.Border)
			} else if tab.Hovered() {
				tab.Background(t.SurfaceHover)
			}
			tab.Children(func() {
				col := t.TextMuted
				if i == a.active && !a.questionsOpen {
					col = t.Text
				}
				ui.Text(c, name).FontSize(12).TextColor(col)
				if ui.Text(c, "×").FontSize(12).TextColor(t.TextMuted).Cursor(ui.CursorPointer).Tooltip(a.tx.Close).Clicked() {
					closeAt = i
				}
			})
		}
	})
	if closeAt >= 0 {
		a.closeTab(closeAt)
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
			a.questionsTab(c)
			a.fileTabs(c)
		})
		ui.Divider(c)
		ui.Row(c).Grow(1).FillWidth().Children(func() {
			if a.changesOpen {
				a.changesList(c)
			}
			if a.questionsOpen {
				a.questionsPanel(c)
			} else {
				a.fileView(c)
			}
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
					row = row.Background(t.SurfaceHover)
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
		a.annotate(tb.diff, tb.path, nil)
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
