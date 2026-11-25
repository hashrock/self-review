// Package repo reads diffs and file contents from a local Git repository.
package repo

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// GuideDir is where guides are written by default. Untracked files in it are
// not part of the change.
const GuideDir = ".self-review"

// Repo is a Git working tree.
type Repo struct {
	Root string
}

// Open finds the working tree containing dir.
func Open(dir string) (*Repo, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return nil, fmt.Errorf("%s is not in a Git repository", dir)
	}
	return &Repo{Root: strings.TrimSpace(string(out))}, nil
}

func (r *Repo) git(args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", r.Root, "-c", "core.quotepath=off"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// Name is the working tree's directory name.
func (r *Repo) Name() string { return filepath.Base(r.Root) }

// Branch is the current branch name, or a short hash when detached.
func (r *Repo) Branch() string {
	if out, err := r.git("rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		if b := strings.TrimSpace(out); b != "HEAD" {
			return b
		}
	}
	out, _ := r.git("rev-parse", "--short", "HEAD")
	return strings.TrimSpace(out)
}

// DefaultBranch guesses the repository's default branch.
func (r *Repo) DefaultBranch() (string, error) {
	if out, err := r.git("symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		return strings.TrimSpace(out), nil
	}
	for _, b := range []string{"main", "master", "origin/main", "origin/master"} {
		if _, err := r.git("rev-parse", "--verify", "--quiet", b+"^{commit}"); err == nil {
			return b, nil
		}
	}
	return "", errors.New("cannot find the default branch: set it with the base attribute or --base")
}

// Comparison is a resolved pair of versions. An empty New means the working tree.
type Comparison struct {
	Mode string
	Base string // branch mode: the branch HEAD diverged from
	Old  string // full commit hash (or EmptyTree before a root commit)
	New  string // full commit hash, or "" for the working tree
}

// Short abbreviates a commit hash.
func Short(h string) string { return short(h) }

// String describes the comparison in English, for the command line.
func (c *Comparison) String() string {
	newSide := "working tree"
	if c.New != "" {
		newSide = short(c.New)
	}
	switch c.Mode {
	case "branch":
		return fmt.Sprintf("merge base with %s (%s) .. %s", c.Base, short(c.Old), newSide)
	case "uncommitted":
		return "uncommitted changes (HEAD .. working tree)"
	case "commit":
		return "commit " + short(c.New)
	}
	return short(c.Old) + " .. " + newSide
}

// Spec describes a comparison before it is resolved to commits.
type Spec struct {
	Mode   string // branch, uncommitted, direct, commit
	Base   string // branch: the branch to diverge from (default branch when empty)
	Prev   string // direct
	Next   string // direct; empty means the working tree
	Commit string // commit
}

// EmptyTree is Git's empty tree object, the parent of a root commit.
const EmptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// Resolve turns a comparison spec into concrete versions.
func (r *Repo) Resolve(sp Spec) (*Comparison, error) {
	switch sp.Mode {
	case "uncommitted":
		h, err := r.revParse("HEAD")
		if err != nil {
			return nil, err
		}
		return &Comparison{Mode: sp.Mode, Old: h}, nil
	case "direct":
		if sp.Prev == "" {
			return nil, errors.New("direct requires previous-version")
		}
		o, err := r.revParse(sp.Prev)
		if err != nil {
			return nil, err
		}
		c := &Comparison{Mode: sp.Mode, Old: o}
		if sp.Next != "" {
			if c.New, err = r.revParse(sp.Next); err != nil {
				return nil, err
			}
		}
		return c, nil
	case "commit":
		if sp.Commit == "" {
			return nil, errors.New("comparison=\"commit\" requires commit")
		}
		h, err := r.revParse(sp.Commit)
		if err != nil {
			return nil, err
		}
		parent, err := r.revParse(h + "^")
		if err != nil {
			parent = EmptyTree // root commit
		}
		return &Comparison{Mode: sp.Mode, Old: parent, New: h}, nil
	case "branch", "":
		mb, base, err := r.MergeBase(sp.Base)
		if err != nil {
			return nil, err
		}
		return &Comparison{Mode: "branch", Base: base, Old: mb}, nil
	}
	return nil, fmt.Errorf("unknown comparison %q", sp.Mode)
}

