package core

import "testing"

// entitySnippet builds a minimal entity snippet for call-scan tests.
func entitySnippet(id, path, lang, symbol, nodeType string, startLine int, body ...string) *Snippet {
	return &Snippet{
		ID:         id,
		FilePath:   path,
		Language:   lang,
		SymbolPath: symbol,
		NodeType:   nodeType,
		StartLine:  startLine,
		EndLine:    startLine + len(body) - 1,
		Body:       body,
	}
}

func edgeSet(edges []SymbolEdge) map[string]SymbolEdge {
	m := make(map[string]SymbolEdge, len(edges))
	for _, e := range edges {
		m[e.From+"->"+e.To] = e
	}
	return m
}

func TestBuildCallEdges_DirectCall(t *testing.T) {
	caller := entitySnippet("c", "a.go", "go", "handle", "function", 10,
		"func handle() {",
		"	x := parse()",
		"}")
	callee := entitySnippet("p", "a.go", "go", "parse", "function", 20,
		"func parse() int { return 1 }")

	edges := BuildCallEdges([]*Snippet{caller, callee})
	got := edgeSet(edges)
	e, ok := got["c->p"]
	if !ok {
		t.Fatalf("expected edge c->p, got %v", edges)
	}
	if e.Kind != EdgeCall {
		t.Errorf("Kind = %v, want EdgeCall", e.Kind)
	}
	if e.CallLine != 11 {
		t.Errorf("CallLine = %d, want 11", e.CallLine)
	}
}

func TestBuildCallEdges_MethodAndQualifiedCalls(t *testing.T) {
	caller := entitySnippet("c", "app.ts", "typescript", "run", "function", 1,
		"function run(store) {",
		"	store.put(1);",
		"	utils.format(2);",
		"}")
	put := entitySnippet("p", "store.ts", "typescript", "put", "function", 1,
		"function put(v) {}")
	format := entitySnippet("f", "utils.ts", "typescript", "format", "function", 1,
		"function format(v) {}")

	got := edgeSet(BuildCallEdges([]*Snippet{caller, put, format}))
	if _, ok := got["c->p"]; !ok {
		t.Error("method call store.put() not detected")
	}
	if _, ok := got["c->f"]; !ok {
		t.Error("qualified call utils.format() not detected")
	}
}

func TestBuildCallEdges_GoCompositeLiteral(t *testing.T) {
	caller := entitySnippet("c", "a.go", "go", "build", "function", 1,
		"func build() Config {",
		"	return Config{Host: \"x\"}",
		"}")
	typ := entitySnippet("t", "a.go", "go", "Config", "type", 10,
		"type Config struct{ Host string }")

	got := edgeSet(BuildCallEdges([]*Snippet{caller, typ}))
	if _, ok := got["c->t"]; !ok {
		t.Error("composite literal Config{} not detected")
	}
}

func TestBuildCallEdges_GoBlockOpenNotACall(t *testing.T) {
	caller := entitySnippet("c", "a.go", "go", "run", "function", 1,
		"func run(cond bool) {",
		"	if cond {",
		"	}",
		"}")
	// Entity named like the identifier before the block-open brace.
	cond := entitySnippet("x", "a.go", "go", "cond", "function", 10,
		"func cond() {}")

	if edges := BuildCallEdges([]*Snippet{caller, cond}); len(edges) != 0 {
		t.Errorf("`if cond {` treated as call: %v", edges)
	}
}

func TestBuildCallEdges_NewConstruction(t *testing.T) {
	caller := entitySnippet("c", "app.js", "javascript", "run", "function", 1,
		"function run() {",
		"	const s = new Store;",
		"}")
	class := entitySnippet("s", "store.js", "javascript", "Store", "class", 1,
		"class Store {}")

	got := edgeSet(BuildCallEdges([]*Snippet{caller, class}))
	if _, ok := got["c->s"]; !ok {
		t.Error("new Store construction not detected")
	}
}

func TestBuildCallEdges_CommentExcluded(t *testing.T) {
	caller := entitySnippet("c", "a.go", "go", "run", "function", 1,
		"func run() {",
		"	// parse() is called elsewhere",
		"}")
	callee := entitySnippet("p", "a.go", "go", "parse", "function", 10,
		"func parse() {}")

	if edges := BuildCallEdges([]*Snippet{caller, callee}); len(edges) != 0 {
		t.Errorf("call inside comment produced edge: %v", edges)
	}
}

