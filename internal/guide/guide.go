// Package guide parses change guide documents.
//
// A change guide is Markdown wrapped in a <change-guide> element, with
// <excerpt> elements pointing at diff ranges and self-closing
// <related-location/> elements pointing at related code.
package guide

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// Comparison modes.
const (
	Branch      = "branch"
	Uncommitted = "uncommitted"
	Direct      = "direct"
	Commit      = "commit"
)

// Guide is a parsed change guide.
type Guide struct {
	Title           string
	FollowUp        string
	Comparison      string
	Base            string // optional base ref for branch mode (extension)
	PreviousVersion string // direct mode
	NextVersion     string // direct mode; empty means the working tree
	Commit          string // commit mode: the commit to review against its parent
	Blocks          []Block
}

// BlockKind identifies the kind of a block.
type BlockKind string

const (
	KindMarkdown BlockKind = "markdown"
	KindExcerpt  BlockKind = "excerpt"
	KindRelated  BlockKind = "related"
)

// Block is one piece of the guide body, in document order.
type Block struct {
	Kind      BlockKind
	Markdown  string // markdown text, or the excerpt's introduction
	Path      string
	Label     string // related locations only
	StartLine int    // 0 when not set
	EndLine   int    // 0 when not set
	Line      int    // line in the guide file where the block starts
}

var (
	openRe    = regexp.MustCompile(`<change-guide\b([^>]*)>`)
	closeRe   = regexp.MustCompile(`</change-guide\s*>`)
	tagRe     = regexp.MustCompile(`<excerpt\b([^>]*)>|<related-location\b([^>]*?)/>`)
	excerptRe = regexp.MustCompile(`</excerpt\s*>`)
	attrRe    = regexp.MustCompile(`([A-Za-z][\w-]*)\s*=\s*"([^"]*)"`)
)

// Parse parses a change guide document.
func Parse(src string) (*Guide, error) {
	open := openRe.FindStringSubmatchIndex(src)
	if open == nil {
		return nil, fmt.Errorf("no <change-guide> element found")
	}
	attrs := parseAttrs(src[open[2]:open[3]])
	g := &Guide{
		Title:           attrs["title"],
		FollowUp:        attrs["follow-up"],
		Comparison:      attrs["comparison"],
		Base:            attrs["base"],
		PreviousVersion: attrs["previous-version"],
		NextVersion:     attrs["next-version"],
		Commit:          attrs["commit"],
	}

	// Tags are searched in a copy where code spans and fenced code blocks are
	// blanked out, so tag names written as code in prose stay prose. Offsets
	// are identical in both strings.
	bodyStart := open[1]
	body := src[bodyStart:]
	masked := maskCode(body)
	if c := closeRe.FindStringIndex(masked); c != nil {
		body, masked = body[:c[0]], masked[:c[0]]
	} else {
		return nil, fmt.Errorf("missing </change-guide> closing tag")
	}

	lineAt := func(offset int) int {
		return strings.Count(src[:bodyStart+offset], "\n") + 1
	}

	pos := 0
	for pos < len(body) {
		m := tagRe.FindStringSubmatchIndex(masked[pos:])
		if m == nil {
			g.addMarkdown(body[pos:], lineAt(pos))
			break
		}
		start, end := pos+m[0], pos+m[1]
		g.addMarkdown(body[pos:start], lineAt(pos))

		if m[2] >= 0 { // <excerpt ...>
			a := parseAttrs(body[pos+m[2] : pos+m[3]])
			rest := body[end:]
			c := excerptRe.FindStringIndex(masked[end:])
			if c == nil {
				return nil, fmt.Errorf("line %d: <excerpt> has no closing tag", lineAt(start))
			}
			b, err := newBlock(KindExcerpt, a, lineAt(start))
			if err != nil {
				return nil, err
			}
			b.Markdown = strings.TrimSpace(rest[:c[0]])
			g.Blocks = append(g.Blocks, b)
			pos = end + c[1]
		} else { // <related-location .../>
			a := parseAttrs(body[pos+m[4] : pos+m[5]])
			b, err := newBlock(KindRelated, a, lineAt(start))
			if err != nil {
				return nil, err
			}
			b.Label = a["label"]
			g.Blocks = append(g.Blocks, b)
			pos = end
		}
	}
	return g, nil
}