// MergeBase returns where HEAD diverged from base (the default branch when
// empty), and the base name it used.
func (r *Repo) MergeBase(base string) (string, string, error) {
	if base == "" {
		var err error
		if base, err = r.DefaultBranch(); err != nil {
			return "", "", err
		}
	}
	out, err := r.git("merge-base", base, "HEAD")
	if err != nil {
		return "", base, fmt.Errorf("cannot find where HEAD diverged from %s: %w", base, err)
	}
	return strings.TrimSpace(out), base, nil
}

// Commit is one commit of the branch under review.
type Commit struct {
	Hash    string `json:"hash"`
	Short   string `json:"short"`
	Subject string `json:"subject"`
	Author  string `json:"author"`
	Date    string `json:"date"`
}

// Commits lists the commits between base's merge base and HEAD, oldest first.
func (r *Repo) Commits(base string) ([]Commit, string, error) {
	mb, base, err := r.MergeBase(base)
	if err != nil {
		return nil, base, err
	}
	out, err := r.git("log", "--reverse", "--format=%H%x00%h%x00%s%x00%an%x00%aI%x1e", mb+"..HEAD")
	if err != nil {
		return nil, base, err
	}
	var cs []Commit
	for _, rec := range strings.Split(out, "\x1e") {
		f := strings.Split(strings.TrimSpace(rec), "\x00")
		if len(f) != 5 {
			continue
		}
		cs = append(cs, Commit{Hash: f[0], Short: f[1], Subject: f[2], Author: f[3], Date: f[4]})
	}
	return cs, base, nil
}

// Dirty reports whether the working tree has changes against HEAD, including
// untracked files outside the guide directory.
func (r *Repo) Dirty() (bool, error) {
	out, err := r.git("status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return false, err
	}
	for _, rec := range strings.Split(out, "\x00") {
		if len(rec) > 3 && !strings.HasPrefix(rec[3:], GuideDir+"/") {
			return true, nil
		}
	}
	return false, nil
}

func (r *Repo) revParse(rev string) (string, error) {
	out, err := r.git("rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("commit %q not found", rev)
	}
	return strings.TrimSpace(out), nil
}

func short(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

// FileStat summarizes one changed file.
type FileStat struct {
	Path    string `json:"path"`
	Status  string `json:"status"` // added, deleted, modified
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
	Binary  bool   `json:"binary"`
	// Key identifies this exact change of the file (old and new blob), so a
	// "viewed" mark stops applying once either side changes.
	Key string `json:"key"`
}

const zeroHash = "0000000000000000000000000000000000000000"

// BlobHash is Git's object id for content stored as a blob.
func BlobHash(content []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(content))
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

func changeKey(p, oldHash, newHash string) string {
	return p + "@" + oldHash[:12] + ".." + newHash[:12]
}

func (c *Comparison) diffArgs(opts []string, paths ...string) []string {
	args := append([]string{"diff", "--no-color", "--no-ext-diff", "--no-renames"}, opts...)
	args = append(args, c.Old)
	if c.New != "" {
		args = append(args, c.New)
	}
	if len(paths) > 0 {
		args = append(append(args, "--"), paths...)
	}
	return args
}

// Changes lists the changed files of a comparison, including untracked files
// when comparing against the working tree.
func (r *Repo) Changes(c *Comparison) ([]FileStat, error) {
	numstat, err := r.git(c.diffArgs([]string{"--numstat", "-z"})...)
	if err != nil {
		return nil, err
	}
	raw, err := r.git(c.diffArgs([]string{"--raw", "-z", "--no-abbrev"})...)
	if err != nil {
		return nil, err
	}
	type rawEntry struct{ status, oldHash, newHash string }
	entries := map[string]rawEntry{}
	parts := strings.Split(raw, "\x00")
	for i := 0; i+1 < len(parts); i += 2 {
		f := strings.Fields(strings.TrimPrefix(parts[i], ":"))
		if len(f) != 5 {
			continue
		}
		e := rawEntry{oldHash: f[2], newHash: f[3]}
		switch f[4] {
		case "A":
			e.status = "added"
		case "D":
			e.status = "deleted"
		default:
			e.status = "modified"
		}
		entries[parts[i+1]] = e
	}

	var files []FileStat
	for _, rec := range strings.Split(numstat, "\x00") {
		f := strings.SplitN(rec, "\t", 3)
		if len(f) != 3 {
			continue
		}
		e := entries[f[2]]
		fs := FileStat{Path: f[2], Status: e.status}
		if f[0] == "-" {
			fs.Binary = true
		} else {
			fs.Added, _ = strconv.Atoi(f[0])
			fs.Deleted, _ = strconv.Atoi(f[1])
		}
		newHash := e.newHash
		if newHash == zeroHash && e.status != "deleted" {
			// Changed in the working tree; Git leaves the new id blank.
			if data, err := os.ReadFile(filepath.Join(r.Root, filepath.FromSlash(fs.Path))); err == nil {
				newHash = BlobHash(data)
			}
		}
		fs.Key = changeKey(fs.Path, orZero(e.oldHash), orZero(newHash))
		files = append(files, fs)
	}

	if c.New == "" {
		untracked, err := r.git("ls-files", "--others", "--exclude-standard", "-z")
		if err != nil {
			return nil, err
		}
		for _, p := range strings.Split(untracked, "\x00") {
			if p == "" || strings.HasPrefix(p, GuideDir+"/") {
				continue
			}
			fs := FileStat{Path: p, Status: "added"}
			data, err := os.ReadFile(filepath.Join(r.Root, filepath.FromSlash(p)))
			if err == nil {
				if isBinary(data) {
					fs.Binary = true
				} else {
					fs.Added = len(splitLines(string(data)))
				}
				fs.Key = changeKey(p, zeroHash, BlobHash(data))
			} else {
				fs.Key = changeKey(p, zeroHash, zeroHash)
			}
			files = append(files, fs)
		}
	}
	return files, nil
}

func orZero(h string) string {
	if len(h) < 12 {
		return zeroHash
	}
	return h
}

// CleanPath validates a worktree-relative path.
func CleanPath(p string) (string, error) {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return "", fmt.Errorf("invalid path %q", p)
	}
	c := path.Clean(p)
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("path %q points outside the worktree", p)
	}
	return c, nil
}

