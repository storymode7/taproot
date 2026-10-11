package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/storymode7/astra/core"
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

func TestAnsiWrap(t *testing.T) {
	if got := ansiWrap("short", 10); len(got) != 1 || got[0] != "short" {
		t.Errorf("short line changed: %q", got)
	}

	line := "   1    1 │ " + green + strings.Repeat("x", 70) + "END" + reset
	rows := ansiWrap(line, 40)
	var joined strings.Builder
	for i, r := range rows {
		plain := stripANSI(r)
		if n := len([]rune(plain)); n > 40 {
			t.Errorf("row %d is %d wide, want <= 40: %q", i, n, plain)
		}
		if i > 0 {
			if !strings.HasPrefix(plain, "          │ ") {
				t.Errorf("row %d lacks the gutter: %q", i, plain)
			}
			if !strings.Contains(r, green) {
				t.Errorf("row %d lost its colour: %q", i, r)
			}
			plain = strings.TrimPrefix(plain, "          │ ")
		}
		joined.WriteString(plain)
	}
	if want := stripANSI(line); joined.String() != want {
		t.Errorf("wrapped text = %q, want %q", joined.String(), want)
	}
}

func TestRenderedDiff_WrapsLongLines(t *testing.T) {
	long := strings.Repeat("word ", 40) + "END"
	s := &core.Snippet{ID: "s", FilePath: "x.py", DiffLines: []string{"@@ -1 +1 @@", "-" + long, "+" + long + " changed"}}
	r := &Runner{diffCache: map[string][]string{}}
	var all string
	for _, l := range r.renderedDiff(s, 80) {
		if n := len([]rune(stripANSI(l))); n > 80 {
			t.Errorf("row wider than pane (%d): %q", n, stripANSI(l))
		}
		all += strings.TrimPrefix(stripANSI(l), "          │ ")
	}
	if !strings.Contains(all, "END changed") {
		t.Errorf("end of the changed line is missing:\n%s", all)
	}
}

// TestRunBat_SyntaxFromFileName checks that bat gets the file name, so it
// picks TypeScriptReact for .tsx rather than a forced plain TypeScript.
func TestRunBat_SyntaxFromFileName(t *testing.T) {
	bin := t.TempDir()
	argsFile := filepath.Join(bin, "args")
	fake := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argsFile + "'\nexec /bin/cat\n"
	if err := os.WriteFile(filepath.Join(bin, "bat"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if out := runBat("web/App.tsx", []string{"@@ -1 +1 @@", "+const a = <p>Don't</p>;"}); out == nil {
		t.Fatal("bat did not run")
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "--file-name=web/App.tsx\n") || strings.Contains(string(args), "--language") {
		t.Errorf("bat args = %q, want --file-name=web/App.tsx and no --language", args)
	}
}
