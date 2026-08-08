package core

import (
	"strings"
	"testing"
)

func TestSplitHunk_NewFileWithTwoFunctions(t *testing.T) {
	// A new file arrives as one hunk spanning every entity; it must split
	// into one snippet per function.
	src := `package api

func handle(cfg int) {
	parse(cfg)
}

func parse(cfg int) {}
`
	files := []*FileDiff{{
		Path: "api/handler.go", OldPath: "api/handler.go",
		Language: "go", IsNew: true,
		Source: []byte(src),
		Hunks: []Hunk{{
			OldStart: 0, OldLines: 0, NewStart: 1, NewLines: 7,
			Lines: []string{
				"@@ -0,0 +1,7 @@",
				"+package api",
				"+",
				"+func handle(cfg int) {",
				"+\tparse(cfg)",
				"+}",
				"+",
				"+func parse(cfg int) {}",
			},
		}},
	}}

	snippets := BuildSnippets(files)
	bySymbol := make(map[string]*Snippet)
	for _, s := range snippets {
		bySymbol[s.SymbolPath] = s
	}
	if _, ok := bySymbol["handle"]; !ok {
		t.Errorf("no snippet for handle (got %d snippets)", len(snippets))
	}
	if _, ok := bySymbol["parse"]; !ok {
		t.Errorf("no snippet for parse — hunk not split at entity boundary")
	}
	// The split must produce a call edge handle → parse.
	edges := BuildCallEdges(snippets)
	found := false
	for _, e := range edges {
		if e.From == bySymbol["handle"].ID && e.To == bySymbol["parse"].ID {
			found = true
		}
	}
	if !found {
		t.Error("expected call edge handle → parse after split")
	}
}

func TestSplitHunk_StraddlingContextOverlap(t *testing.T) {
	// Two adjacent small functions changed together: unified-context merges
	// them into one hunk; the split must keep the entities separate and
	// each sub-hunk must be a coherent patch fragment.
	src := `package main

func main() {
	helper()
	println("new")
}

func helper() {
	println("h")
}
`
	files := []*FileDiff{{
		Path: "main.go", OldPath: "main.go", Language: "go",
		Source: []byte(src),
		Hunks: []Hunk{{
			OldStart: 1, OldLines: 8, NewStart: 1, NewLines: 10,
			Lines: []string{
				"@@ -1,8 +1,10 @@",
				" package main",
				" ",
				" func main() {",
				" \thelper()",
				"+\tprintln(\"new\")",
				" }",
				" ",
				" func helper() {",
				"+\tprintln(\"h\")",
				" }",
			},
		}},
	}}

	snippets := BuildSnippets(files)
	bySymbol := make(map[string]*Snippet)
	for _, s := range snippets {
		bySymbol[s.SymbolPath] = s
	}

	m, ok := bySymbol["main"]
	if !ok {
		t.Fatal("no snippet for main")
	}
	h, ok := bySymbol["helper"]
	if !ok {
		t.Fatal("no snippet for helper — straddling hunk not split")
	}
	if strings.Contains(strings.Join(m.DiffLines, "\n"), `println("h")`) {
		t.Error("main's snippet contains helper's added line")
	}
	if !strings.Contains(strings.Join(h.DiffLines, "\n"), `println("h")`) {
		t.Error("helper's snippet is missing its added line")
	}
	// Sub-hunk headers must be regenerated with consistent counts.
	head := h.DiffLines[0]
	if !strings.HasPrefix(head, "@@ -") {
		t.Errorf("helper snippet does not start with a hunk header: %q", head)
	}
}

func TestSplitHunk_DeletionAtBoundaryStaysWithReplacement(t *testing.T) {
	// A '-' line following a method's trailing blank line must land in the
	// same snippet as its '+' replacement (the class container) — otherwise
	// accepting only the method stages a deletion the reviewer never saw.
	src := `class Store:
    version = 2

    def put(self, x):
        self.items.append(x)
        self.log(x)

    limit = 20
`
	files := []*FileDiff{{
		Path: "store.py", OldPath: "store.py", Language: "python",
		Source: []byte(src),
		Hunks: []Hunk{{
			OldStart: 1, OldLines: 7, NewStart: 1, NewLines: 8,
			Lines: []string{
				"@@ -1,7 +1,8 @@",
				" class Store:",
				"-    version = 1",
				"+    version = 2",
				" ",
				"     def put(self, x):",
				"         self.items.append(x)",
				"+        self.log(x)",
				" ",
				"-    limit = 10",
				"+    limit = 20",
			},
		}},
	}}

	snippets := BuildSnippets(files)
	bySymbol := make(map[string]*Snippet)
	for _, s := range snippets {
		bySymbol[s.SymbolPath] = s
	}

	put, ok := bySymbol["Store.put"]
	if !ok {
		t.Fatalf("no snippet for Store.put (got %v)", symbolPaths(snippets))
	}
	class, ok := bySymbol["Store"]
	if !ok {
		t.Fatalf("no snippet for Store (got %v)", symbolPaths(snippets))
	}
	if strings.Contains(strings.Join(put.DiffLines, "\n"), "limit") {
		t.Errorf("Store.put snippet contains the class-level limit change:\n%s", strings.Join(put.DiffLines, "\n"))
	}
	joined := strings.Join(class.DiffLines, "\n")
	if !strings.Contains(joined, "-    limit = 10") || !strings.Contains(joined, "+    limit = 20") {
		t.Errorf("Store snippet missing paired limit change:\n%s", joined)
	}
}

func TestSplitHunk_SingleEntityUntouched(t *testing.T) {
	// A hunk inside one entity must come back unchanged (no resplit noise).
	h := Hunk{
		OldStart: 3, OldLines: 3, NewStart: 3, NewLines: 3,
		Lines: []string{"@@ -3,3 +3,3 @@", " func a() {", "-\tx()", "+\ty()"},
	}
	entities := []Entity{{Name: "a", Kind: "function", StartLine: 3, EndLine: 5}}
	got := splitHunkAtEntities(h, entities)
	if len(got) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(got))
	}
	if got[0].Lines[0] != h.Lines[0] {
		t.Errorf("hunk modified: %v", got[0].Lines)
	}
}