// Content reads a file at a version. ok is false when the file does not exist there.
func (r *Repo) Content(version, p string) (content string, ok bool, err error) {
	if version == "" {
		data, err := os.ReadFile(filepath.Join(r.Root, filepath.FromSlash(p)))
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		return string(data), true, nil
	}
	if _, err := r.git("cat-file", "-e", version+":"+p); err != nil {
		return "", false, nil
	}
	out, err := r.git("show", version+":"+p)
	if err != nil {
		return "", false, err
	}
	return out, true, nil
}

// LineType is the kind of a diff line.
type LineType string

const (
	Context LineType = "ctx"
	Add     LineType = "add"
	Del     LineType = "del"
)

// Line is one line of a whole-file diff.
type Line struct {
	Type LineType
	Old  int // 0 for added lines
	New  int // 0 for deleted lines
	Text string
}

// FileDiff is the whole-file diff of one path.
type FileDiff struct {
	Path       string
	Status     string // added, deleted, modified, unchanged, missing
	Binary     bool
	OldContent string
	NewContent string
	Lines      []Line
}

var hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// Diff returns the full-file diff of p, with every unchanged line as context.
func (r *Repo) Diff(c *Comparison, p string) (*FileDiff, error) {
	p, err := CleanPath(p)
	if err != nil {
		return nil, err
	}
	oldC, oldOK, err := r.Content(c.Old, p)
	if err != nil {
		return nil, err
	}
	newC, newOK, err := r.Content(c.New, p)
	if err != nil {
		return nil, err
	}
	fd := &FileDiff{Path: p, OldContent: oldC, NewContent: newC}
	switch {
	case !oldOK && !newOK:
		fd.Status = "missing"
		return fd, nil
	case !oldOK:
		fd.Status = "added"
	case !newOK:
		fd.Status = "deleted"
	case oldC == newC:
		fd.Status = "unchanged"
	default:
		fd.Status = "modified"
	}
	if isBinary([]byte(oldC)) || isBinary([]byte(newC)) {
		fd.Binary = true
		return fd, nil
	}

	switch fd.Status {
	case "added":
		for i, t := range splitLines(newC) {
			fd.Lines = append(fd.Lines, Line{Type: Add, New: i + 1, Text: t})
		}
		return fd, nil
	case "deleted":
		for i, t := range splitLines(oldC) {
			fd.Lines = append(fd.Lines, Line{Type: Del, Old: i + 1, Text: t})
		}
		return fd, nil
	case "unchanged":
		for i, t := range splitLines(newC) {
			fd.Lines = append(fd.Lines, Line{Type: Context, Old: i + 1, New: i + 1, Text: t})
		}
		return fd, nil
	}

	out, err := r.git(c.diffArgs([]string{"-U100000000"}, p)...)
	if err != nil {
		return nil, err
	}
	fd.Lines = parseUnified(out)
	return fd, nil
}

