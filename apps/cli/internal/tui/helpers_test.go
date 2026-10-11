package tui

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/storymode7/astra/apps/cli/internal/state"
	"github.com/storymode7/astra/core"
)

// testRunner builds a Runner over snippets, each its own root, with its
// review state in a temp dir.
func testRunner(t *testing.T, snippets ...*core.Snippet) *Runner {
	t.Helper()
	store, err := state.Open(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	return NewRunner(core.BuildForest(snippets, nil), store, false, false, nil)
}

// useHighlighter sets the active highlighter for one test.
func useHighlighter(t *testing.T, h Highlighter) {
	t.Helper()
	old := activeHighlighter
	activeHighlighter = h
	t.Cleanup(func() { activeHighlighter = old })
}

// captureStdout returns what f prints to stdout.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	rd, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	out := make(chan string)
	go func() {
		b, _ := io.ReadAll(rd)
		rd.Close()
		out <- string(b)
	}()
	stdout := os.Stdout
	os.Stdout = w
	func() {
		defer func() {
			os.Stdout = stdout
			w.Close()
		}()
		f()
	}()
	return <-out
}

// frame renders r on a rows x cols terminal and returns the frame's rows.
func frame(t *testing.T, r *Runner, rows, cols int) []string {
	t.Helper()
	size := termSize
	termSize = func() (int, int) { return rows, cols }
	defer func() { termSize = size }()
	out := captureStdout(t, r.render)
	return strings.Split(strings.TrimPrefix(out, "\033[?25l\033[2J\033[H"), "\n")
}
