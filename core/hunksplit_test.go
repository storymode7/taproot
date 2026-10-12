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
	got, _ := splitHunkAtEntities(h, entities, nil)
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

// stagingCase is one file's diff, as git prints it, and the snippets it
// must split into.
type stagingCase struct {
	name, path, lang, oldText, newText string
	hunk                               []string // the hunk, header first
	symbols                            []string // snippets expected, in order
	alone                              map[string]string
}

func runStagingCase(t *testing.T, tc stagingCase) {
	t.Helper()
	frags, err := fragments(tc.hunk)
	if err != nil || len(frags) != 1 {
		t.Fatalf("bad test hunk: %v", err)
	}
	f := frags[0]
	files := []*FileDiff{{Path: tc.path, OldPath: tc.path, Language: tc.lang, Source: []byte(tc.newText),
		Hunks: []Hunk{{OldStart: f.oldStart, OldLines: f.oldCount, NewStart: f.newStart, NewLines: f.newCount, Lines: tc.hunk}}}}
	snippets := BuildSnippets(files)
	if got := symbolPaths(snippets); strings.Join(got, ",") != strings.Join(tc.symbols, ",") {
		t.Fatalf("snippets %q, want %q", got, tc.symbols)
	}
	checkStaging(t, tc.oldText, tc.newText, snippets, tc.alone)
}

func TestSplitHunk_BlankLinesBelongToTheCodeTheyTouch(t *testing.T) {
	cases := []stagingCase{{
		// The blank after the new function leads into unchanged c(); it
		// must go with d(), not turn c() into a blank-only snippet.
		name: "function inserted between two", path: "ins.go", lang: "go",
		oldText: "package p\n\nfunc a() {\n}\n\nfunc c() {\n}\n",
		newText: "package p\n\nfunc a() {\n}\n\nfunc d() {\n}\n\nfunc c() {\n}\n",
		hunk:    []string{"@@ -3,5 +3,8 @@ package p", " func a() {", " }", " ", "+func d() {", "+}", "+", " func c() {", " }"},
		symbols: []string{"d"},
	}, {
		name: "python function appended", path: "app.py", lang: "python",
		oldText: "def f():\n    return 1\n",
		newText: "def f():\n    return 1\n\n\ndef g():\n    return 2\n",
		hunk:    []string{"@@ -1,2 +1,6 @@", " def f():", "     return 1", "+", "+", "+def g():", "+    return 2"},
		symbols: []string{"g"},
	}, {
		name: "blank line added between two", path: "addblank.go", lang: "go",
		oldText: "package p\n\nfunc a() {\n}\n\nfunc c() {\n}\n",
		newText: "package p\n\nfunc a() {\n}\n\n\nfunc c() {\n}\n",
		hunk:    []string{"@@ -3,5 +3,6 @@ package p", " func a() {", " }", " ", "+", " func c() {", " }"},
		symbols: []string{"c"},
	}, {
		name: "blank line removed between two", path: "rmblank.go", lang: "go",
		oldText: "package p\n\nfunc a() {\n}\n\n\nfunc c() {\n}\n",
		newText: "package p\n\nfunc a() {\n}\n\nfunc c() {\n}\n",
		hunk:    []string{"@@ -3,6 +3,5 @@ package p", " func a() {", " }", " ", "-", " func c() {", " }"},
		symbols: []string{"c"},
	}, {
		name: "blank line added at the end", path: "trail.go", lang: "go",
		oldText: "package p\n\nfunc a() {\n}\n",
		newText: "package p\n\nfunc a() {\n}\n\n",
		hunk:    []string{"@@ -2,3 +2,4 @@ package p", " ", " func a() {", " }", "+"},
		symbols: []string{"a"},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { runStagingCase(t, tc) })
	}
}

