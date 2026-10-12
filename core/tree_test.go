package core

import (
	"fmt"
	"strings"
	"testing"
)

func fn(id, path, symbol string, startLine int) *Snippet {
	return &Snippet{
		ID: id, FilePath: path, Language: "go",
		SymbolPath: symbol, NodeType: "function",
		StartLine: startLine, EndLine: startLine + 2,
	}
}

func call(from, to string, line int) SymbolEdge {
	return SymbolEdge{From: from, To: to, Kind: EdgeCall, CallLine: line}
}

func rootIDs(f *ReviewForest) []string {
	ids := make([]string, len(f.Roots))
	for i, r := range f.Roots {
		ids[i] = r.Snippet.ID
	}
	return ids
}

func childIDs(n *ReviewNode) []string {
	ids := make([]string, len(n.Children))
	for i, c := range n.Children {
		ids[i] = c.Snippet.ID
	}
	return ids
}

func TestBuildForest_LinearChain(t *testing.T) {
	// main → handle → parse
	snippets := []*Snippet{
		fn("main", "cmd/main.go", "main", 1),
		fn("handle", "api.go", "handle", 1),
		fn("parse", "parse.go", "parse", 1),
	}
	edges := []SymbolEdge{call("main", "handle", 3), call("handle", "parse", 2)}

	f := BuildForest(snippets, edges)
	if got := rootIDs(f); len(got) != 1 || got[0] != "main" {
		t.Fatalf("roots = %v, want [main]", got)
	}
	mainNode := f.ByID["main"]
	if got := childIDs(mainNode); len(got) != 1 || got[0] != "handle" {
		t.Fatalf("main children = %v, want [handle]", got)
	}
	if got := childIDs(f.ByID["handle"]); len(got) != 1 || got[0] != "parse" {
		t.Fatalf("handle children = %v, want [parse]", got)
	}
	if f.ByID["parse"].Parent != f.ByID["handle"] {
		t.Error("parse.Parent should be handle")
	}
}

func TestBuildForest_ChildrenOrderedByCallLine(t *testing.T) {
	snippets := []*Snippet{
		fn("main", "main.go", "main", 1),
		fn("a", "a.go", "a", 1),
		fn("b", "b.go", "b", 1),
	}
	// b is called earlier in main's body than a.
	edges := []SymbolEdge{call("main", "a", 9), call("main", "b", 2)}

	f := BuildForest(snippets, edges)
	if got := childIDs(f.ByID["main"]); len(got) != 2 || got[0] != "b" || got[1] != "a" {
		t.Fatalf("children = %v, want [b a] (call-line order)", got)
	}
}

func TestBuildForest_DiamondMultiParent(t *testing.T) {
	//   main → a → shared
	//   main → b → shared (ref)
	snippets := []*Snippet{
		fn("main", "main.go", "main", 1),
		fn("a", "a.go", "a", 1),
		fn("b", "b.go", "b", 1),
		fn("shared", "shared.go", "shared", 1),
	}
	edges := []SymbolEdge{
		call("main", "a", 2), call("main", "b", 3),
		call("a", "shared", 2), call("b", "shared", 2),
	}

	f := BuildForest(snippets, edges)
	canonical := f.ByID["shared"]
	if canonical.Parent != f.ByID["a"] {
		t.Errorf("shared canonical under %v, want a (first visit)", canonical.Parent.Snippet.ID)
	}
	if len(canonical.AlsoCalledBy) != 1 || canonical.AlsoCalledBy[0] != "b" {
		t.Errorf("AlsoCalledBy = %v, want [b]", canonical.AlsoCalledBy)
	}
	bChildren := f.ByID["b"].Children
	if len(bChildren) != 1 || !bChildren[0].Ref || bChildren[0].Snippet.ID != "shared" {
		t.Errorf("b should hold a Ref leaf to shared, got %+v", bChildren)
	}
	// Flatten must show shared exactly once.
	seen := 0
	for _, s := range f.Flatten(OrderPre) {
		if s.ID == "shared" {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("shared appears %d times in Flatten, want 1", seen)
	}
}

func TestBuildForest_CycleGetsRefLeaf(t *testing.T) {
	// a ↔ b with an outside caller: main → a → b → (ref a)
	snippets := []*Snippet{
		fn("main", "main.go", "main", 1),
		fn("a", "a.go", "a", 1),
		fn("b", "b.go", "b", 1),
	}
	edges := []SymbolEdge{call("main", "a", 2), call("a", "b", 2), call("b", "a", 2)}

	f := BuildForest(snippets, edges)
	bChildren := f.ByID["b"].Children
	if len(bChildren) != 1 || !bChildren[0].Ref || bChildren[0].Snippet.ID != "a" {
		t.Fatalf("cycle back-edge should be a Ref leaf under b, got %+v", bChildren)
	}
	if got := len(f.Flatten(OrderPre)); got != 3 {
		t.Errorf("Flatten returned %d snippets, want 3", got)
	}
}

func TestBuildForest_PureCyclePicksEntryPoint(t *testing.T) {
	// a → b → c → a, where a also calls c (a has highest out−in).
	snippets := []*Snippet{
		fn("a", "a.go", "a", 1),
		fn("b", "b.go", "b", 1),
		fn("c", "c.go", "c", 1),
	}
	edges := []SymbolEdge{
		call("a", "b", 2), call("b", "c", 2), call("c", "a", 2), call("a", "c", 3),
	}

	f := BuildForest(snippets, edges)
	if got := rootIDs(f); len(got) != 1 || got[0] != "a" {
		t.Fatalf("roots = %v, want [a] (max out−in degree)", got)
	}
	if got := len(f.Flatten(OrderPre)); got != 3 {
		t.Errorf("Flatten returned %d snippets, want 3", got)
	}
}

func TestBuildForest_DisconnectedComponents_LargestFirst(t *testing.T) {
	// Component 1: x → y (size 2). Component 2: lone (size 1).
	snippets := []*Snippet{
		fn("lone", "a_first.go", "lone", 1),
		fn("x", "z1.go", "x", 1),
		fn("y", "z2.go", "y", 1),
	}
	edges := []SymbolEdge{call("x", "y", 2)}

	f := BuildForest(snippets, edges)
	if got := rootIDs(f); len(got) != 2 || got[0] != "x" || got[1] != "lone" {
		t.Fatalf("roots = %v, want [x lone] (larger component first despite path order)", got)
	}
}

func TestBuildForest_TiedComponentsKeepLineOrder(t *testing.T) {
	// Unrelated changes in one file, and single-test components in one test
	// file, tie on test-ness, size and path. They must come in line order
	// on every run, not in map order.
	var snippets []*Snippet
	var want []string
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("f%02d", i)
		snippets = append(snippets, fn(id, "svc.go", id, 1+10*i))
		want = append(want, id)
	}
	var edges []SymbolEdge
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("Test%d", i)
		snippets = append(snippets, fn(id, "svc_test.go", id, 1+10*i))
		edges = append(edges, call(id, "f00", 1+10*i+1))
		want = append(want, id)
	}
	for run := 0; run < 50; run++ {
		if got := rootIDs(BuildForest(snippets, edges)); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("run %d: roots = %v, want %v", run, got, want)
		}
	}
}

