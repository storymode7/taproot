package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/astra-review/astra/core"
)

func TestEditorArgs(t *testing.T) {
	for editor, want := range map[string][]string{
		"nvim":        {"nvim", "+12", "/r/a.go"},
		"code -w":     {"code", "-w", "-g", "/r/a.go:12"},
		"/usr/bin/hx": {"/usr/bin/hx", "/r/a.go:12"},
	} {
		name, args := editorArgs(editor, "/r/a.go", 12)
		if got := append([]string{name}, args...); !reflect.DeepEqual(got, want) {
			t.Errorf("editorArgs(%q) = %v, want %v", editor, got, want)
		}
	}
}

func TestFirstChangedLine(t *testing.T) {
	s := &core.Snippet{StartLine: 5, DiffLines: []string{
		"@@ -10,4 +20,5 @@ func x() {", " a", " b", "+c", " d",
	}}
	if got := firstChangedLine(s); got != 22 {
		t.Errorf("firstChangedLine = %d, want 22", got)
	}
}

func TestWithLineNumbers(t *testing.T) {
	diff := []string{"@@ -3,2 +3,2 @@", " keep", "-old", "+new"}
	got := withLineNumbers(diff, diff)
	want := []string{"          │ @@", "   3    3 │  keep", "   4      │ -old", "        4 │ +new"}
	for i := range want {
		if plain := stripANSI(got[i]); !strings.HasPrefix(plain, want[i]) {
			t.Errorf("line %d = %q, want prefix %q", i, plain, want[i])
		}
	}
}

func TestRenderSegments_TruncatesBeforeStyling(t *testing.T) {
	out := renderSegments([]segment{{"abcdef", bold}, {"ghij", dim}}, 8, false)
	if plain := stripANSI(out); plain != "abcdef…" {
		t.Errorf("plain = %q, want %q", plain, "abcdef…")
	}
	if strings.Count(out, reset) != 2 {
		t.Errorf("each styled segment must be reset: %q", out)
	}
}

func TestWithLineNumbers_NoNewlineMarker(t *testing.T) {
	diff := []string{"@@ -1,2 +1,3 @@", " a", "-b", `\ No newline at end of file`, "+b", "+c"}
	got := withLineNumbers(diff, diff)
	if plain := stripANSI(got[4]); !strings.HasPrefix(plain, "        2 │") {
		t.Errorf("+b must be new line 2 (marker is not a line), got %q", plain)
	}
}

func TestScrollStep(t *testing.T) {
	for _, c := range []struct{ scroll, max, dir, wantScroll, wantMove int }{
		{0, 0, 1, 0, 1},   // fits on one page: j moves on
		{0, 0, -1, 0, -1}, // fits on one page: k moves back
		{0, 12, 1, 5, 0},
		{10, 12, 1, 12, 0}, // clamps to the last full page
		{12, 12, 1, 12, 1}, // at the bottom: j moves on
		{3, 12, -1, 0, 0},
		{0, 12, -1, 0, -1}, // at the top: k moves back
	} {
		s, m := scrollStep(c.scroll, c.max, c.dir)
		if s != c.wantScroll || m != c.wantMove {
			t.Errorf("scrollStep(%d, %d, %d) = (%d, %d), want (%d, %d)",
				c.scroll, c.max, c.dir, s, m, c.wantScroll, c.wantMove)
		}
	}
}
