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

	if a.rightOpen {
		t.Fatal("the right pane should start closed")
	}
	rightClick(t, tt, "以上。")
	if err := tt.ChooseMenuItem("変更全体について質問"); err != nil {
		t.Fatalf("menu %q: %v", tt.Menu(), err)
	}
	tt.Frame()
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

	// Right-clicking the section's heading offers to ask about it.
	rightClick(t, tt, "関数")
	if err := tt.ChooseMenuItem("このセクションについて質問"); err != nil {
		t.Fatalf("menu %q: %v", tt.Menu(), err)
	}
	tt.Frame()
	th := a.viewThreads()[0]
	if th.kind != threadSection || th.section != "関数" || !a.rightOpen || !a.questionsOpen {
		t.Fatalf("section thread: %+v (right %v)", th, a.rightOpen)
	}

	// A line of an excerpt, asked about from its menu.
	rightClick(t, tt, "func A() {}")
	if err := tt.ChooseMenuItem("a.go:3 について質問"); err != nil {
		t.Fatalf("menu %q: %v", tt.Menu(), err)
	}
	tt.Frame()
	lt := a.viewThreads()[1]
	if lt.kind != threadLine || lt.label != "a.go:3" {
		t.Fatalf("line thread: %+v", lt)
	}
	lt.draft = "Is this exported on purpose?"
	a.send(lt)
	if p := f.prompts[0]; !strings.Contains(p, "about a.go:3") || !strings.Contains(p, "+func A() {}") {
		t.Fatalf("line prompt:\n%s", p)
	}

	// The line shows its conversation under it.
	tt.Frame()
	if !tt.HasText("💬 a.go:3 · 1") {
		t.Fatalf("no mark under the line: %q", tt.Texts())
	}

	// Asking about the same lines again shows the same thread.
	a.askLines("a.go", editor.DiffNew, 3, 3)
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

// rightClick hovers over the text first, as a pointer does, then right-clicks it.
func rightClick(t *testing.T, tt *ui.Tester, text string) {
	t.Helper()
	r, ok := tt.Find(text)
	if !ok {
		t.Fatalf("no %q in %q", text, tt.Texts())
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	tt.Frame() // hover reports the frame before
	if err := tt.RightClick(text); err != nil {
		t.Fatal(err)
	}
}