func TestBuildForest_NonEntityTrailingRoots(t *testing.T) {
	imports := &Snippet{ID: "imp", FilePath: "a.go", SymbolPath: "imports", NodeType: "imports", StartLine: 1}
	preamble := &Snippet{ID: "pre", FilePath: "b.go", SymbolPath: "", NodeType: "hunk", StartLine: 5}
	entity := fn("f", "z.go", "f", 1)

	f := BuildForest([]*Snippet{imports, preamble, entity}, nil)
	if got := rootIDs(f); len(got) != 3 || got[0] != "f" || got[1] != "imp" || got[2] != "pre" {
		t.Fatalf("roots = %v, want entity first then trailing [imp pre]", got)
	}
	if len(f.ByID["imp"].Children) != 0 {
		t.Error("trailing roots must be childless")
	}
}

func TestFlatten_PostOrder(t *testing.T) {
	snippets := []*Snippet{
		fn("main", "main.go", "main", 1),
		fn("a", "a.go", "a", 1),
		fn("leaf", "leaf.go", "leaf", 1),
	}
	edges := []SymbolEdge{call("main", "a", 2), call("a", "leaf", 2)}

	f := BuildForest(snippets, edges)
	got := f.Flatten(OrderPost)
	want := []string{"leaf", "a", "main"}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("post-order = %v, want %v", snippetIDs(got), want)
		}
	}
	if got[0].Depth != 2 || got[2].Depth != 0 {
		t.Errorf("depths: leaf=%d main=%d, want 2 and 0", got[0].Depth, got[2].Depth)
	}
}

func TestNextPending_WrapsAndSkipsReviewed(t *testing.T) {
	snippets := []*Snippet{
		fn("main", "main.go", "main", 1),
		fn("a", "a.go", "a", 1),
		fn("b", "b.go", "b", 1),
	}
	edges := []SymbolEdge{call("main", "a", 2), call("main", "b", 3)}
	f := BuildForest(snippets, edges)

	pending := map[string]bool{"main": true} // a and b reviewed
	// From a, the next pending (wrapping past b) is main.
	next := f.NextPending("a", func(id string) bool { return pending[id] })
	if next == nil || next.Snippet.ID != "main" {
		t.Fatalf("NextPending = %v, want main", next)
	}
	// Nothing pending → nil.
	if f.NextPending("a", func(string) bool { return false }) != nil {
		t.Error("expected nil when nothing is pending")
	}
}

func TestBuildForest_TestsFollowCodeAsTestedBy(t *testing.T) {
	// TestHandle → handle → parse, TestHandle → fixture (test helper).
	snippets := []*Snippet{
		fn("th", "api_test.go", "TestHandle", 1),
		fn("fx", "api_test.go", "fixture", 10),
		fn("handle", "api.go", "handle", 1),
		fn("parse", "parse.go", "parse", 1),
	}
	edges := []SymbolEdge{
		call("th", "handle", 2), call("th", "fx", 3), call("handle", "parse", 2),
	}

	f := BuildForest(snippets, edges)
	if got := rootIDs(f); len(got) != 2 || got[0] != "handle" || got[1] != "th" {
		t.Fatalf("roots = %v, want [handle th]", got)
	}
	if got := childIDs(f.ByID["th"]); len(got) != 1 || got[0] != "fx" {
		t.Errorf("test children = %v, want [fx] (test→test edges keep structure)", got)
	}
	if got := f.ByID["handle"].Snippet.TestedBy; len(got) != 1 || got[0] != "TestHandle" {
		t.Errorf("handle.TestedBy = %v, want [TestHandle]", got)
	}
}

func TestIsTestPath(t *testing.T) {
	for p, want := range map[string]bool{
		"core/tree_test.go": true, "tests/test_cli.py": true, "pkg/test_x.py": true,
		"src/app.spec.ts": true, "web/__tests__/a.js": true,
		"src/test/java/FooTest.java": true, "core/tree.go": false,
		"test/helpers.js": true, "shop/tests.py": true, "TestFoo.java": true,
		"FooIT.java": true, "src/Testimony.py": false,
		"src/contest.py": false, "latest/app.ts": false,
	} {
		if got := isTestPath(p); got != want {
			t.Errorf("isTestPath(%q) = %v, want %v", p, got, want)
		}
	}
}
