package tui

import (
	"strings"
	"testing"

	"github.com/storymode7/astra/core"
)

// mixedSnippets is a function, the imports of a Go file, and hunks of two
// files in languages Astra does not read.
func mixedSnippets() []*core.Snippet {
	return []*core.Snippet{
		{ID: "txt", FilePath: "a.txt", NodeType: "hunk", StartLine: 1, DiffLines: []string{"@@ -1 +1 @@", "-a", "+b"}},
		{ID: "imp", FilePath: "b.go", Language: "go", SymbolPath: "imports", NodeType: "imports", StartLine: 3, DiffLines: []string{"@@ -3 +3 @@", `-import "os"`, `+import "io"`}},
		{ID: "fn", FilePath: "c.go", Language: "go", SymbolPath: "run", NodeType: "function", StartLine: 1, DiffLines: []string{"@@ -1 +1 @@", "-func run() {}", "+func run() { }"}},
		{ID: "yml", FilePath: "ci.yml", NodeType: "hunk", StartLine: 1, DiffLines: []string{"@@ -1 +1 @@", "-on: push", "+on: [push]"}},
	}
}

func TestTree_OtherFilesHaveAHeadingTheCursorSkips(t *testing.T) {
	r := testRunner(t, mixedSnippets()...)
	var rows []string
	for _, row := range r.tree.visible {
		if row.node == nil {
			rows = append(rows, "heading")
		} else {
			rows = append(rows, row.node.Snippet.ID)
		}
	}
	if got, want := strings.Join(rows, " "), "fn imp heading txt yml"; got != want {
		t.Fatalf("rows = %s, want %s", got, want)
	}

	lines := frame(t, r, 24, 120)
	at := -1
	for i, l := range lines {
		if strings.HasPrefix(stripANSI(l), "Other files  Plain hunks, no call info. Astra reads Go, JavaScript, TypeScript, Python, Java and Rust.") {
			at = i
		}
	}
	if at < 0 || !strings.Contains(stripANSI(lines[at-1]), "b.go (imports)") || !strings.Contains(stripANSI(lines[at+1]), "a.txt (hunks)") {
		t.Errorf("no heading between the imports and the other files:\n%s", stripANSI(strings.Join(lines, "\n")))
	}

	var visited []string
	for range 4 {
		r.moveCursor(1)
		visited = append(visited, r.currentNode().Snippet.ID)
	}
	for range 4 {
		r.moveCursor(-1)
		visited = append(visited, r.currentNode().Snippet.ID)
	}
	if got, want := strings.Join(visited, " "), "imp txt yml yml txt imp fn fn"; got != want {
		t.Errorf("J then K visit %s, want %s", got, want)
	}
	r.moveCursor(len(r.tree.visible) - 1 - r.cursor)
	if id := r.currentNode().Snippet.ID; id != "yml" {
		t.Errorf("End goes to %s, want yml", id)
	}
}

func TestTree_OnlyOtherFilesStartBelowTheHeading(t *testing.T) {
	r := testRunner(t, mixedSnippets()[3], mixedSnippets()[0])
	if n := r.currentNode(); n == nil || n.Snippet.ID != "txt" {
		t.Fatalf("cursor starts on %v, want the first file", n)
	}
	r.moveCursor(-r.cursor) // Home
	if n := r.currentNode(); n == nil || n.Snippet.ID != "txt" {
		t.Errorf("Home goes to %v, want the first file", n)
	}
	r.moveCursor(-1)
	if n := r.currentNode(); n == nil || n.Snippet.ID != "txt" {
		t.Errorf("K above the first file goes to %v", n)
	}
	if lines := frame(t, r, 24, 120); !strings.HasPrefix(stripANSI(lines[2]), "Other files") {
		t.Errorf("the tree does not open with the heading: %q", stripANSI(lines[2]))
	}
}

func TestView_OtherFilesHaveAHeading(t *testing.T) {
	useHighlighter(t, HighlightBuiltin)
	r := testRunner(t, mixedSnippets()...)
	for _, plain := range []bool{true, false} {
		Plain = plain
		out := stripANSI(captureStdout(t, func() { View(r.flat, r.store) }))
		Plain = false
		heading := strings.Index(out, "Other files · Plain hunks, no call info.")
		if heading < 0 || strings.Count(out, "Other files") != 1 {
			t.Fatalf("plain=%v: want one heading:\n%s", plain, out)
		}
		if imp, txt := strings.Index(out, "b.go:3"), strings.Index(out, "a.txt:1"); imp < 0 || imp > heading || txt < heading {
			t.Errorf("plain=%v: the heading is not between the imports and the other files:\n%s", plain, out)
		}
	}
}
