package app

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/hashrock/self-review/internal/repo"
)

// codeRow is a line of a diff, ready to draw: plain rows of text, so that
// they sit in the guide's selection and right-click menu, and the window
// knows which line the pointer is on.
type codeRow struct {
	typ      repo.LineType
	old, new int // file lines, 0 on the side the line is not in
	spans    []ui.Span
}

// side is the side a thread about the row is on: the new one, unless the
// line was deleted.
func (r codeRow) side() (editor.DiffSide, int) {
	if r.typ == repo.Del {
		return editor.DiffOld, r.old
	}
	return editor.DiffNew, r.new
}

// codeRows colors lines of a whole-file diff in the file's language. Each
// side is tokenized in order, so a comment or string that spans lines
// stays colored.
func codeRows(path string, lines []repo.Line, idx []int) []codeRow {
	var tok editor.Tokenizer
	if l := editor.LanguageFor(path); l != nil {
		tok = l.Tokenizer
	}
	var oldState, newState uint64
	var buf []editor.Token
	rows := make([]codeRow, 0, len(idx))
	for _, i := range idx {
		l := lines[i]
		r := codeRow{typ: l.Type, old: l.Old, new: l.New}
		if tok == nil {
			r.spans = []ui.Span{{Text: l.Text}}
		} else {
			state := &newState
			if l.Type == repo.Del {
				state = &oldState
			}
			buf, *state = tok.Tokenize(l.Text, *state, buf[:0])
			if l.Type == repo.Context {
				oldState = newState
			}
			r.spans = spansOf(l.Text, buf)
		}
		rows = append(rows, r)
	}
	return rows
}

func spansOf(text string, toks []editor.Token) []ui.Span {
	var spans []ui.Span
	prev := 0
	for _, t := range toks {
		end := min(t.End, len(text))
		if end <= prev {
			continue
		}
		s := ui.Span{Text: text[prev:end]}
		if int(t.Kind) < len(codeTheme.Tokens) {
			st := codeTheme.Tokens[t.Kind]
			s.Color, s.Italic = st.Color, st.Italic
			if st.Bold {
				s.Weight = 600
			}
		}
		spans = append(spans, s)
		prev = end
	}
	if prev < len(text) {
		spans = append(spans, ui.Span{Text: text[prev:]})
	}
	if len(spans) == 0 {
		spans = []ui.Span{{Text: " "}}
	}
	return spans
}

var (
	addedTint   = codeTheme.Added.Alpha(0.13)
	deletedTint = codeTheme.Deleted.Alpha(0.11)
	focusTint   = ui.RGBA(113, 113, 122, 0.10)
)

// drawRow draws a code row: tinted by its change, with line numbers when
// numbered, and focused (as a range opened from the guide) in gray.
func drawRow(c *ui.Context, r codeRow, numbered, focused bool) ui.Element {
	t := c.Theme()
	row := ui.Row(c).FillWidth().AlignItems(ui.Baseline).PaddingX(14)
	switch {
	case r.typ == repo.Add:
		row.Background(addedTint)
	case r.typ == repo.Del:
		row.Background(deletedTint)
	case focused:
		row.Background(focusTint)
	}
	row.Children(func() {
		if numbered {
			n := ""
			if r.typ == repo.Del {
				n = fmt.Sprint(r.old)
			} else if r.new > 0 {
				n = fmt.Sprint(r.new)
			}
			ui.Text(c, n).Font(codeFont.Family).FontSize(12).TextColor(t.TextMuted).Width(44).TextAlign(ui.End).MarginEnd(14).Unselectable()
		}
		ui.RichText(c, r.spans...).Font(codeFont.Family).FontSize(codeFont.Size).LineHeight(codeLineHeight).NoWrap().TabSize(4)
	})
	return row
}

// threadMark is the row under a line that has a conversation: click it to
// open the conversation.
func (a *App) threadMark(c *ui.Context, th *thread) {
	t := c.Theme()
	mark := ui.Row(c).PaddingX(14).PaddingY(4).Gap(6).Cursor(ui.CursorPointer).FillWidth()
	if mark.Hovered() {
		mark.Background(t.SurfaceHover)
	}
	if mark.Clicked() {
		a.focusThread(th)
	}
	mark.Children(func() {
		n := 0
		for _, m := range th.msgs {
			if m.user {
				n++
			}
		}
		ui.Textf(c, "💬 %s · %d", th.label, n).FontSize(12).TextColor(t.TextMuted).Unselectable()
	})
}

// threadsOn indexes the threads on lines of path by where they end.
func (a *App) threadsOn(path string) map[[2]int][]*thread {
	out := map[[2]int][]*thread{}
	for _, th := range a.viewThreads() {
		if th.kind == threadLine && th.path == path {
			k := [2]int{int(th.side), th.end}
			out[k] = append(out[k], th)
		}
	}
	return out
}

func rowKey(r codeRow) [2]int {
	side, n := r.side()
	return [2]int{int(side), n}
}
