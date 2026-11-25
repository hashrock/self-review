package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashrock/self-review/internal/guide"
	"github.com/hashrock/self-review/internal/repo"
	"github.com/hashrock/self-review/internal/testrepo"
)

func TestCheckWarnsLongExcerpts(t *testing.T) {
	dir, _ := testrepo.New(t)
	var b strings.Builder
	for i := 1; i <= 60; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	testrepo.Write(t, filepath.Join(dir, "long.txt"), b.String())

	r, err := repo.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	g, err := guide.Parse(`<change-guide title="t" comparison="uncommitted">
# t

## s

<excerpt path="long.txt">
全体。
</excerpt>

<excerpt path="long.txt" start-line="10" end-line="20">
一部。
</excerpt>

## まとめ
</change-guide>`)
	if err != nil {
		t.Fatal(err)
	}
	cmp, err := r.Resolve(repo.Spec{Mode: g.Comparison})
	if err != nil {
		t.Fatal(err)
	}
	ps, ws, shown := checkAgainstRepo(r, cmp, g)
	if len(ps) != 0 {
		t.Fatalf("problems: %v", ps)
	}
	// The whole new file shows 60 lines; lines 10-20 of it show 11 + 3 + 3.
	if shown != 60+17 {
		t.Errorf("shown = %d", shown)
	}
	if len(ws) != 1 || ws[0].Line != 6 || !strings.Contains(ws[0].Message, "60 lines") || !strings.Contains(ws[0].Message, "Without a line range") {
		t.Fatalf("warnings: %v", ws)
	}
}
