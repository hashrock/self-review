package app

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/hashrock/self-review/internal/agent"
	"github.com/hashrock/self-review/internal/repo"
	"github.com/hashrock/self-review/internal/review"
)

// TestLiveUI drives the window, without one on screen, against a real agent
// (SELF_REVIEW_AGENT_LIVE=claude|gemini|codex) on the repository in
// SELF_REVIEW_REPO, which must have a change to src/physics.js. It asks
// about a line from the right-click menu, follows up in the same
// conversation, and checks that the agent cannot write files. Screenshots go
// to SELF_REVIEW_SHOTS when set.
func TestLiveUI(t *testing.T) {
	name, dir := os.Getenv("SELF_REVIEW_AGENT_LIVE"), os.Getenv("SELF_REVIEW_REPO")
	if name == "" || dir == "" {
		t.Skip("set SELF_REVIEW_AGENT_LIVE and SELF_REVIEW_REPO")
	}
	r, err := repo.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := review.Open(r, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	a := New(ws, "", "ja")
	a.setAgent(agent.Agents[name])
	defer a.agents.close()
	a.reload()
	tt := ui.NewTester(a.View, 1440, 1400) // tall, to keep the line in view
	tt.Frame()
	shot := func(n string) {
		if out := os.Getenv("SELF_REVIEW_SHOTS"); out != "" {
			tt.Frame()
			f, _ := os.Create(filepath.Join(out, n+".png"))
			png.Encode(f, tt.Image())
			f.Close()
		}
	}

	// 1. Right-click the changed line and ask about it.
	line := "    if (settings.shiftLock && this.gear === 'P' && this.input.brake < 0.2) {"
	rightClick(t, tt, line)
	var item string
	for _, it := range tt.Menu() {
		if strings.HasPrefix(it, "src/physics.js:") && strings.HasSuffix(it, "について質問") && !strings.Contains(it, "-") {
			item = it
		}
	}
	if item == "" {
		t.Fatalf("no line item in the menu %q", tt.Menu())
	}
	if err := tt.ChooseMenuItem(item); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.rightOpen || !a.questionsOpen || len(a.viewThreads()) != 1 {
		t.Fatalf("questions did not open: right %v questions %v threads %d", a.rightOpen, a.questionsOpen, len(a.viewThreads()))
	}
	th := a.viewThreads()[0]
	t.Logf("asked from the menu item %q: thread %q", item, th.label)

	ask := func(q string) message {
		t.Helper()
		th.draft = q
		tt.Frame()
		click(t, tt, "送信") // runs the turn to its end: no window, so in place
		tt.Frame()
		last := th.msgs[len(th.msgs)-1]
		if th.busy {
			t.Fatal("still busy after the turn")
		}
		t.Logf("Q: %s\nA (%d chars, tools %v, err %q):\n%s", q, len(last.text), last.tools, last.err, last.text)
		return last
	}

	// 2. The question and its answer.
	m := ask("この条件で、シフトロックが OFF のときに P から出られる条件をひと言で。")
	if m.err != "" || m.text == "" {
		t.Fatalf("no answer: %+v", m)
	}
	if !tt.HasText("💬 " + th.label + " · 1") {
		t.Errorf("no mark under the line: %q", tt.Texts())
	}
	shot("live-1")

	// 3. A follow-up in the same conversation knows the first.
	m = ask("さっきの答えを英語で一文にして。")
	if m.err != "" || m.text == "" {
		t.Fatalf("no follow-up answer: %+v", m)
	}

	// 4. A request to change code is refused, and nothing is written.
	target := filepath.Join(dir, "SHOULD_NOT_EXIST.txt")
	m = ask("リポジトリ直下に SHOULD_NOT_EXIST.txt を作って中身を hi にして。")
	if _, err := os.Stat(target); err == nil {
		os.Remove(target)
		t.Fatal("the agent wrote a file from the window")
	}
	if m.text == "" && len(m.denied) == 0 {
		t.Error("the refused turn shows neither an answer nor what was refused")
	}
	tt.Frame()
	if len(m.denied) > 0 && !tt.HasText("読み取り専用のため断りました: "+strings.Join(m.denied, ", ")) {
		t.Errorf("refusal not shown: %q", tt.Texts())
	}
	shot("live-2")
}
