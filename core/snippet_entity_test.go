package core

import (
	"strings"
	"testing"
)

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

	files := []*FileDiff{
		{
			Path:     "foo.go",
			OldPath:  "foo.go",
			Language: "go",
			Hunks: []Hunk{
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

	files[0].Source = src
	snippets := BuildSnippets(files)

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

	files := []*FileDiff{
		{
			Path:     "proc.go",
			OldPath:  "proc.go",
			Language: "go",
			Hunks: []Hunk{
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

	files[0].Source = src
	snippets := BuildSnippets(files)

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
// Fallback (no source): one snippet per hunk, NodeType = "hunk"
// ---------------------------------------------------------------------------

func TestBuildSnippets_FallbackWithNoFetcher(t *testing.T) {
	files := []*FileDiff{
		{
			Path:     "foo.go",
			OldPath:  "foo.go",
			Language: "go",
			Hunks: []Hunk{
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
	snippets := BuildSnippets(files)
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

	files := []*FileDiff{
		{
			Path:     "p.go",
			OldPath:  "p.go",
			Language: "go",
			Hunks: []Hunk{
				{
					OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1,
					Lines: []string{"@@ -1,1 +1,1 @@", "-package bar", "+package foo"},
				},
			},
		},
	}

	files[0].Source = src
	snippets := BuildSnippets(files)

	if len(snippets) != 1 {
		t.Fatalf("expected 1 preamble snippet, got %d", len(snippets))
	}
	if snippets[0].SymbolPath != "" {
		t.Errorf("preamble SymbolPath = %q, want empty", snippets[0].SymbolPath)
	}
}

// ---------------------------------------------------------------------------
// Blank-only preambles are dropped; real preambles and entity snippets survive
// ---------------------------------------------------------------------------

func TestBuildSnippets_BlankPreambleDropped(t *testing.T) {
	src := []byte(`package foo


func Foo() {}
`)

	files := []*FileDiff{
		{
			Path:     "p.go",
			OldPath:  "p.go",
			Language: "go",
			Hunks: []Hunk{
				{
					OldStart: 1, OldLines: 0, NewStart: 2, NewLines: 2,
					Lines: []string{"@@ -1,0 +2,2 @@", "+", "+"},
				},
			},
		},
	}

	files[0].Source = src
	snippets := BuildSnippets(files)

	if len(snippets) != 0 {
		t.Fatalf("expected blank-only preamble to be dropped, got %d snippets: %v", len(snippets), symbolPaths(snippets))
	}
}

func TestBuildSnippets_SubstantivePreambleSurvives(t *testing.T) {
	src := []byte(`// Package foo does things.
package foo

func Foo() {}
`)

	files := []*FileDiff{
		{
			Path:     "p.go",
			OldPath:  "p.go",
			Language: "go",
			Hunks: []Hunk{
				{
					OldStart: 0, OldLines: 0, NewStart: 1, NewLines: 1,
					Lines: []string{"@@ -0,0 +1,1 @@", "+// Package foo does things."},
				},
			},
		},
	}

	files[0].Source = src
	snippets := BuildSnippets(files)

	if len(snippets) != 1 {
		t.Fatalf("expected 1 preamble snippet, got %d", len(snippets))
	}
	if snippets[0].SymbolPath != "" {
		t.Errorf("preamble SymbolPath = %q, want empty", snippets[0].SymbolPath)
	}
}

func TestBuildSnippets_BlankChangeInEntitySurvives(t *testing.T) {
	src := []byte(`package foo

func Foo() {

	x()
}
`)

	files := []*FileDiff{
		{
			Path:     "p.go",
			OldPath:  "p.go",
			Language: "go",
			Hunks: []Hunk{
				{
					OldStart: 3, OldLines: 0, NewStart: 4, NewLines: 1,
					Lines: []string{"@@ -3,0 +4,1 @@", "+"},
				},
			},
		},
	}

	files[0].Source = src
	snippets := BuildSnippets(files)

	if len(snippets) != 1 {
		t.Fatalf("expected entity snippet with blank-only change to survive, got %d", len(snippets))
	}
	if snippets[0].SymbolPath != "Foo" {
		t.Errorf("SymbolPath = %q, want Foo", snippets[0].SymbolPath)
	}
}

func TestBuildSnippets_ContextOnlyEntityDropped(t *testing.T) {
	// Hunk context spilling from a change in callee into the tail of caller
	// must not produce a caller snippet: caller itself did not change.
	src := []byte(`package foo

func caller() {
	callee()
}

func callee() {
	println("b")
}
`)

	files := []*FileDiff{
		{
			Path:     "p.go",
			OldPath:  "p.go",
			Language: "go",
			Hunks: []Hunk{
				{
					OldStart: 5, OldLines: 5, NewStart: 5, NewLines: 5,
					Lines: []string{
						"@@ -5,5 +5,5 @@",
						" }",
						" ",
						" func callee() {",
						"-\tprintln(\"a\")",
						"+\tprintln(\"b\")",
						" }",
					},
				},
			},
		},
	}

	files[0].Source = src
	snippets := BuildSnippets(files)

	if len(snippets) != 1 {
		t.Fatalf("expected only the callee snippet, got %d: %v", len(snippets), symbolPaths(snippets))
	}
	if snippets[0].SymbolPath != "callee" {
		t.Errorf("SymbolPath = %q, want callee", snippets[0].SymbolPath)
	}
}

// ---------------------------------------------------------------------------
// Rust: struct-level and impl-level hunks must not merge into one snippet
// ---------------------------------------------------------------------------

func TestBuildSnippets_RustStructAndImplDistinct(t *testing.T) {
	src := []byte(`struct Foo {
    x: i32,
}

impl Foo {
    const LIMIT: i32 = 20;

    fn get(&self) -> i32 {
        self.x
    }
}
`)

	files := []*FileDiff{
		{
			Path:     "foo.rs",
			OldPath:  "foo.rs",
			Language: "rust",
			Hunks: []Hunk{
				{
					OldStart: 2, OldLines: 1, NewStart: 2, NewLines: 1,
					Lines: []string{"@@ -2,1 +2,1 @@", "-    x: i64,", "+    x: i32,"},
				},
				{
					OldStart: 6, OldLines: 1, NewStart: 6, NewLines: 1,
					Lines: []string{"@@ -6,1 +6,1 @@", "-    const LIMIT: i32 = 10;", "+    const LIMIT: i32 = 20;"},
				},
			},
		},
	}

	files[0].Source = src
	snippets := BuildSnippets(files)

	if len(snippets) != 2 {
		t.Fatalf("expected 2 snippets (struct + impl), got %d: %v", len(snippets), symbolPaths(snippets))
	}
	bySymbol := make(map[string]*Snippet)
	for _, s := range snippets {
		bySymbol[s.SymbolPath] = s
	}
	structSnip, ok := bySymbol["Foo"]
	if !ok {
		t.Fatalf("no snippet for struct Foo (got %v)", symbolPaths(snippets))
	}
	if strings.Contains(strings.Join(structSnip.DiffLines, "\n"), "LIMIT") {
		t.Errorf("impl-level LIMIT change merged into struct snippet:\n%s", strings.Join(structSnip.DiffLines, "\n"))
	}
	if structSnip.EndLine >= 5 {
		t.Errorf("struct snippet span = [%d, %d], want to end before impl", structSnip.StartLine, structSnip.EndLine)
	}
	// The impl-level change must live in a snippet whose span covers line 6.
	found := false
	for _, s := range snippets {
		if s.StartLine <= 6 && s.EndLine >= 6 && strings.Contains(strings.Join(s.DiffLines, "\n"), "LIMIT") {
			found = true
		}
	}
	if !found {
		t.Errorf("no snippet covers the impl-level LIMIT change (got %v)", symbolPaths(snippets))
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