func TestBuildCallEdges_PythonComment(t *testing.T) {
	caller := entitySnippet("c", "a.py", "python", "run", "function", 1,
		"def run():",
		"	x = parse()  # helper() too",
		"	return x")
	parse := entitySnippet("p", "a.py", "python", "parse", "function", 10,
		"def parse(): pass")
	helper := entitySnippet("h", "a.py", "python", "helper", "function", 20,
		"def helper(): pass")

	got := edgeSet(BuildCallEdges([]*Snippet{caller, parse, helper}))
	if _, ok := got["c->p"]; !ok {
		t.Error("python call not detected")
	}
	if _, ok := got["c->h"]; ok {
		t.Error("call inside # comment produced edge")
	}
}

func TestBuildCallEdges_NoSelfEdge(t *testing.T) {
	rec := entitySnippet("r", "a.go", "go", "walk", "function", 1,
		"func walk(n int) {",
		"	walk(n - 1)",
		"}")
	if edges := BuildCallEdges([]*Snippet{rec}); len(edges) != 0 {
		t.Errorf("recursive call produced self-edge: %v", edges)
	}
}

func TestBuildCallEdges_CollisionPrefersSameFile(t *testing.T) {
	caller := entitySnippet("c", "pkg/a.go", "go", "run", "function", 1,
		"func run() {",
		"	get()",
		"}")
	local := entitySnippet("l", "pkg/a.go", "go", "get", "function", 10,
		"func get() {}")
	remote := entitySnippet("r", "other/b.go", "go", "get", "function", 10,
		"func get() {}")

	got := edgeSet(BuildCallEdges([]*Snippet{caller, local, remote}))
	if _, ok := got["c->l"]; !ok {
		t.Error("same-file target not preferred")
	}
	if _, ok := got["c->r"]; ok {
		t.Error("cross-file target kept despite same-file match")
	}
}

func TestBuildCallEdges_DedupeKeepsEarliestLine(t *testing.T) {
	caller := entitySnippet("c", "a.go", "go", "run", "function", 5,
		"func run() {",
		"	parse()",
		"	parse()",
		"}")
	callee := entitySnippet("p", "a.go", "go", "parse", "function", 20,
		"func parse() {}")

	edges := BuildCallEdges([]*Snippet{caller, callee})
	if len(edges) != 1 {
		t.Fatalf("expected 1 deduped edge, got %d", len(edges))
	}
	if edges[0].CallLine != 6 {
		t.Errorf("CallLine = %d, want 6 (earliest)", edges[0].CallLine)
	}
}

func TestBuildCallEdges_DottedMethodResolvesViaLastSegment(t *testing.T) {
	method := entitySnippet("m", "spider.py", "python", "ProductSpider.parse", "function", 20,
		"def parse(self, response):",
		"	pass")
	callerSelf := entitySnippet("c1", "spider.py", "python", "ProductSpider.start", "function", 10,
		"def start(self):",
		"	self.parse(None)")
	callerObj := entitySnippet("c2", "runner.py", "python", "run", "function", 1,
		"def run(spider):",
		"	spider.parse(None)")

	got := edgeSet(BuildCallEdges([]*Snippet{method, callerSelf, callerObj}))
	if _, ok := got["c1->m"]; !ok {
		t.Error("self.parse() did not resolve to ProductSpider.parse")
	}
	if _, ok := got["c2->m"]; !ok {
		t.Error("spider.parse() did not resolve to ProductSpider.parse")
	}
}

func TestBuildCallEdges_VarReferenceEdges(t *testing.T) {
	c := entitySnippet("v", "config.py", "python", "MAX_RETRIES", "const", 1,
		"MAX_RETRIES = 3")
	f1 := entitySnippet("a", "client.py", "python", "fetch", "function", 10,
		"def fetch():",
		"	for i in range(MAX_RETRIES):",
		"		attempt(i, MAX_RETRIES)")
	f2 := entitySnippet("b", "worker.py", "python", "work", "function", 30,
		"def work():",
		"	return MAX_RETRIES * 2")

	got := edgeSet(BuildCallEdges([]*Snippet{c, f1, f2}))
	e, ok := got["a->v"]
	if !ok {
		t.Fatal("const reference from fetch not detected")
	}
	if e.Kind != EdgeCall {
		t.Errorf("Kind = %v, want EdgeCall", e.Kind)
	}
	if e.CallLine != 11 {
		t.Errorf("CallLine = %d, want 11 (earliest reference)", e.CallLine)
	}
	if _, ok := got["b->v"]; !ok {
		t.Error("const reference from work not detected")
	}
}

