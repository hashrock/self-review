// Command self-review shows change guides with local diffs. Nothing leaves the machine.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hashrock/self-review/internal/app"
	"github.com/hashrock/self-review/internal/config"
	"github.com/hashrock/self-review/internal/guide"
	"github.com/hashrock/self-review/internal/repo"
	"github.com/hashrock/self-review/internal/review"
	"github.com/hashrock/self-review/skill"
)

// version is set at build time (-X main.version=...).
var version = "dev"

const usage = `self-review: read a code change through a guide, with local diffs

Usage:
  self-review <command> [options]

Commands:
  skill     print the agent skill (--install to install it)
  commits   list the commits under review and whether each has a guide
  check     check guides: format, and paths and lines against the repository
  open      open the review window
  config    show or change settings (the reader's language)
  version   print the version
  help      show help (self-review help <command> for a command)

Review scopes:
  whole branch  the diff against the base branch (the whole pull request);
                guide at .self-review/guide.md
  by commit     one commit at a time; guides at .self-review/commits/<hash>.md,
                and .self-review/commits/worktree.md for uncommitted changes
  The window switches between them. Without a guide it shows the diff only.

Typical flow:
  self-review skill --install    # install the skill for Claude Code
  (have the agent run /self-review or /self-review commits)
  self-review open               # open the window; closing it ends the process

Nothing is sent anywhere: no server, no port.
`

var commands = map[string]struct {
	help string
	run  func(args []string) error
}{
	"skill":   {skillHelp, runSkill},
	"check":   {checkHelp, runCheck},
	"commits": {commitsHelp, runCommits},
	"open":    {openHelp, runOpen},
	"config":  {configHelp, runConfig},
}