func TestSplitHunk_EachEntityKeepsItsOwnBoundaryLine(t *testing.T) {
	// git may pair an unchanged line with an equal line of a neighbouring
	// new entity. Staging either entity alone must not move that line.
	cases := []stagingCase{{
		name: "go closing brace", path: "a.go", lang: "go",
		oldText: "package main\n\nfunc alpha() int {\n\treturn x + y\n}\n\nfunc gamma() int {\n\treturn 3\n}\n",
		newText: "package main\n\nfunc alpha() int {\n\treturn x * y\n}\n\nfunc helper() int {\n\treturn 42\n}\n\nfunc gamma() int {\n\treturn 3\n}\n",
		hunk: []string{"@@ -1,7 +1,11 @@", " package main", " ", " func alpha() int {", "-\treturn x + y",
			"+\treturn x * y", "+}", "+", "+func helper() int {", "+\treturn 42", " }", " ", " func gamma() int {"},
		symbols: []string{"alpha", "helper"},
		alone: map[string]string{
			"alpha":  "package main\n\nfunc alpha() int {\n\treturn x * y\n}\n\nfunc gamma() int {\n\treturn 3\n}\n",
			"helper": "package main\n\nfunc alpha() int {\n\treturn x + y\n}\n\nfunc helper() int {\n\treturn 42\n}\n\nfunc gamma() int {\n\treturn 3\n}\n",
		},
	}, {
		// Staging g alone used to move f's return into g: f returned None
		// and the file still compiled.
		name: "python last statement", path: "f.py", lang: "python",
		oldText: "def f():\n    result = compute()\n    return result\n",
		newText: "def f():\n    result = compute2()\n    return result\n\n\ndef g():\n    result = other()\n    return result\n",
		hunk: []string{"@@ -1,3 +1,8 @@", " def f():", "-    result = compute()", "+    result = compute2()",
			"+    return result", "+", "+", "+def g():", "+    result = other()", "     return result"},
		symbols: []string{"f", "g"},
		alone: map[string]string{
			"f": "def f():\n    result = compute2()\n    return result\n",
			"g": "def f():\n    result = compute()\n    return result\n\n\ndef g():\n    result = other()\n    return result\n",
		},
	}, {
		name: "python decorator", path: "d.py", lang: "python",
		oldText: "@cache\ndef a():\n    return 1\n",
		newText: "@cache\ndef z():\n    return 0\n\n\n@cache\ndef a():\n    return 1\n",
		hunk: []string{"@@ -1,3 +1,8 @@", " @cache", "+def z():", "+    return 0", "+", "+", "+@cache",
			" def a():", "     return 1"},
		symbols: []string{"z"},
	}, {
		name: "java annotation", path: "T.java", lang: "java",
		oldText: "class T {\n    @Test\n    void a() {}\n}\n",
		newText: "class T {\n    @Test\n    void z() {}\n\n    @Test\n    void a() {}\n}\n",
		hunk: []string{"@@ -1,4 +1,7 @@", " class T {", "     @Test", "+    void z() {}", "+", "+    @Test",
			"     void a() {}", " }"},
		symbols: []string{"T.z"},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { runStagingCase(t, tc) })
	}
}

