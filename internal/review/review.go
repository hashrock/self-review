// Package review gathers what one review shows, independent of how it is
// displayed: the views (whole branch, each commit, uncommitted changes), the
// guide of each, its comparison, changed files and viewed marks.
package review

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashrock/self-review/internal/guide"
	"github.com/hashrock/self-review/internal/repo"
	"github.com/hashrock/self-review/internal/state"
)

// Views.
const (
	ViewBranch   = "branch"
	ViewWorktree = "worktree"
	ViewCommit   = "commit:" // followed by a full commit hash
)

// Workspace is the guide directory of one repository.
//
//	guide.md              the whole branch (or BranchGuide when set)
//	commits/<hash>.md     one commit
//	commits/worktree.md   the uncommitted changes
//	state.json            viewed marks
type Workspace struct {
	Repo        *repo.Repo
	Dir         string
	BranchGuide string
	Base        string
	State       *state.Store
}

// Open prepares a workspace. dir defaults to <root>/.self-review and
// branchGuide to dir/guide.md.
func Open(r *repo.Repo, dir, branchGuide, base string) (*Workspace, error) {
	if dir == "" {
		dir = filepath.Join(r.Root, repo.GuideDir)
	}
	if d, err := filepath.EvalSymlinks(dir); err == nil {
		dir = d
	}
	if branchGuide == "" {
		branchGuide = filepath.Join(dir, "guide.md")
	}
	st, err := state.Open(filepath.Join(dir, "state.json"))
	if err != nil {
		return nil, fmt.Errorf("cannot read state.json: %w", err)
	}
	return &Workspace{Repo: r, Dir: dir, BranchGuide: branchGuide, Base: base, State: st}, nil
}

// Entry is one view in the list of views.
type Entry struct {
	View        string
	Title       string // commit subject; empty for the whole branch and the worktree
	Short       string // short hash for commits
	HasGuide    bool
	Files       int
	ViewedFiles int
}

// Done reports whether every file of the entry is viewed.
func (e Entry) Done() bool { return e.Files > 0 && e.ViewedFiles >= e.Files }

// Views lists the commits of the branch (oldest first) and the uncommitted
// changes, plus the whole branch, and the base used.
func (w *Workspace) Views() (whole Entry, commits []Entry, base string, err error) {
	whole = w.entry(ViewBranch)
	cs, base, err := w.Repo.Commits(w.Base)
	for _, c := range cs {
		e := w.entry(ViewCommit + c.Hash)
		e.Title, e.Short = c.Subject, c.Short
		commits = append(commits, e)
	}
	if dirty, _ := w.Repo.Dirty(); dirty {
		e := w.entry(ViewWorktree)
		commits = append(commits, e)
	}
	return whole, commits, base, err
}

func (w *Workspace) entry(view string) Entry {
	e := Entry{View: view}
	l, err := w.Load(view)
	if err != nil {
		return e
	}
	e.HasGuide = l.Guide != nil
	e.Files = len(l.Files)
	for _, f := range l.Files {
		if f.Viewed {
			e.ViewedFiles++
		}
	}
	return e
}

// File is a changed file with its viewed mark.
type File struct {
	repo.FileStat
	Viewed bool
}

// Loaded is what a view shows.
type Loaded struct {
	View       string
	Guide      *guide.Guide // nil when the view has no guide
	GuidePath  string       // where the guide is, or would be (repository-relative when inside it)
	Comparison *repo.Comparison
	Files      []File
	Problems   []string
	Message    string // commit message body, for commit views without a guide
}

// Load reads a view's guide and changes.
func (w *Workspace) Load(view string) (*Loaded, error) {
	l := &Loaded{View: view}
	gp, exists := w.guidePath(view)
	l.GuidePath = w.rel(gp)
	if exists {
		src, err := os.ReadFile(gp)
		if err != nil {
			return nil, err
		}
		if l.Guide, err = guide.Parse(string(src)); err != nil {
			return nil, fmt.Errorf("%s: %w", l.GuidePath, err)
		}
		for _, p := range l.Guide.Validate() {
			l.Problems = append(l.Problems, p.String())
		}
	}
	cmp, err := w.comparison(view, l.Guide)
	if err != nil {
		return nil, err
	}
	l.Comparison = cmp
	files, err := w.Repo.Changes(cmp)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		l.Files = append(l.Files, File{FileStat: f, Viewed: w.State.Viewed(f.Key)})
	}
	if l.Guide == nil && strings.HasPrefix(view, ViewCommit) {
		msg, _ := w.Repo.CommitMessage(cmp.New)
		_, body, _ := strings.Cut(msg, "\n")
		l.Message = strings.TrimSpace(body)
	}
	return l, nil
}

// Title is the guide's title, else the commit subject; empty for the whole
// branch and the worktree without a guide, which the window names itself.
func (l *Loaded) Title(w *Workspace) string {
	if l.Guide != nil {
		return l.Guide.Title
	}
	if strings.HasPrefix(l.View, ViewCommit) {
		msg, _ := w.Repo.CommitMessage(l.Comparison.New)
		subject, _, _ := strings.Cut(msg, "\n")
		return subject
	}
	return ""
}

func (w *Workspace) guidePath(view string) (string, bool) {
	var p string
	switch {
	case view == ViewBranch:
		p = w.BranchGuide
	case view == ViewWorktree:
		p = filepath.Join(w.Dir, "commits", "worktree.md")
	case strings.HasPrefix(view, ViewCommit):
		hash := strings.TrimPrefix(view, ViewCommit)
		p = filepath.Join(w.Dir, "commits", hash+".md")
		if _, err := os.Stat(p); err != nil {
			ents, _ := os.ReadDir(filepath.Join(w.Dir, "commits"))
			for _, e := range ents {
				name := strings.TrimSuffix(e.Name(), ".md")
				if len(name) >= 7 && name != e.Name() && strings.HasPrefix(hash, name) {
					return filepath.Join(w.Dir, "commits", e.Name()), true
				}
			}
		}
	default:
		return "", false
	}
	_, err := os.Stat(p)
	return p, err == nil
}

func (w *Workspace) rel(p string) string {
	if rel, err := filepath.Rel(w.Repo.Root, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return p
}

func (w *Workspace) comparison(view string, g *guide.Guide) (*repo.Comparison, error) {
	switch {
	case view == ViewBranch:
		sp := repo.Spec{Mode: guide.Branch, Base: w.Base}
		if g != nil {
			sp = repo.Spec{Mode: g.Comparison, Base: g.Base, Prev: g.PreviousVersion, Next: g.NextVersion, Commit: g.Commit}
			if w.Base != "" {
				sp.Base = w.Base
			}
		}
		return w.Repo.Resolve(sp)
	case view == ViewWorktree:
		return w.Repo.Resolve(repo.Spec{Mode: guide.Uncommitted})
	case strings.HasPrefix(view, ViewCommit):
		return w.Repo.Resolve(repo.Spec{Mode: guide.Commit, Commit: strings.TrimPrefix(view, ViewCommit)})
	}
	return nil, fmt.Errorf("unknown view %q", view)
}