func main() {
	args := os.Args[1:]
	if launchedFromFinder(args) {
		if err := runFinder(); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch args[0] {
	case "version", "--version":
		fmt.Println("self-review", version)
		return
	case "help", "-h", "--help":
		if len(args) > 1 {
			if c, ok := commands[args[1]]; ok {
				fmt.Print(c.help)
				return
			}
			fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", args[1])
			fmt.Fprint(os.Stderr, usage)
			os.Exit(2)
		}
		fmt.Print(usage)
		return
	}
	c, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", args[0])
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err := c.run(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		var ee exitError
		if errors.As(err, &ee) {
			os.Exit(int(ee))
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type exitError int

func (e exitError) Error() string { return "exit " + strconv.Itoa(int(e)) }

func newFlags(name, help string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(fs.Output(), help) }
	return fs
}

// parseInterspersed lets options appear after positional arguments.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

// ---------- skill ----------

const skillHelp = `Usage: self-review skill [--install] [--dir <dir>]

Prints the skill for coding agents (such as Claude Code) that writes guides.

Options:
  --install     install the skill as a file
  --dir <dir>   where to install it (default: ~/.claude/skills/self-review)
`

func runSkill(args []string) error {
	fs := newFlags("skill", skillHelp)
	install := fs.Bool("install", false, "")
	dir := fs.String("dir", "", "")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if !*install {
		fmt.Print(skill.Content)
		return nil
	}
	if *dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		*dir = filepath.Join(home, ".claude", "skills", "self-review")
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		return err
	}
	p := filepath.Join(*dir, "SKILL.md")
	if err := os.WriteFile(p, []byte(skill.Content), 0o644); err != nil {
		return err
	}
	fmt.Println("installed the skill:", p)
	return nil
}

// ---------- commits ----------

const commitsHelp = `Usage: self-review commits [--base <ref>] [--dir <dir>]

Lists the commits from where HEAD diverged from the base branch to HEAD,
oldest first, and whether each has a guide. Uncommitted changes, if any,
come last as "worktree". Output is tab-separated: hash, guide, subject.

Options:
  --base <ref>   the base branch (default: the default branch)
  --dir <dir>    the guide directory (default: <repository>/.self-review)
  --repo <dir>   the repository (default: the current directory)
`

func runCommits(args []string) error {
	fs := newFlags("commits", commitsHelp)
	base := fs.String("base", "", "")
	dir := fs.String("dir", "", "")
	repoDir := fs.String("repo", ".", "")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	r, err := repo.Open(*repoDir)
	if err != nil {
		return err
	}
	if *dir == "" {
		*dir = filepath.Join(r.Root, repo.GuideDir)
	}
	commits, baseName, err := r.Commits(*base)
	if err != nil {
		return err
	}
	has := func(name string) string {
		if _, err := os.Stat(filepath.Join(*dir, "commits", name+".md")); err == nil {
			return "guide"
		}
		return "no-guide"
	}
	fmt.Fprintf(os.Stderr, "%d commits from the merge base with %s to HEAD\n", len(commits), baseName)
	for _, c := range commits {
		fmt.Printf("%s\t%s\t%s\n", c.Hash, has(c.Hash), c.Subject)
	}
	if dirty, _ := r.Dirty(); dirty {
		fmt.Printf("worktree\t%s\t(uncommitted changes)\n", has("worktree"))
	}
	return nil
}

// ---------- check ----------

const checkHelp = `Usage: self-review check [<guide> | <dir>]

Checks a guide's format, then checks it against the repository:
  - the comparison resolves
  - every path exists in the comparison
  - line numbers are within the files
  - every excerpt has lines to show
  - no excerpt is too long (warns over 30 lines; does not change the exit code)

Given a directory, checks guide.md and commits/*.md in it
(default: <repository>/.self-review). Exits 1 when there are problems.

Options:
  --base <ref>   override the base branch of branch comparisons
  --repo <dir>   the repository (default: the current directory)
`

func runCheck(args []string) error {
	fs := newFlags("check", checkHelp)
	base := fs.String("base", "", "")
	repoDir := fs.String("repo", ".", "")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		fs.Usage()
		return exitError(2)
	}
	r, err := repo.Open(*repoDir)
	if err != nil {
		return err
	}
	target := filepath.Join(r.Root, repo.GuideDir)
	if len(pos) == 1 {
		target = pos[0]
	}

	var paths []string
	if st, err := os.Stat(target); err != nil {
		return err
	} else if st.IsDir() {
		if _, err := os.Stat(filepath.Join(target, "guide.md")); err == nil {
			paths = append(paths, filepath.Join(target, "guide.md"))
		}
		more, _ := filepath.Glob(filepath.Join(target, "commits", "*.md"))
		paths = append(paths, more...)
		if len(paths) == 0 {
			return fmt.Errorf("no guides in %s", target)
		}
	} else {
		paths = []string{target}
	}

	failed := 0
	for _, p := range paths {
		if !checkOne(r, p, *base, len(paths) > 1) {
			failed++
		}
	}
	if failed > 0 {
		if len(paths) > 1 {
			fmt.Printf("\n%d of %d guides have problems\n", failed, len(paths))
		}
		return exitError(1)
	}
	return nil
}

func checkOne(r *repo.Repo, path, base string, many bool) bool {
	label := ""
	if many {
		label = path + ": "
	}
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Println(label + err.Error())
		return false
	}
	g, err := guide.Parse(string(src))
	if err != nil {
		fmt.Println(label + err.Error())
		return false
	}
	problems := g.Validate()

	// Guides under commits/ are named after what they review.
	name := strings.TrimSuffix(filepath.Base(path), ".md")
	if filepath.Base(filepath.Dir(path)) == "commits" {
		switch {
		case name == "worktree" && g.Comparison != guide.Uncommitted:
			problems = append(problems, guide.Problem{Message: `commits/worktree.md must have comparison="uncommitted"`})
		case name != "worktree" && (g.Comparison != guide.Commit || !strings.HasPrefix(g.Commit, name) && !strings.HasPrefix(name, g.Commit)):
			problems = append(problems, guide.Problem{Message: fmt.Sprintf(`commits/%s.md must have comparison="commit" commit="%s"`, name, name)})
		}
	}

	sp := repo.Spec{Mode: g.Comparison, Base: g.Base, Prev: g.PreviousVersion, Next: g.NextVersion, Commit: g.Commit}
	if base != "" {
		sp.Base = base
	}
	var warnings []guide.Problem
	shown := 0
	cmp, err := r.Resolve(sp)
	if err != nil {
		problems = append(problems, guide.Problem{Message: "cannot resolve the comparison: " + err.Error()})
	} else {
		var ps []guide.Problem
		ps, warnings, shown = checkAgainstRepo(r, cmp, g)
		problems = append(problems, ps...)
	}

	for _, w := range warnings {
		fmt.Println(label + "warning: " + w.String())
	}
	if len(problems) == 0 {
		fmt.Printf("%sOK: %s (%s; %d excerpts, %d lines)\n", label, g.Title, cmp, countKind(g, guide.KindExcerpt), shown)
		return true
	}
	for _, p := range problems {
		fmt.Println(label + p.String())
	}
	fmt.Printf("%s%d problems\n", label, len(problems))
	return false
}

// maxExcerptLines is how long an excerpt may show before check warns: a
// guide reads best with the smallest range that reveals the behavior.
const maxExcerptLines = 30

func countKind(g *guide.Guide, k guide.BlockKind) int {
	n := 0
	for _, b := range g.Blocks {
		if b.Kind == k {
			n++
		}
	}
	return n
}