func TestSplitHunk_EachEntityGetsItsOwnOldLinesBack(t *testing.T) {
	// git pairs unchanged lines with any equal line, so an old function's
	// tail can end up paired with a new function's tail. Each case is git's
	// own output; staging any one entity alone must leave the others whole.
	cases := []stagingCase{{
		name: "both functions end in return nil", path: "a.go", lang: "go",
		oldText: "package p\n\nfunc a() error {\n\tx()\n\treturn nil\n}\n\nfunc c() error {\n\treturn nil\n}\n",
		newText: "package p\n\nfunc a() error {\n\ty()\n\treturn nil\n}\n\nfunc b() error {\n\tz()\n\treturn nil\n}\n\nfunc c() error {\n\treturn nil\n}\n",
		hunk: []string{"@@ -1,7 +1,12 @@", " package p", " ", " func a() error {", "-\tx()", "+\ty()", "+\treturn nil", "+}", "+",
			"+func b() error {", "+\tz()", " \treturn nil", " }", " "},
		symbols: []string{"a", "b"},
		alone: map[string]string{
			"a": "package p\n\nfunc a() error {\n\ty()\n\treturn nil\n}\n\nfunc c() error {\n\treturn nil\n}\n",
			"b": "package p\n\nfunc a() error {\n\tx()\n\treturn nil\n}\n\nfunc b() error {\n\tz()\n\treturn nil\n}\n\nfunc c() error {\n\treturn nil\n}\n",
		},
	}, {
		name: "last statement changed above a function ending in it", path: "b.go", lang: "go",
		oldText: "package p\n\nfunc f3() int {\n\tv := 1\n\treturn v\n}\n",
		newText: "package p\n\nfunc f3() int {\n\tv := 1\n\treturn v + 4\n}\n\nfunc n4() int {\n\tv := 8\n\treturn v\n}\n",
		hunk: []string{"@@ -2,5 +2,10 @@ package p", " ", " func f3() int {", " \tv := 1", "+\treturn v + 4", "+}", "+",
			"+func n4() int {", "+\tv := 8", " \treturn v", " }"},
		symbols: []string{"f3", "n4"},
		alone: map[string]string{
			"f3": "package p\n\nfunc f3() int {\n\tv := 1\n\treturn v + 4\n}\n",
			"n4": "package p\n\nfunc f3() int {\n\tv := 1\n\treturn v\n}\n\nfunc n4() int {\n\tv := 8\n\treturn v\n}\n",
		},
	}, {
		name: "appended to a file that keeps no final newline", path: "e.go", lang: "go",
		oldText: "package p\n\nfunc a() int {\n\tv := 1\n\treturn v\n}",
		newText: "package p\n\nfunc a() int {\n\tv := 1\n\treturn v\n}\n\nfunc b() int {\n\tv := 2\n\treturn v\n}",
		hunk: []string{"@@ -3,4 +3,9 @@ package p", " func a() int {", " \tv := 1", " \treturn v", "+}", "+", "+func b() int {",
			"+\tv := 2", "+\treturn v", " }", `\ No newline at end of file`},
		symbols: []string{"b"},
	}, {
		name: "renamed below a new function", path: "h.js", lang: "javascript",
		oldText: "\"use strict\";\n\nfunction f1() {\n  const v = 0;\n  return v;\n}\n",
		newText: "\"use strict\";\n\n\nfunction n2() {\n  const v = 2;\n  return v;\n}\n\n\nfunction r3() {\n  const v = 0;\n  return v;\n}\n",
		hunk: []string{"@@ -1,6 +1,13 @@", " \"use strict\";", " ", "-function f1() {", "+", "+function n2() {", "+  const v = 2;",
			"+  return v;", "+}", "+", "+", "+function r3() {", "   const v = 0;", "   return v;", " }"},
		symbols: []string{"n2", "r3"},
		alone: map[string]string{
			"n2": "\"use strict\";\n\n\nfunction n2() {\n  const v = 2;\n  return v;\n}\nfunction f1() {\n  const v = 0;\n  return v;\n}\n",
			"r3": "\"use strict\";\n\n\n\nfunction r3() {\n  const v = 0;\n  return v;\n}\n",
		},
	}, {
		name: "body rewritten above a function sharing a line", path: "i.go", lang: "go",
		oldText: "package p\n\nfunc f1() int {\n\tv := 2\n\treturn v\n}\n",
		newText: "package p\n\nfunc f1() int {\n\tv := 48\n\treturn v + 7\n}\n\n\nfunc n2() int {\n\tv := 1\n\tv := 2\n\treturn v + 3\n}\n",
		hunk: []string{"@@ -1,6 +1,13 @@", " package p", " ", " func f1() int {", "+\tv := 48", "+\treturn v + 7", "+}", "+", "+",
			"+func n2() int {", "+\tv := 1", " \tv := 2", "-\treturn v", "+\treturn v + 3", " }"},
		symbols: []string{"f1", "n2"},
		alone: map[string]string{
			"f1": "package p\n\nfunc f1() int {\n\tv := 48\n\treturn v + 7\n}\n",
			"n2": "package p\n\nfunc f1() int {\n\tv := 2\n\treturn v\n}\n\n\nfunc n2() int {\n\tv := 1\n\tv := 2\n\treturn v + 3\n}\n",
		},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { runStagingCase(t, tc) })
	}
}
