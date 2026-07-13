package engine

import (
	"strings"
	"testing"

	"github.com/astra-review/astra/apps/cli/internal/diff"
)

// mockFetcher satisfies BlobFetcher with fixed source bytes keyed by path.
type mockFetcher struct {
	blobs map[string][]byte
}

func (m *mockFetcher) FileBlob(req diff.BlobRequest) ([]byte, error) {
	return m.blobs[req.Path], nil
}

// ---------------------------------------------------------------------------
// Entity splitting: two distinct functions → two snippets
// ---------------------------------------------------------------------------

func TestBuildSnippets_EntitySplitting_Go(t *testing.T) {
	src := []byte(`package foo

func Alpha() {
	doSomething()
}

func Beta() {
	doOther()
}
`)

	files := []*diff.FileDiff{
		{
			Path:     "foo.go",
			OldPath:  "foo.go",
			Language: "go",
			Hunks: []diff.Hunk{
				{
					OldStart: 3, OldLines: 3, NewStart: 3, NewLines: 3,
					Lines: []string{"@@ -3,3 +3,3 @@", " func Alpha() {", "-\tdoSomething()", "+\tdoSomethingNew()"},
				},
				{
					OldStart: 7, OldLines: 3, NewStart: 7, NewLines: 3,
					Lines: []string{"@@ -7,3 +7,3 @@", " func Beta() {", "-\tdoOther()", "+\tdoOtherNew()"},
				},
			},
		},
	}

	fetcher := &mockFetcher{blobs: map[string][]byte{"foo.go": src}}
	snippets := BuildSnippets(files, fetcher)

	if len(snippets) != 2 {
		t.Fatalf("expected 2 snippets (one per function), got %d", len(snippets))
	}
	bySymbol := make(map[string]*Snippet)
	for _, s := range snippets {
		bySymbol[s.SymbolPath] = s
	}
	if _, ok := bySymbol["Alpha"]; !ok {
		t.Errorf("expected snippet for Alpha, got symbols: %v", symbolPaths(snippets))
	}
	if _, ok := bySymbol["Beta"]; !ok {
		t.Errorf("expected snippet for Beta, got symbols: %v", symbolPaths(snippets))
	}
}

// ---------------------------------------------------------------------------
// Hunk merging: two hunks inside the same function → one snippet
// ---------------------------------------------------------------------------

func TestBuildSnippets_EntityMerging_Go(t *testing.T) {
	src := []byte(`package foo

func Process(items []string) []string {
	var out []string
	for _, item := range items {
		out = append(out, item)
	}
	return out
}
`)

	files := []*diff.FileDiff{
		{
			Path:     "proc.go",
			OldPath:  "proc.go",
			Language: "go",
			Hunks: []diff.Hunk{
				{
					OldStart: 4, OldLines: 1, NewStart: 4, NewLines: 1,
					Lines: []string{"@@ -4,1 +4,1 @@", "-\tvar out []string", "+\tout := make([]string, 0, len(items))"},
				},
				{
					OldStart: 8, OldLines: 1, NewStart: 8, NewLines: 1,
					Lines: []string{"@@ -8,1 +8,1 @@", "-\treturn out", "+\treturn out[:len(out)]"},
				},
			},
		},
	}

	fetcher := &mockFetcher{blobs: map[string][]byte{"proc.go": src}}
	snippets := BuildSnippets(files, fetcher)

	if len(snippets) != 1 {
		t.Fatalf("expected 1 merged snippet for Process, got %d: %v", len(snippets), symbolPaths(snippets))
	}
	if snippets[0].SymbolPath != "Process" {
		t.Errorf("SymbolPath = %q, want Process", snippets[0].SymbolPath)
	}
	joined := strings.Join(snippets[0].DiffLines, "\n")
	if !strings.Contains(joined, "make([]string") {
		t.Error("merged snippet missing first hunk content")
	}
	if !strings.Contains(joined, "return out[:len(out)]") {
		t.Error("merged snippet missing second hunk content")
	}
}

// ---------------------------------------------------------------------------
// Fallback (no fetcher): one snippet per hunk, NodeType = "hunk"
// ---------------------------------------------------------------------------

func TestBuildSnippets_FallbackWithNoFetcher(t *testing.T) {
	files := []*diff.FileDiff{
		{
			Path:     "foo.go",
			OldPath:  "foo.go",
			Language: "go",
			Hunks: []diff.Hunk{
				{
					OldStart: 1, OldLines: 3, NewStart: 1, NewLines: 4,
					Lines: []string{"@@ -1,3 +1,4 @@", "+// new", " existing"},
				},
				{
					OldStart: 10, OldLines: 2, NewStart: 11, NewLines: 2,
					Lines: []string{"@@ -10,2 +11,2 @@", "-old", "+new"},
				},
			},
		},
	}
	snippets := BuildSnippets(files, nil)
	if len(snippets) != 2 {
		t.Fatalf("fallback: expected 2 snippets (one per hunk), got %d", len(snippets))
	}
	for _, s := range snippets {
		if s.NodeType != "hunk" {
			t.Errorf("fallback NodeType = %q, want hunk", s.NodeType)
		}
	}
}

// ---------------------------------------------------------------------------
// Preamble: hunk outside any entity → empty SymbolPath snippet
// ---------------------------------------------------------------------------

func TestBuildSnippets_PreambleGrouping(t *testing.T) {
	src := []byte(`package foo

func Foo() {}
`)

	files := []*diff.FileDiff{
		{
			Path:     "p.go",
			OldPath:  "p.go",
			Language: "go",
			Hunks: []diff.Hunk{
				{
					OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1,
					Lines: []string{"@@ -1,1 +1,1 @@", "-package bar", "+package foo"},
				},
			},
		},
	}

	fetcher := &mockFetcher{blobs: map[string][]byte{"p.go": src}}
	snippets := BuildSnippets(files, fetcher)

	if len(snippets) != 1 {
		t.Fatalf("expected 1 preamble snippet, got %d", len(snippets))
	}
	if snippets[0].SymbolPath != "" {
		t.Errorf("preamble SymbolPath = %q, want empty", snippets[0].SymbolPath)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func symbolPaths(snippets []*Snippet) []string {
	out := make([]string, len(snippets))
	for i, s := range snippets {
		out[i] = s.SymbolPath
	}
	return out
}