// maskCode returns s with the contents of fenced code blocks and inline code
// spans replaced by spaces (newlines kept), preserving every byte offset.
func maskCode(s string) string {
	b := []byte(s)
	blank := func(from, to int) {
		for i := from; i < to; i++ {
			if b[i] != '\n' {
				b[i] = ' '
			}
		}
	}

	// Fenced code blocks: a line of 3+ backticks or tildes (up to 3 spaces of
	// indent) until a closing fence of the same char that is at least as long.
	var fenceChar byte
	fenceLen, fenceStart := 0, 0
	inFence := make([]bool, len(b))
	for lineStart := 0; lineStart < len(b); {
		lineEnd := strings.IndexByte(s[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(b)
		} else {
			lineEnd += lineStart
		}
		line := strings.TrimLeft(s[lineStart:lineEnd], " ")
		indent := lineEnd - lineStart - len(line)
		if indent <= 3 && len(line) >= 3 && (line[0] == '`' || line[0] == '~') {
			n := 0
			for n < len(line) && line[n] == line[0] {
				n++
			}
			switch {
			case fenceLen == 0 && n >= 3:
				fenceChar, fenceLen, fenceStart = line[0], n, lineStart
			case fenceLen > 0 && line[0] == fenceChar && n >= fenceLen && strings.TrimSpace(line[n:]) == "":
				for i := fenceStart; i < lineEnd; i++ {
					inFence[i] = true
				}
				fenceLen = 0
			}
		}
		lineStart = lineEnd + 1
	}
	if fenceLen > 0 { // unclosed fence runs to the end
		for i := fenceStart; i < len(b); i++ {
			inFence[i] = true
		}
	}
	for i, f := range inFence {
		if f && b[i] != '\n' {
			b[i] = ' '
		}
	}

	// Inline code spans: a run of n backticks closed by a run of exactly n.
	runAt := func(i int) int {
		n := 0
		for i+n < len(b) && b[i+n] == '`' && !inFence[i+n] {
			n++
		}
		return n
	}
	for i := 0; i < len(b); {
		if inFence[i] || b[i] != '`' {
			i++
			continue
		}
		n := runAt(i)
		closed := false
		for j := i + n; j < len(b); {
			if inFence[j] {
				break
			}
			if b[j] != '`' {
				j++
				continue
			}
			m := runAt(j)
			if m == n {
				blank(i, j+m)
				i, closed = j+m, true
				break
			}
			j += m
		}
		if !closed {
			i += n // unmatched backticks are literal
		}
	}
	return string(b)
}

func (g *Guide) addMarkdown(s string, line int) {
	if strings.TrimSpace(s) == "" {
		return
	}
	g.Blocks = append(g.Blocks, Block{Kind: KindMarkdown, Markdown: strings.Trim(s, "\n"), Line: line})
}

func newBlock(kind BlockKind, a map[string]string, line int) (Block, error) {
	b := Block{Kind: kind, Path: a["path"], Line: line}
	var err error
	if b.StartLine, err = atoiOpt(a["start-line"]); err != nil {
		return b, fmt.Errorf("line %d: start-line is not a number: %q", line, a["start-line"])
	}
	if b.EndLine, err = atoiOpt(a["end-line"]); err != nil {
		return b, fmt.Errorf("line %d: end-line is not a number: %q", line, a["end-line"])
	}
	return b, nil
}

func atoiOpt(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	return strconv.Atoi(strings.TrimSpace(s))
}

func parseAttrs(s string) map[string]string {
	out := map[string]string{}
	for _, m := range attrRe.FindAllStringSubmatch(s, -1) {
		out[m[1]] = html.UnescapeString(m[2])
	}
	return out
}

// Problem is a validation issue found in a guide.
type Problem struct {
	Line    int
	Message string
}

func (p Problem) String() string {
	if p.Line > 0 {
		return fmt.Sprintf("line %d: %s", p.Line, p.Message)
	}
	return p.Message
}

// recapHeadings are the accepted names of the final section, lower-cased:
// "Recap" and its translations in the languages guides are written in.
var recapHeadings = map[string]bool{"recap": true, "summary": true, "まとめ": true, "要約": true}

// Validate checks the guide's structure, independent of any repository.
func (g *Guide) Validate() []Problem {
	var ps []Problem
	add := func(line int, f string, a ...any) { ps = append(ps, Problem{line, fmt.Sprintf(f, a...)}) }

	if g.Title == "" {
		add(0, "<change-guide> has no title")
	}
	if g.Commit != "" && g.Comparison != Commit {
		add(0, "commit is only allowed with comparison=\"commit\"")
	}
	switch g.Comparison {
	case Branch, Uncommitted:
		if g.PreviousVersion != "" || g.NextVersion != "" {
			add(0, "previous-version / next-version are only allowed with comparison=\"direct\"")
		}
	case Direct:
		if g.PreviousVersion == "" {
			add(0, "comparison=\"direct\" requires previous-version")
		}
	case Commit:
		if g.Commit == "" {
			add(0, "comparison=\"commit\" requires commit")
		}
		if g.PreviousVersion != "" || g.NextVersion != "" {
			add(0, "previous-version / next-version are only allowed with comparison=\"direct\"")
		}
	case "":
		add(0, "<change-guide> has no comparison")
	default:
		add(0, "invalid comparison %q (want branch, uncommitted, direct or commit)", g.Comparison)
	}

	var h1, h2, recap int
	for _, b := range g.Blocks {
		switch b.Kind {
		case KindMarkdown:
			for _, l := range strings.Split(maskCode(b.Markdown), "\n") { // ignore "#" lines in code blocks
				switch {
				case strings.HasPrefix(l, "# "):
					h1++
				case strings.HasPrefix(l, "## "):
					h2++
					if recapHeadings[strings.ToLower(strings.TrimSpace(l[3:]))] {
						recap++
					}
				}
			}
		case KindExcerpt, KindRelated:
			if b.Path == "" {
				add(b.Line, "missing path")
			} else if strings.HasPrefix(b.Path, "/") || strings.Contains(b.Path, "\\") {
				add(b.Line, "path must be a worktree-relative Unix path: %q", b.Path)
			}
			if b.StartLine < 0 || b.EndLine < 0 {
				add(b.Line, "line numbers start at 1")
			}
			if b.StartLine > 0 && b.EndLine > 0 && b.EndLine < b.StartLine {
				add(b.Line, "end-line (%d) is before start-line (%d)", b.EndLine, b.StartLine)
			}
			if b.Kind == KindExcerpt && b.Markdown == "" {
				add(b.Line, "<excerpt> has no introduction")
			}
			if b.Kind == KindRelated {
				if b.Label == "" {
					add(b.Line, "<related-location> has no label")
				}
				if b.StartLine == 0 {
					add(b.Line, "<related-location> has no start-line")
				}
			}
		}
	}
	if h1 != 1 {
		add(0, "want exactly one # heading, found %d", h1)
	}
	if h2 == 0 {
		add(0, "no ## sections")
	}
	if recap == 0 {
		add(0, "end with a recap section: ## Recap, or its translation in the guide's language (e.g. ## まとめ)")
	}
	return ps
}
