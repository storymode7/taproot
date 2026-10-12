package tui

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/storymode7/astra/apps/cli/internal/state"
	"github.com/storymode7/astra/core"
)

// manySnippets returns n function snippets with a diff of size added lines.
func manySnippets(n, size int) []*core.Snippet {
	var out []*core.Snippet
	for i := 0; i < n; i++ {
		diff := []string{fmt.Sprintf("@@ -1,0 +1,%d @@", size)}
		for j := 0; j < size; j++ {
			diff = append(diff, fmt.Sprintf("+\tline %d of a long hunk", j))
		}
		out = append(out, &core.Snippet{
			ID: fmt.Sprintf("s%d", i), FilePath: "pkg/main.go", SymbolPath: fmt.Sprintf("fn%d", i),
			NodeType: "function", StartLine: 1, DiffLines: diff,
		})
	}
	return out
}

func assertFits(t *testing.T, lines []string, rows, cols int) {
	t.Helper()
	if len(lines) > rows {
		t.Errorf("%dx%d: frame has %d rows, so %d scroll off", rows, cols, len(lines), len(lines)-rows)
	}
	for i, l := range lines {
		if w := cells(l); w > cols {
			t.Errorf("%dx%d: row %d is %d columns: %q", rows, cols, i, w, stripANSI(l))
		}
	}
}

func TestRender_FrameFitsTerminal(t *testing.T) {
	r := testRunner(t, manySnippets(10, 40)...)
	r.applyMode = true
	for _, size := range [][2]int{{40, 120}, {24, 80}, {13, 80}, {12, 80}, {12, 60}, {11, 120}, {8, 40}, {6, 20}} {
		rows, cols := size[0], size[1]
		for _, cursor := range []int{0, 1, 9} {
			r.cursor, r.diffScroll = cursor, 0
			lines := frame(t, r, rows, cols)
			assertFits(t, lines, rows, cols)
			if !strings.Contains(lines[0], "Astra") {
				t.Errorf("%dx%d: first row is not the header: %q", rows, cols, stripANSI(lines[0]))
			}
			if cols >= 60 && !strings.Contains(lines[0], "[staging]") {
				t.Errorf("%dx%d: header lost [staging]: %q", rows, cols, stripANSI(lines[0]))
			}
		}
	}
}

// TestRender_NoColor: with NoColor the frame keeps only bold, dim and
// inverse text, whichever highlighter is installed, and the diff keeps its
// +/- markers.
func TestRender_NoColor(t *testing.T) {
	useHighlighter(t, HighlightDelta)
	r := testRunner(t, manySnippets(3, 5)...)
	r.applyMode = true
	r.Skipped = []core.SkippedFile{{Path: "logo.png", Reason: "binary"}}
	for i, status := range []string{"accepted", "rejected", "reviewed"} {
		if err := r.store.Set(r.key(r.flat[i]), status, ""); err != nil {
			t.Fatal(err)
		}
	}
	sgr := regexp.MustCompile("\x1b\\[([0-9;]*)m")
	colors := func() []string {
		r.diffCache = make(map[string][]string)
		r.status = "could not stage: no"
		var found []string
		for _, m := range sgr.FindAllStringSubmatch(strings.Join(frame(t, r, 24, 80), "\n"), -1) {
			switch m[1] {
			case "0", "1", "2", "7", "27":
			default:
				found = append(found, m[0])
			}
		}
		return found
	}
	if len(colors()) == 0 {
		t.Fatal("the frame has no color to turn off")
	}
	NoColor = true
	t.Cleanup(func() { NoColor = false })
	if found := colors(); len(found) > 0 {
		t.Errorf("NoColor frame has color escapes %q", found)
	}
	if got := stripANSI(strings.Join(frame(t, r, 24, 80), "\n")); !strings.Contains(got, "│ +    line 0 of a long hunk") {
		t.Errorf("NoColor diff lost its + markers:\n%s", got)
	}
}

func TestRender_CountsLinesBelowThePane(t *testing.T) {
	useHighlighter(t, HighlightBuiltin)
	// At 24 rows one tree row leaves the diff pane 18 rows.
	for size, want := range map[int]string{18: "… 1 more line (j/k to scroll)", 19: "… 2 more lines (j/k to scroll)"} {
		r := testRunner(t, manySnippets(1, size)...)
		if got := stripANSI(strings.Join(frame(t, r, 24, 80), "\n")); !strings.Contains(got, want+"\n") {
			t.Errorf("%d diff lines below a header: frame lacks %q:\n%s", size, want, got)
		}
	}
}

