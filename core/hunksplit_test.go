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

// noEOLCase is a diff of a file whose old or new text ends without a
// newline, as git prints it.
type noEOLCase struct {
	name, path, lang, oldText, newText string
	hunk                               Hunk
	symbols                            []string          // snippets expected, in order
	alone                              map[string]string // symbol → old text with only that snippet applied
}

func runNoEOLCase(t *testing.T, tc noEOLCase) {
	t.Helper()
	files := []*FileDiff{{Path: tc.path, OldPath: tc.path, Language: tc.lang,
		Source: []byte(tc.newText), Hunks: []Hunk{tc.hunk}}}
	snippets := BuildSnippets(files)
	if got := symbolPaths(snippets); strings.Join(got, ",") != strings.Join(tc.symbols, ",") {
		t.Fatalf("snippets %q, want %q", got, tc.symbols)
	}
	checkStaging(t, tc.oldText, tc.newText, snippets, tc.alone)
}

func TestSplitHunk_NoNewlineMarkers(t *testing.T) {
	cases := []noEOLCase{{
		name: "last line edited, no newline on either side", path: "e.py", lang: "python",
		oldText: "def a():\n    return 1\n\n\ndef b():\n    return 2",
		newText: "def a():\n    return 1\n\n\ndef b():\n    return 20",
		hunk: Hunk{OldStart: 4, OldLines: 3, NewStart: 4, NewLines: 3, Lines: []string{
			"@@ -4,3 +4,3 @@", " ", " def b():", "-    return 2", `\ No newline at end of file`,
			"+    return 20", `\ No newline at end of file`}},
		symbols: []string{"b"},
	}, {
		name: "marker after trailing context", path: "m.js", lang: "javascript",
		oldText: "function a() {\n  return 1;\n}\n\nfunction b() {\n  return 1;\n}",
		newText: "function a() {\n  return 1;\n}\n\nfunction b() {\n  return 2;\n}",
		hunk: Hunk{OldStart: 4, OldLines: 4, NewStart: 4, NewLines: 4, Lines: []string{
			"@@ -4,4 +4,4 @@", " ", " function b() {", "-  return 1;", "+  return 2;", " }",
			`\ No newline at end of file`}},
		symbols: []string{"b"},
	}, {
		name: "only the new side drops the newline", path: "p.go", lang: "go",
		oldText: "package p\n\nfunc a() {\n\tx()\n}\n",
		newText: "package p\n\nfunc a() {\n\ty()\n}",
		hunk: Hunk{OldStart: 3, OldLines: 3, NewStart: 3, NewLines: 3, Lines: []string{
			"@@ -3,3 +3,3 @@", " func a() {", "-\tx()", "-}", "+\ty()", "+}",
			`\ No newline at end of file`}},
		symbols: []string{"a"},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { runNoEOLCase(t, tc) })
	}
}

func TestSplitHunk_AppendAfterLastLineWithoutNewline(t *testing.T) {
	// A function appended to a file that ends without a newline needs that
	// newline. The "-last / \ No newline / +last" pair must travel with
	// the appended function, or staging it alone glues it to the old last
	// line and every later y appends another copy.
	cases := []noEOLCase{{
		name: "python", path: "m.py", lang: "python",
		oldText: "def a():\n    return 1",
		newText: "def a():\n    return 1\n\n\ndef f():\n    return 2",
		hunk: Hunk{OldStart: 1, OldLines: 2, NewStart: 1, NewLines: 6, Lines: []string{
			"@@ -1,2 +1,6 @@", " def a():", "-    return 1", `\ No newline at end of file`,
			"+    return 1", "+", "+", "+def f():", "+    return 2", `\ No newline at end of file`}},
		symbols: []string{"f"},
	}, {
		name: "go", path: "p.go", lang: "go",
		oldText: "package p\n\nfunc a() {\n\tx()\n}",
		newText: "package p\n\nfunc a() {\n\tx()\n}\n\nfunc b() {\n\ty()\n}\n",
		hunk: Hunk{OldStart: 3, OldLines: 3, NewStart: 3, NewLines: 7, Lines: []string{
			"@@ -3,3 +3,7 @@", " func a() {", " \tx()", "-}", `\ No newline at end of file`,
			"+}", "+", "+func b() {", "+\ty()", "+}"}},
		symbols: []string{"b"},
	}, {
		// The last function also changed: its edit stays its own, the
		// re-added closing line goes with the appended function.
		name: "js, last function edited", path: "m.js", lang: "javascript",
		oldText: "function a() {\n  return 1\n}",
		newText: "function a() {\n  return 2\n}\n\nfunction f() {\n  return 3\n}\n",
		hunk: Hunk{OldStart: 1, OldLines: 3, NewStart: 1, NewLines: 7, Lines: []string{
			"@@ -1,3 +1,7 @@", " function a() {", "-  return 1", "-}", `\ No newline at end of file`,
			"+  return 2", "+}", "+", "+function f() {", "+  return 3", "+}"}},
		symbols: []string{"a", "f"},
		alone: map[string]string{
			"a": "function a() {\n  return 2\n}",
			"f": "function a() {\n  return 1\n}\n\nfunction f() {\n  return 3\n}\n",
		},
	}, {
		// The old last line itself changed, so nothing can be appended
		// without that change: everything after it stays in one snippet.
		name: "python, last line edited", path: "m.py", lang: "python",
		oldText: "def a():\n    return 1",
		newText: "def a():\n    return 10\n\n\ndef f():\n    return 2\n",
		hunk: Hunk{OldStart: 1, OldLines: 2, NewStart: 1, NewLines: 6, Lines: []string{
			"@@ -1,2 +1,6 @@", " def a():", "-    return 1", `\ No newline at end of file`,
			"+    return 10", "+", "+", "+def f():", "+    return 2"}},
		symbols: []string{"a"},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { runNoEOLCase(t, tc) })
	}
}