func parseUnified(out string) []Line {
	var lines []Line
	var o, n int
	inHunk := false
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		l := sc.Text()
		if m := hunkRe.FindStringSubmatch(l); m != nil {
			o, _ = strconv.Atoi(m[1])
			n, _ = strconv.Atoi(m[2])
			inHunk = true
			continue
		}
		if !inHunk || l == "" {
			continue
		}
		switch l[0] {
		case ' ':
			lines = append(lines, Line{Type: Context, Old: o, New: n, Text: l[1:]})
			o++
			n++
		case '+':
			lines = append(lines, Line{Type: Add, New: n, Text: l[1:]})
			n++
		case '-':
			lines = append(lines, Line{Type: Del, Old: o, Text: l[1:]})
			o++
		}
	}
	return lines
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

// SplitLines splits file content into lines without trailing newlines.
func SplitLines(s string) []string { return splitLines(s) }

func isBinary(b []byte) bool {
	if len(b) > 8000 {
		b = b[:8000]
	}
	return bytes.IndexByte(b, 0) >= 0
}

// ExcerptContext is how many unchanged lines surround the changes of an
// excerpt, in the window and in check's measure of its length.
const ExcerptContext = 3

// ExcerptLength is how many lines an excerpt shows: its lines, plus a
// separator between groups.
func ExcerptLength(groups [][]int) int {
	n := 0
	for i, g := range groups {
		if i > 0 {
			n++
		}
		n += len(g)
	}
	return n
}

// Excerpt selects the lines of a whole-file diff to show for a range.
// It returns groups of contiguous line indices. start and end are 0 when unset.
func Excerpt(lines []Line, start, end, context int) [][]int {
	if start == 0 && end == 0 {
		start, end = 1, int(^uint(0)>>1)
	} else if start == 0 {
		start = 1
	} else if end == 0 {
		end = int(^uint(0) >> 1)
	}
	in := func(x int) bool { return x >= start && x <= end }

	// anchor: the new-version line a deleted line sits before.
	anchor := make([]int, len(lines))
	next := 0
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i].New > 0 {
			next = lines[i].New
		}
		anchor[i] = next
	}

	selected := make([]bool, len(lines))
	any := false
	for i, l := range lines {
		switch l.Type {
		case Add:
			selected[i] = in(l.New)
		case Del:
			selected[i] = in(l.Old) || (anchor[i] > 0 && in(anchor[i]))
		}
		any = any || selected[i]
	}

	if !any {
		// No changes in range: show the range itself as unchanged code.
		var g []int
		for i, l := range lines {
			if l.New > 0 && in(l.New) {
				g = append(g, i)
			}
		}
		if g == nil {
			return nil
		}
		return [][]int{g}
	}

	// Keep replacements intact: in a change block (a run of changed lines)
	// with both deleted and added lines, a selected line of one kind brings
	// the lines of the other kind. Runs of one kind (a new file, a block of
	// additions) are not extended, so a range within them stays a range.
	for i := 0; i < len(lines); {
		if lines[i].Type == Context {
			i++
			continue
		}
		j := i
		var hasAdd, hasDel, selAdd, selDel bool
		for ; j < len(lines) && lines[j].Type != Context; j++ {
			if lines[j].Type == Add {
				hasAdd, selAdd = true, selAdd || selected[j]
			} else {
				hasDel, selDel = true, selDel || selected[j]
			}
		}
		if hasAdd && hasDel {
			for k := i; k < j; k++ {
				if lines[k].Type == Del && selAdd || lines[k].Type == Add && selDel {
					selected[k] = true
				}
			}
		}
		i = j
	}

	show := make([]bool, len(lines))
	for i := range lines {
		if !selected[i] {
			continue
		}
		for j := max(0, i-context); j <= min(len(lines)-1, i+context); j++ {
			show[j] = true
		}
	}

	var groups [][]int
	var cur []int
	for i := range lines {
		if show[i] {
			cur = append(cur, i)
		} else if cur != nil {
			groups = append(groups, cur)
			cur = nil
		}
	}
	if cur != nil {
		groups = append(groups, cur)
	}
	return groups
}

// CommitMessage returns the full message of a commit.
func (r *Repo) CommitMessage(rev string) (string, error) {
	out, err := r.git("log", "-1", "--format=%B", rev)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