func TestBuildCallEdges_VarRefStopwordAndShortTokens(t *testing.T) {
	stop := entitySnippet("s", "a.py", "python", "map", "var", 1,
		"map = {}")
	short := entitySnippet("h", "a.py", "python", "db", "var", 2,
		"db = connect()")
	caller := entitySnippet("c", "a.py", "python", "use", "function", 10,
		"def use():",
		"	return map, db")

	if edges := BuildCallEdges([]*Snippet{stop, short, caller}); len(edges) != 0 {
		t.Errorf("stopword/short tokens produced edges: %v", edges)
	}
}

func TestBuildCallEdges_VarRefScopedToFileOrDir(t *testing.T) {
	// A bare identifier carries no syntax tying it to a declaration, so a
	// changed var in another directory (or language) must not be pulled in
	// by an unrelated same-named local.
	v := entitySnippet("v", "app/settings.py", "python", "config", "var", 1,
		"config = load()")
	f := entitySnippet("f", "server/handler.go", "go", "HandleIndex", "function", 10,
		"func HandleIndex() {",
		"	config := loadLocal()",
		"	_ = config",
		"}")

	if edges := BuildCallEdges([]*Snippet{v, f}); len(edges) != 0 {
		t.Errorf("cross-file/cross-language var reference produced edges: %v", edges)
	}
}

func TestBuildCallEdges_CallSiteTokenNotVarRef(t *testing.T) {
	// A token already matched as a call site resolves to the function; it
	// must not also create an edge to a same-named var — nor may the
	// function's own declaration line reference the var.
	fn := entitySnippet("fn", "pkg/a.py", "python", "config", "function", 20,
		"def config():",
		"	return 1")
	v := entitySnippet("v", "pkg/b.py", "python", "config", "var", 1,
		"config = {}")
	caller := entitySnippet("c", "pkg/a.py", "python", "use", "function", 10,
		"def use():",
		"	return config()")

	got := edgeSet(BuildCallEdges([]*Snippet{fn, v, caller}))
	if _, ok := got["c->fn"]; !ok {
		t.Error("call edge to function config missing")
	}
	if _, ok := got["c->v"]; ok {
		t.Error("call site config() also produced a var-reference edge")
	}
	if _, ok := got["fn->v"]; ok {
		t.Error("function's own declaration line produced fn→var edge")
	}
}

func TestBuildCallEdges_NestedSpanSkip(t *testing.T) {
	class := entitySnippet("k", "store.js", "javascript", "Store", "class", 1,
		"class Store {",
		"	reset() {",
		"		clearAll();",
		"	}",
		"	init = makeInit();",
		"}")
	method := entitySnippet("m", "store.js", "javascript", "Store.reset", "function", 2,
		"	reset() {",
		"		clearAll();",
		"	}")
	clearAll := entitySnippet("h", "util.js", "javascript", "clearAll", "function", 1,
		"function clearAll() {}")
	makeInit := entitySnippet("i", "util.js", "javascript", "makeInit", "function", 5,
		"function makeInit() {}")

	got := edgeSet(BuildCallEdges([]*Snippet{class, method, clearAll, makeInit}))
	if _, ok := got["m->h"]; !ok {
		t.Error("method's own edge missing")
	}
	if _, ok := got["k->h"]; ok {
		t.Error("class container duplicated its method's edge")
	}
	if _, ok := got["k->i"]; !ok {
		t.Error("class-level call outside nested method span not detected")
	}
}

func TestOrderSnippets_ImportEdgesDoNotSetDepth(t *testing.T) {
	a := entitySnippet("a", "a.go", "go", "fa", "function", 1, "func fa() {}")
	b := entitySnippet("b", "b.go", "go", "fb", "function", 1, "func fb() {}")
	edges := []SymbolEdge{{From: "a", To: "b", Kind: EdgeImport}}

	ordered := OrderSnippets([]*Snippet{a, b}, edges, PresetTopDown)
	for _, s := range ordered {
		if s.Depth != 0 {
			t.Errorf("snippet %s: depth %d from import edge, want 0", s.ID, s.Depth)
		}
	}
}