func TestRender_StageErrorStaysOnOneLine(t *testing.T) {
	r := testRunner(t, manySnippets(3, 40)...)
	r.applyMode = true
	r.stage = func(*core.Snippet, bool) error {
		return errors.New("git apply --cached: exit status 1: error: patch failed: m.go:3\nerror: m.go: patch does not apply\n")
	}
	fixedSize(t, 12, 80)
	captureStdout(t, func() { r.setStatus("accepted") })
	assertFits(t, frame(t, r, 12, 80), 12, 80)
	lines := frame(t, r, 12, 160)
	want := "could not stage: git apply --cached: exit status 1: error: patch failed: m.go:3 error: m.go: patch does not apply"
	if last := stripANSI(lines[len(lines)-1]); last != want {
		t.Errorf("status line = %q, want %q", last, want)
	}
}

func TestSetStatus_ReportsUnsavedState(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	gitDir := t.TempDir()
	store, err := state.Open(gitDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(gitDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(gitDir, 0o755) })

	r := NewRunner(core.BuildForest(manySnippets(3, 2), nil), store, true, false, nil)
	fixedSize(t, 24, 80)
	captureStdout(t, func() { r.setStatus("reviewed") })
	if r.status != "review state not saved: permission denied" {
		t.Errorf("status = %q, want the save error", r.status)
	}
	if r.cursor != 0 {
		t.Errorf("cursor moved to row %d although the verdict was not saved", r.cursor)
	}
}

func TestRender_HintShowsHowToQuit(t *testing.T) {
	r := testRunner(t, manySnippets(3, 5)...)
	lines := frame(t, r, 24, 80)
	if hint := stripANSI(lines[len(lines)-1]); !strings.Contains(hint, "? help") || !strings.Contains(hint, "q quit") {
		t.Errorf("hint line at 80 columns = %q, want ? help and q quit on it", hint)
	}
}

func TestRenderHelp_FitsTerminal(t *testing.T) {
	r := testRunner(t, manySnippets(3, 5)...)
	for _, size := range [][2]int{{24, 80}, {12, 60}, {6, 20}} {
		rows, cols := size[0], size[1]
		fixedSize(t, rows, cols)
		feedStdin(t, " ").Close()
		out := captureStdout(t, r.renderHelp)
		// The help screen is followed by the frame drawn when it closes.
		help := strings.TrimPrefix(out[:strings.Index(out, "\033[?25l")], "\033[2J\033[H")
		lines := strings.Split(help, "\n")
		assertFits(t, lines, rows, cols)
		if !strings.Contains(lines[0], "keyboard shortcuts") && !strings.Contains(lines[0], "Astra") {
			t.Errorf("%dx%d: first help row = %q, want the title", rows, cols, stripANSI(lines[0]))
		}
		if rows >= 24 && (!strings.Contains(help, "Move down the tree") || !strings.Contains(help, "Reject snippet") || !strings.Contains(help, "Quit")) {
			t.Errorf("%dx%d: help lost key lines:\n%s", rows, cols, stripANSI(help))
		}
	}
}

func TestRenderHelp_ListsFilesNotShownOnAShortScreen(t *testing.T) {
	r := testRunner(t, manySnippets(3, 5)...)
	r.Skipped = []core.SkippedFile{{Path: "logo.png", Reason: "binary"}, {Path: "b.py", OldPath: "a.py", Reason: "renamed"}}
	fixedSize(t, 24, 80)
	feedStdin(t, " ").Close()
	out := captureStdout(t, r.renderHelp)
	help := strings.TrimPrefix(out[:strings.Index(out, "\033[?25l")], "\033[2J\033[H")
	assertFits(t, strings.Split(help, "\n"), 24, 80)
	plain := stripANSI(help)
	for _, want := range []string{"Not shown: binary logo.png · renamed a.py → b.py", "Quit", "Press any key"} {
		if !strings.Contains(plain, want) {
			t.Errorf("24x80 help lacks %q:\n%s", want, plain)
		}
	}
}

func TestRender_HeaderCountsFilesNotShown(t *testing.T) {
	r := testRunner(t, manySnippets(3, 5)...)
	r.applyMode = true
	r.Skipped = []core.SkippedFile{{Path: "logo.png", Reason: "binary"}, {Path: "b.py", OldPath: "a.py", Reason: "renamed"}}
	for _, cols := range []int{120, 80} {
		lines := frame(t, r, 24, cols)
		assertFits(t, lines, 24, cols)
		if h := stripANSI(lines[0]); !strings.Contains(h, "2 not shown") || !strings.Contains(h, "[staging]") {
			t.Errorf("%d columns: header does not count the files not shown: %q", cols, h)
		}
	}
	lines := frame(t, r, 24, 60)
	assertFits(t, lines, 24, 60)
	if h := stripANSI(lines[0]); !strings.Contains(h, "[staging]") || !strings.Contains(h, "█") && !strings.Contains(h, "░") {
		t.Errorf("60 columns: the count must give way before the bar and [staging]: %q", h)
	}
}
