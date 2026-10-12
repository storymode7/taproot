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
	store, err := state.Open(t.TempDir())
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

// fixedSize makes the terminal rows x cols for one test.
func fixedSize(t *testing.T, rows, cols int) {
	t.Helper()
	size := termSize
	termSize = func() (int, int) { return rows, cols }
	t.Cleanup(func() { termSize = size })
}

// frame renders r on a rows x cols terminal and returns the frame's rows.
func frame(t *testing.T, r *Runner, rows, cols int) []string {
	t.Helper()
	fixedSize(t, rows, cols)
	out := captureStdout(t, r.render)
	return strings.Split(strings.TrimPrefix(out, "\033[?25l\033[2J\033[H"), "\n")
}

// feedStdin makes stdin a pipe that holds input. The caller closes the
// returned write end to signal the end of input.
func feedStdin(t *testing.T, input string) *os.File {
	t.Helper()
	rd, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin := os.Stdin
	os.Stdin = rd
	t.Cleanup(func() {
		os.Stdin = stdin
		rd.Close()
		w.Close()
	})
	if _, err := w.WriteString(input); err != nil {
		t.Fatal(err)
	}
	return w
}
