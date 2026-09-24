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