// checkAgainstRepo checks a guide's locations against the repository. It
// returns problems, warnings about length, and the lines the excerpts show.
func checkAgainstRepo(r *repo.Repo, cmp *repo.Comparison, g *guide.Guide) (ps, warnings []guide.Problem, shown int) {
	diffs := map[string]*repo.FileDiff{}
	errs := map[string]error{}
	for _, b := range g.Blocks {
		if b.Kind == guide.KindMarkdown || b.Path == "" {
			continue
		}
		add := func(f string, a ...any) {
			ps = append(ps, guide.Problem{Line: b.Line, Message: b.Path + ": " + fmt.Sprintf(f, a...)})
		}
		fd, seen := diffs[b.Path]
		if !seen {
			fd, errs[b.Path] = r.Diff(cmp, b.Path)
			diffs[b.Path] = fd
		}
		if err := errs[b.Path]; err != nil {
			add("%v", err)
			continue
		}
		if fd.Status == "missing" {
			add("not in either version of the comparison")
			continue
		}
		if fd.Binary {
			continue
		}
		maxLine := max(len(repo.SplitLines(fd.OldContent)), len(repo.SplitLines(fd.NewContent)))
		if b.StartLine > maxLine {
			add("start-line %d is past the end of the file (%d lines)", b.StartLine, maxLine)
			continue
		}
		if b.EndLine > maxLine {
			add("end-line %d is past the end of the file (%d lines)", b.EndLine, maxLine)
		}
		if b.Kind == guide.KindExcerpt {
			groups := repo.Excerpt(fd.Lines, b.StartLine, b.EndLine, repo.ExcerptContext)
			n := repo.ExcerptLength(groups)
			shown += n
			switch {
			case fd.Status == "unchanged" && b.StartLine == 0 && b.EndLine == 0:
				add("this file has no changes (give a line range to show unchanged code)")
			case len(groups) == 0:
				add("no lines to show in this range")
			case n > maxExcerptLines:
				hint := "Narrow the range, split the excerpt, or point to it with a related location."
				if b.StartLine == 0 && b.EndLine == 0 {
					hint = "Without a line range it shows every change in the file. " + hint
				}
				warnings = append(warnings, guide.Problem{Line: b.Line, Message: fmt.Sprintf("%s: the excerpt shows %d lines (aim for at most %d). %s", b.Path, n, maxExcerptLines, hint)})
			}
		}
	}
	return ps, warnings, shown
}

// ---------- open ----------

const openHelp = `Usage: self-review open [<dir> | <guide>] [--view <view>] [--base <ref>]

Opens the review window. No server, no port: closing the window ends the
process.

The window runs in a process of its own and this command returns at once,
so an agent is not kept waiting. If a window for the same guides is open,
it reloads and comes to the front instead.

  <dir>     the guide directory (default: <repository>/.self-review)
            guide.md            guide for the whole branch
            commits/<hash>.md   guide for one commit
            commits/worktree.md guide for the uncommitted changes
            state.json          viewed marks (created as needed)
  <guide>   a file: use it as the whole-branch guide, and its directory
            as <dir>

Without a guide the window shows the diff only. After editing a guide,
run open again, or press R in the window.

The window shows its texts in the reader's language (self-review config).

Keys in the window:
  R  reload           C  show/hide Changes   V  mark the open file viewed
  [  previous commit  ]  next commit

Options:
  --view <view>  what to show first: branch, worktree or commit:<hash>
  --file <f>     open a file on the right: path, path:line or path:start-end
  --base <ref>   override the base branch
  --repo <dir>   the repository (default: the current directory)
  --wait         run the window in this process, until it is closed
`

func runOpen(args []string) error {
	wait := false
	rest := []string{}
	for _, a := range args {
		if a == "--wait" || a == "-wait" {
			wait = true
		} else {
			rest = append(rest, a)
		}
	}
	// Validate the command line here, so mistakes show in the terminal.
	if _, err := openWorkspace(rest, ""); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if wait {
		return app.Main(rest, func(args []string, wd string) (*app.Request, error) {
			// A later launch forwards os.Args[1:]: "open" and its options,
			// or nothing when the app is opened from Finder.
			if len(args) > 0 && args[0] == "open" {
				return openWorkspace(stripWait(args[1:]), wd)
			}
			if len(args) == 0 || strings.HasPrefix(args[0], "-psn_") {
				dir, err := app.ChooseRepository()
				if err != nil {
					return nil, err
				}
				return openWorkspace([]string{"--repo", dir}, "")
			}
			return openWorkspace(stripWait(args), wd)
		})
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// Run the bundle's own binary, not a symlink to it, so macOS shows the
	// window as the app (its name and icon) when installed as one.
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	cmd := exec.Command(exe, append([]string{"open", "--wait"}, rest...)...)
	cmd.SysProcAttr = detached()
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	fmt.Println("self-review: opened the review window (closing it ends the process)")
	return nil
}

// launchedFromFinder reports whether the app bundle was opened without a
// command line (from Finder, the Dock or the open command).
func launchedFromFinder(args []string) bool {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-psn_") {
		return false
	}
	exe, err := os.Executable()
	return err == nil && strings.Contains(exe, ".app/Contents/MacOS/")
}

