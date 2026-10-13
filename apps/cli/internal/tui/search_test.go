package tui

import (
	"testing"

	"github.com/storymode7/taproot/core"
)

func TestCompileSearch_Smartcase(t *testing.T) {
	for pattern, want := range map[string]map[string]bool{
		"foo": {"Foo": true, "FOO": true},
		"Foo": {"Foo": true, "foo": false},
	} {
		re, err := compileSearch(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for text, match := range want {
			if re.MatchString(text) != match {
				t.Errorf("/%s/ on %q = %v, want %v", pattern, text, !match, match)
			}
		}
	}
	if _, err := compileSearch("("); err == nil {
		t.Error("unbalanced pattern must fail")
	}
}

func TestSnippetMatches(t *testing.T) {
	s := &core.Snippet{FilePath: "pkg/a.go", SymbolPath: "Runner.Run", DiffLines: []string{"@@", "+x := parse(y)"}}
	for pattern, want := range map[string]bool{"a\\.go": true, "runner": true, "parse": true, "nope": false} {
		re, _ := compileSearch(pattern)
		if got := snippetMatches(re, s); got != want {
			t.Errorf("/%s/ = %v, want %v", pattern, got, want)
		}
	}
}

func TestMatchingRows_IgnoresColour(t *testing.T) {
	re, _ := compileSearch("needle")
	rows := []string{"hay", green + "nee" + reset + "dle", "NEEDLE"}
	if got := matchingRows(re, rows); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("rows = %v, want [1 2]", got)
	}
}

func TestNextHit(t *testing.T) {
	// snippet 0: rows 2 and 7; snippet 1: none; snippet 2: header only.
	hits := map[int][]int{0: {2, 7}, 2: {headerHit}}
	rows := func(i int) []int { return hits[i] }
	for _, c := range []struct {
		cur  searchHit
		dir  int
		want searchHit
	}{
		{searchHit{0, beforeAll}, 1, searchHit{0, 2}}, // fresh search starts in the focused snippet
		{searchHit{0, 2}, 1, searchHit{0, 7}},         // next row of the same snippet
		{searchHit{0, 7}, 1, searchHit{2, headerHit}}, // skips the snippet with no hits
		{searchHit{2, headerHit}, 1, searchHit{0, 2}}, // wraps
		{searchHit{0, 2}, -1, searchHit{2, headerHit}},
		{searchHit{1, afterAll}, -1, searchHit{0, 7}}, // fresh backward search lands on the last row
		{searchHit{0, 7}, -1, searchHit{0, 2}},
	} {
		got, ok := nextHit(c.cur, c.dir, 3, rows)
		if !ok || got != c.want {
			t.Errorf("nextHit(%v, %d) = %v,%v want %v", c.cur, c.dir, got, ok, c.want)
		}
	}
	if _, ok := nextHit(searchHit{0, beforeAll}, 1, 3, func(int) []int { return nil }); ok {
		t.Error("no hits anywhere must report not found")
	}
	only := func(i int) []int {
		if i == 1 {
			return []int{4}
		}
		return nil
	}
	if got, ok := nextHit(searchHit{1, 4}, 1, 3, only); !ok || got != (searchHit{1, 4}) {
		t.Errorf("lone hit must be found again, got %v,%v", got, ok)
	}
}
