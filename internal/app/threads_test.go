package app

import (
	"context"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/hashrock/self-review/internal/agent"
	"github.com/hashrock/self-review/internal/review"
)

// fakeAgent answers every prompt with a fixed text and records the prompts.
type fakeAgent struct {
	prompts []string
	answer  string
	closed  bool
}

func (f *fakeAgent) NewSession(context.Context) (conversation, error) { return f, nil }
func (f *fakeAgent) Close()                                           { f.closed = true }
func (f *fakeAgent) Prompt(_ context.Context, text string, on func(agent.Event)) error {
	f.prompts = append(f.prompts, text)
	on(agent.Event{Tool: "Read a.go"})
	on(agent.Event{Text: f.answer})
	return nil
}

func withFake(a *App) *fakeAgent {
	f := &fakeAgent{answer: "It adds **A**."}
	a.agentTitle = "Fake"
	a.agents = &agentHolder{start: func(context.Context) (asker, error) { return f, nil }}
	return f
}

func TestAskChange(t *testing.T) {
	a, hs := newApp(t)
	f := withFake(a)
	a.selectView(review.ViewCommit + hs[0])
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()

	click(t, tt, "💬 質問")
	click(t, tt, "新しい質問")
	th := a.viewThreads()[0]
	th.draft = "Why A?"
	click(t, tt, "送信")
	if len(f.prompts) != 1 {
		t.Fatalf("prompts: %q", f.prompts)
	}
	p := f.prompts[0]
	for _, want := range []string{"Answer my questions and review comments in Japanese", "git show " + hs[0], "the change as a whole", "Why A?", ".self-review/commits/" + hs[0] + ".md"} {
		if !strings.Contains(p, want) {
			t.Errorf("first prompt lacks %q:\n%s", want, p)
		}
	}
	tt.Frame()
	if !tt.HasText("It adds ") || th.busy {
		t.Fatalf("answer not shown (busy %v): %q", th.busy, tt.Texts())
	}

	// A follow-up goes to the same conversation, without the context again.
	th.draft = "And B?"
	click(t, tt, "送信")
	if len(f.prompts) != 2 || f.prompts[1] != "And B?" {
		t.Fatalf("follow-up: %q", f.prompts)
	}
}

func TestAskSectionAndLines(t *testing.T) {
	a, hs := newApp(t)
	f := withFake(a)
	a.selectView(review.ViewCommit + hs[0])
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()

	// The guide's only ## section before the recap is "関数".
	click(t, tt, "💬 このセクションについて質問")
	th := a.viewThreads()[0]
	if th.kind != threadSection || th.section != "関数" {
		t.Fatalf("section thread: %+v", th)
	}

	// The + by line 3 of the excerpt of a.go (the added func A).
	var ex *block
	for i := range a.page.blocks {
		if a.page.blocks[i].diff != nil {
			ex = &a.page.blocks[i]
		}
	}
	line, ok := ex.lines.view(editor.DiffNew, 3)
	if !ok {
		t.Fatalf("line 3 not in the excerpt: %+v", ex.lines)
	}
	ex.diff.OnGutterButton(editor.DiffLineRange{Side: editor.DiffNew, Start: line, End: line})
	lt := a.viewThreads()[1]
	if lt.kind != threadLine || lt.label != "a.go:3" || !a.questionsOpen {
		t.Fatalf("line thread: %+v", lt)
	}
	lt.draft = "Is this exported on purpose?"
	a.send(lt)
	if p := f.prompts[0]; !strings.Contains(p, "about a.go:3") || !strings.Contains(p, "+func A() {}") {
		t.Fatalf("line prompt:\n%s", p)
	}

	// The line shows its thread under it.
	a.annotate(ex.diff, ex.path, ex.lines)
	if len(ex.diff.Annotations) != 1 || ex.diff.Annotations[0].Key != lt.id {
		t.Fatalf("annotations: %+v", ex.diff.Annotations)
	}

	// Asking about the same lines again shows the same thread.
	ex.diff.OnGutterButton(editor.DiffLineRange{Side: editor.DiffNew, Start: line, End: line})
	if len(a.viewThreads()) != 2 {
		t.Fatalf("duplicate thread: %d", len(a.viewThreads()))
	}

	// Threads belong to their view.
	a.selectView(review.ViewBranch)
	if len(a.viewThreads()) != 0 {
		t.Fatal("threads leaked into another view")
	}
}

func TestSplitSections(t *testing.T) {
	bs := splitSections("# T\n\nintro\n\n## One\n\ntext\n## Two\nmore")
	var got []string
	for _, b := range bs {
		got = append(got, b.section+"|"+strings.Split(b.src, "\n")[0])
	}
	if strings.Join(got, ",") != "|# T,One|## One,Two|## Two" {
		t.Fatalf("sections: %q", got)
	}
}
