package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	store, err := state.Open(gitDir, "test")
	if err != nil {
		t.Fatal(err)
	}
	reviews := filepath.Join(gitDir, "astra", "reviews")
	if err := os.Chmod(reviews, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(reviews, 0o755) })

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
