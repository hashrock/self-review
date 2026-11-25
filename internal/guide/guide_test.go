package guide

import (
	"strings"
	"testing"
)

const sample = `前置きは無視される
<change-guide title="Make &quot;P&quot; optional" comparison="branch" base="main">
# タイトル

概要。

## 設定値

<excerpt path="a/b.js" start-line="5" end-line="8">
紹介文 <code>
</excerpt>

補足。

<related-location label="Car.requestGear" path="a/c.js" start-line="10"/>
<related-location label="x" path="a/d.js" start-line="1" end-line="3" />

## まとめ

おわり。
</change-guide>
`

func TestParse(t *testing.T) {
	g, err := Parse(sample)
	if err != nil {
		t.Fatal(err)
	}
	if g.Title != `Make "P" optional` || g.Comparison != Branch || g.Base != "main" {
		t.Fatalf("attrs: %+v", g)
	}
	kinds := []BlockKind{}
	for _, b := range g.Blocks {
		kinds = append(kinds, b.Kind)
	}
	want := []BlockKind{KindMarkdown, KindExcerpt, KindMarkdown, KindRelated, KindRelated, KindMarkdown}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", kinds, want)
		}
	}
	ex := g.Blocks[1]
	if ex.Path != "a/b.js" || ex.StartLine != 5 || ex.EndLine != 8 || ex.Markdown != "紹介文 <code>" {
		t.Fatalf("excerpt: %+v", ex)
	}
	if ex.Line != 9 {
		t.Fatalf("excerpt line = %d, want 9", ex.Line)
	}
	r := g.Blocks[4]
	if r.Label != "x" || r.StartLine != 1 || r.EndLine != 3 {
		t.Fatalf("related: %+v", r)
	}
	if ps := g.Validate(); len(ps) != 0 {
		t.Fatalf("problems: %v", ps)
	}
}

func TestValidate(t *testing.T) {
	g, err := Parse(`<change-guide comparison="direct">
## a
<excerpt path="/abs" start-line="9" end-line="3"></excerpt>
<related-location path="x"/>
</change-guide>`)
	if err != nil {
		t.Fatal(err)
	}
	var msgs []string
	for _, p := range g.Validate() {
		msgs = append(msgs, p.String())
	}
	all := strings.Join(msgs, "\n")
	for _, want := range []string{"no title", "previous-version", "worktree-relative Unix path", "end-line (3)", "no introduction", "no label", "no start-line", "# heading", "recap"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing problem containing %q in:\n%s", want, all)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, src := range []string{
		"no guide",
		`<change-guide title="x" comparison="branch"># x`,
		`<change-guide title="x" comparison="branch"><excerpt path="a">x</change-guide>`,
		`<change-guide title="x" comparison="branch"><excerpt path="a" start-line="x">i</excerpt></change-guide>`,
	} {
		if _, err := Parse(src); err == nil {
			t.Errorf("Parse(%q) succeeded", src)
		}
	}
}

func TestTagsInCodeAreProse(t *testing.T) {
	src := "<change-guide title=\"t\" comparison=\"branch\">\n" +
		"# t\n\n" +
		"`<excerpt>` と `` <related-location/> `` と `</change-guide>` は文中のコード。\n\n" +
		"```html\n<excerpt path=\"fenced\">x</excerpt>\n```\n\n" +
		"~~~~\n<related-location label=\"f\" path=\"f\" start-line=\"1\"/>\n```\n~~~~\n\n" +
		"## s\n\n" +
		"<excerpt path=\"real.go\">\n紹介文の `<excerpt>` と `</excerpt>`\n</excerpt>\n\n" +
		"閉じない ` のあとの <related-location label=\"r\" path=\"r.go\" start-line=\"2\"/>\n\n" +
		"## まとめ\n</change-guide>\n"
	g, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	var tags []string
	for _, b := range g.Blocks {
		if b.Kind != KindMarkdown {
			tags = append(tags, string(b.Kind)+":"+b.Path)
		}
	}
	if strings.Join(tags, ",") != "excerpt:real.go,related:r.go" {
		t.Fatalf("tags = %v", tags)
	}
	if g.Blocks[1].Markdown != "紹介文の `<excerpt>` と `</excerpt>`" {
		t.Fatalf("intro = %q", g.Blocks[1].Markdown)
	}
	if !strings.Contains(g.Blocks[0].Markdown, "<excerpt path=\"fenced\">") {
		t.Fatalf("fenced code lost: %q", g.Blocks[0].Markdown)
	}
	if ps := g.Validate(); len(ps) != 0 {
		t.Fatalf("problems: %v", ps)
	}
}

func TestMaskCodeKeepsOffsets(t *testing.T) {
	for _, s := range []string{"a `日本語` b", "```\n漢字\n```\nx", "`` a ` b ``", "``` unclosed\n<excerpt>"} {
		m := maskCode(s)
		if len(m) != len(s) || strings.Count(m, "\n") != strings.Count(s, "\n") {
			t.Errorf("maskCode(%q) = %q", s, m)
		}
		if strings.ContainsAny(m, "`<漢日") && !strings.Contains(s, "unclosed") {
			t.Errorf("maskCode(%q) left code: %q", s, m)
		}
	}
}