// runFinder asks for a repository and opens its review window. A later
// launch, from Finder or a terminal, goes to the same process.
func runFinder() error {
	return app.Main(nil, func(args []string, wd string) (*app.Request, error) {
		if len(args) > 0 && args[0] == "open" {
			return openWorkspace(stripWait(args[1:]), wd)
		}
		dir, err := app.ChooseRepository()
		if err != nil {
			return nil, err
		}
		return openWorkspace([]string{"--repo", dir}, "")
	})
}

func stripWait(args []string) []string {
	var kept []string
	for _, a := range args {
		if a != "--wait" && a != "-wait" {
			kept = append(kept, a)
		}
	}
	return kept
}

// openWorkspace parses open's arguments, relative to wd when not empty.
func openWorkspace(args []string, wd string) (*app.Request, error) {
	fs := newFlags("open", openHelp)
	view := fs.String("view", "", "")
	base := fs.String("base", "", "")
	repoDir := fs.String("repo", ".", "")
	file := fs.String("file", "", "")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) > 1 {
		return nil, errors.New("too many arguments (see self-review help open)")
	}
	abs := func(p string) string {
		if wd != "" && !filepath.IsAbs(p) {
			return filepath.Join(wd, p)
		}
		return p
	}
	r, err := repo.Open(abs(*repoDir))
	if err != nil {
		return nil, err
	}
	dir, branchGuide := "", ""
	if len(pos) == 1 {
		p, err := filepath.Abs(abs(pos[0]))
		if err != nil {
			return nil, err
		}
		st, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if st.IsDir() {
			dir = p
		} else {
			dir, branchGuide = filepath.Dir(p), p
		}
	}
	ws, err := review.Open(r, dir, branchGuide, *base)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	req := &app.Request{Workspace: ws, View: *view, Lang: cfg.ResolvedLang()}
	if *file != "" {
		if req.File, req.Start, req.End, err = parseFileArg(*file); err != nil {
			return nil, err
		}
	}
	return req, nil
}

// parseFileArg reads path[:start[-end]].
func parseFileArg(s string) (path string, start, end int, err error) {
	path, lines, ok := strings.Cut(s, ":")
	if !ok {
		return path, 0, 0, nil
	}
	from, to, ranged := strings.Cut(lines, "-")
	if start, err = strconv.Atoi(from); err != nil || start < 1 {
		return "", 0, 0, fmt.Errorf("--file %q: want path, path:line or path:start-end", s)
	}
	end = start
	if ranged {
		if end, err = strconv.Atoi(to); err != nil || end < start {
			return "", 0, 0, fmt.Errorf("--file %q: want path, path:line or path:start-end", s)
		}
	}
	return path, start, end, nil
}

// ---------- config ----------

const configHelp = `Usage: self-review config [lang [<value>]]

Shows or changes settings, kept in <user config dir>/self-review/config.json.

  self-review config            show every setting
  self-review config lang       print the reader's language, resolved (e.g. ja)
  self-review config lang <v>   set it: auto (follow the system), en or ja

The reader's language is what the review window shows its texts in, and
what agents write guides in. Agent-facing text (help, check output, the
skill) stays English.
`

func runConfig(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	switch {
	case len(args) == 0:
		fmt.Printf("lang = %s (%s)\n", cfg.Lang, cfg.ResolvedLang())
		return nil
	case args[0] == "-h" || args[0] == "--help":
		fmt.Print(configHelp)
		return nil
	case args[0] != "lang" || len(args) > 2:
		fmt.Fprint(os.Stderr, configHelp)
		return exitError(2)
	case len(args) == 1:
		fmt.Println(cfg.ResolvedLang())
		return nil
	}
	v := strings.ToLower(args[1])
	if !config.ValidLang(v) {
		return fmt.Errorf("unsupported language %q: want auto, %s", args[1], strings.Join(config.Languages, ", "))
	}
	cfg.Lang = v
	if err := config.Save(cfg); err != nil {
		return err
	}
	fmt.Printf("lang = %s (%s)\n", cfg.Lang, cfg.ResolvedLang())
	return nil
}
