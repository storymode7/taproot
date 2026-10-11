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
// Blank lines join the entity they lead into; real preambles survive
// ---------------------------------------------------------------------------

func TestBuildSnippets_BlankLinesJoinTheEntityTheyLeadInto(t *testing.T) {
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

	if len(snippets) != 1 || snippets[0].SymbolPath != "Foo" {
		t.Fatalf("expected the blank lines in Foo's snippet, got %v", symbolPaths(snippets))
	}
	if got := strings.Join(snippets[0].DiffLines, "|"); got != "@@ -1,0 +2,2 @@|+|+" {
		t.Errorf("DiffLines = %q", got)
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

func TestBuildSnippets_ContextSpillIntoSameNamedEntityDropped(t *testing.T) {
	// Two import blocks share the "imports" key. Context from a change in
	// the first block spilling into the second must not add a context-only
	// fragment to the merged snippet: git apply rejects a hunk with no
	// added or removed lines as corrupt.
	src := []byte(`import os
import sys
from app.errors import RequestError

from app.client import Client


def f():
    pass
`)

	files := []*FileDiff{
		{
			Path:     "p.py",
			OldPath:  "p.py",
			Language: "python",
			Hunks: []Hunk{
				{
					OldStart: 1, OldLines: 5, NewStart: 1, NewLines: 6,
					Lines: []string{
						"@@ -1,5 +1,6 @@",
						" import os",
						" import sys",
						"+from app.errors import RequestError",
						" ",
						" from app.client import Client",
						" ",
					},
				},
			},
		},
	}

	files[0].Source = src
	snippets := BuildSnippets(files)

	if len(snippets) != 1 || snippets[0].NodeType != "imports" {
		t.Fatalf("expected one imports snippet, got %v", symbolPaths(snippets))
	}
	want := []string{
		"@@ -1,2 +1,3 @@",
		" import os",
		" import sys",
		"+from app.errors import RequestError",
	}
	if got := snippets[0].DiffLines; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("DiffLines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
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

func TestBuildSnippets_SameNameDifferentKindsGetDistinctIDs(t *testing.T) {
	// The zod pattern: a schema const and its inferred type share a name.
	// One ID for both would make one accept mark both.
	src := []byte(`import { z } from "zod";

export const User = z.object({
  name: z.string(),
  email: z.string().email(),
});

export type User = z.infer<typeof User> & { createdAt: Date };
`)
	files := []*FileDiff{{
		Path: "user.ts", OldPath: "user.ts", Language: "typescript", Source: src,
		Hunks: []Hunk{{
			OldStart: 1, OldLines: 7, NewStart: 1, NewLines: 8,
			Lines: []string{
				"@@ -1,7 +1,8 @@",
				` import { z } from "zod";`,
				" ",
				" export const User = z.object({",
				"   name: z.string(),",
				"+  email: z.string().email(),",
				" });",
				" ",
				"-export type User = z.infer<typeof User>;",
				"+export type User = z.infer<typeof User> & { createdAt: Date };",
			},
		}},
	}}

	snippets := BuildSnippets(files)
	if len(snippets) != 2 {
		t.Fatalf("expected the const and the type as 2 snippets, got %v", symbolPaths(snippets))
	}
	if snippets[0].ID == snippets[1].ID {
		t.Errorf("%s %q and %s %q share ID %s", snippets[0].NodeType, snippets[0].SymbolPath,
			snippets[1].NodeType, snippets[1].SymbolPath, snippets[0].ID)
	}
	if f := BuildForest(snippets, BuildCallEdges(snippets)); len(f.ByID) != 2 {
		t.Errorf("forest has %d nodes, want 2", len(f.ByID))
	}
}

// ---------------------------------------------------------------------------
// Same-named entities in one file stay separate snippets
// ---------------------------------------------------------------------------

// snippetsNamed returns the snippets with the given symbol path, in order.
func snippetsNamed(snippets []*Snippet, symbol string) []*Snippet {
	var out []*Snippet
	for _, s := range snippets {
		if s.SymbolPath == symbol {
			out = append(out, s)
		}
	}
	return out
}

func TestBuildSnippets_SameNamedGoMethodsStaySeparate(t *testing.T) {
	src := `package shapes

type Circle struct{ R float64 }

func (c Circle) String() string {
	return "circle"
}

type Square struct{ Side float64 }

func (s Square) String() string {
	return describe(s.Side)
}

func describe(x float64) string { return "" }
`
	files := []*FileDiff{{
		Path: "shapes.go", OldPath: "shapes.go", Language: "go", Source: []byte(src),
		Hunks: []Hunk{
			{OldStart: 6, OldLines: 1, NewStart: 6, NewLines: 1,
				Lines: []string{"@@ -6,1 +6,1 @@", `-	return "c"`, `+	return "circle"`}},
			{OldStart: 12, OldLines: 1, NewStart: 12, NewLines: 1,
				Lines: []string{"@@ -12,1 +12,1 @@", `-	return "s"`, `+	return describe(s.Side)`}},
			{OldStart: 15, OldLines: 0, NewStart: 15, NewLines: 1,
				Lines: []string{"@@ -14,0 +15,1 @@", `+func describe(x float64) string { return "" }`}},
		},
	}}

	snippets := BuildSnippets(files)
	str := snippetsNamed(snippets, "String")
	if len(str) != 2 {
		t.Fatalf("want 2 String snippets, got %d of %v", len(str), symbolPaths(snippets))
	}
	if str[0].StartLine != 5 || str[1].StartLine != 11 {
		t.Errorf("String spans start at %d and %d, want 5 and 11", str[0].StartLine, str[1].StartLine)
	}
	if strings.Contains(strings.Join(str[0].DiffLines, "\n"), "describe") {
		t.Errorf("Circle.String carries Square.String's change:\n%s", strings.Join(str[0].DiffLines, "\n"))
	}
	if str[0].ID == str[1].ID {
		t.Errorf("both String snippets have ID %s", str[0].ID)
	}
	if str[0].ID != entitySnippetID("shapes.go", "function", "String", 1) {
		t.Error("the first String must keep the plain entity ID so saved state survives")
	}
	if len(str[1].Body) == 0 || !strings.Contains(str[1].Body[0], "Square") {
		t.Errorf("second String's body is not Square.String's: %q", str[1].Body)
	}
	desc := snippetsNamed(snippets, "describe")
	if len(desc) != 1 {
		t.Fatalf("no describe snippet in %v", symbolPaths(snippets))
	}
	got := edgeSet(BuildCallEdges(snippets))
	if _, ok := got[str[1].ID+"->"+desc[0].ID]; !ok {
		t.Errorf("Square.String's call to describe made no edge: %v", got)
	}
	if f := BuildForest(snippets, BuildCallEdges(snippets)); len(f.ByID) != len(snippets) {
		t.Errorf("forest has %d nodes for %d snippets", len(f.ByID), len(snippets))
	}
}

func TestBuildSnippets_SameNamedMembersStaySeparate(t *testing.T) {
	cases := []struct {
		name, path, lang, symbol, src string
		hunks                         []Hunk
	}{
		{"java overloads", "Calc.java", "java", "Calc.add", `public class Calc {
    int add(int a, int b) {
        return a + b + 0;
    }

    double add(double a, double b) {
        return a + b + 0.0;
    }
}
`, []Hunk{
			{OldStart: 3, OldLines: 1, NewStart: 3, NewLines: 1,
				Lines: []string{"@@ -3,1 +3,1 @@", "-        return a + b;", "+        return a + b + 0;"}},
			{OldStart: 7, OldLines: 1, NewStart: 7, NewLines: 1,
				Lines: []string{"@@ -7,1 +7,1 @@", "-        return a + b;", "+        return a + b + 0.0;"}},
		}},
		{"python property pair", "acct.py", "python", "Account.balance", `class Account:
    @property
    def balance(self):
        return self._b + 0

    @balance.setter
    def balance(self, v):
        self._b = v + 0
`, []Hunk{
			{OldStart: 4, OldLines: 1, NewStart: 4, NewLines: 1,
				Lines: []string{"@@ -4,1 +4,1 @@", "-        return self._b", "+        return self._b + 0"}},
			{OldStart: 8, OldLines: 1, NewStart: 8, NewLines: 1,
				Lines: []string{"@@ -8,1 +8,1 @@", "-        self._b = v", "+        self._b = v + 0"}},
		}},
		{"rust trait impls", "p.rs", "rust", "P.fmt", `struct P;

impl fmt::Display for P {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        write!(f, "p!")
    }
}

impl fmt::Debug for P {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        write!(f, "P!")
    }
}
`, []Hunk{
			{OldStart: 5, OldLines: 1, NewStart: 5, NewLines: 1,
				Lines: []string{"@@ -5,1 +5,1 @@", `-        write!(f, "p")`, `+        write!(f, "p!")`}},
			{OldStart: 11, OldLines: 1, NewStart: 11, NewLines: 1,
				Lines: []string{"@@ -11,1 +11,1 @@", `-        write!(f, "P")`, `+        write!(f, "P!")`}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := []*FileDiff{{Path: tc.path, OldPath: tc.path, Language: tc.lang,
				Source: []byte(tc.src), Hunks: tc.hunks}}
			snippets := BuildSnippets(files)
			got := snippetsNamed(snippets, tc.symbol)
			if len(got) != 2 {
				t.Fatalf("want 2 %s snippets, got %d of %v", tc.symbol, len(got), symbolPaths(snippets))
			}
			if got[0].ID == got[1].ID {
				t.Errorf("both %s snippets have ID %s", tc.symbol, got[0].ID)
			}
			if got[0].StartLine == got[1].StartLine {
				t.Errorf("both %s snippets start at line %d", tc.symbol, got[0].StartLine)
			}
			for i, s := range got {
				if n := strings.Count(strings.Join(s.DiffLines, "\n"), "\n+"); n != 1 {
					t.Errorf("%s #%d holds %d added lines, want its own 1:\n%s", tc.symbol, i+1, n, strings.Join(s.DiffLines, "\n"))
				}
			}
		})
	}
}
