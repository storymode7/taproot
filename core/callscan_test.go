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

func TestBuildCallEdges_IgnoresStringsAndComments(t *testing.T) {
	test := entitySnippet("t", "a_test.go", "go", "TestX", "function", 1,
		"func TestX(t *testing.T) {",
		"	src := \"func run() { parse() }\"",
		"	raw := `",
		"	parse()",
		"	`",
		"	/* parse() */ // parse()",
		"}")
	callee := entitySnippet("p", "a.go", "go", "parse", "function", 20,
		"func parse() int { return 1 }")

	if edges := BuildCallEdges([]*Snippet{test, callee}); len(edges) != 0 {
		t.Errorf("string/comment mentions must not create edges, got %v", edges)
	}
}

func TestBuildCallEdges_TemplateInterpolationIsCode(t *testing.T) {
	caller := entitySnippet("c", "a.ts", "typescript", "label", "function", 1,
		"function label(x) {",
		"	return `n=${count(x)} parse()`;",
		"}")
	count := entitySnippet("n", "a.ts", "typescript", "count", "function", 5,
		"function count(x) {}")
	parse := entitySnippet("p", "a.ts", "typescript", "parse", "function", 6,
		"function parse(x) {}")

	got := edgeSet(BuildCallEdges([]*Snippet{caller, count, parse}))
	if _, ok := got["c->n"]; !ok {
		t.Errorf("call inside ${} must count, got %v", got)
	}
	if _, ok := got["c->p"]; ok {
		t.Errorf("call in template text must not count, got %v", got)
	}
}

func TestBuildCallEdges_PythonTripleQuoteAndRustLifetime(t *testing.T) {
	py := entitySnippet("y", "t.py", "python", "test_it", "function", 1,
		"def test_it():",
		"    src = \"\"\"",
		"    parse()",
		"    \"\"\"",
		"    return src")
	pyParse := entitySnippet("pp", "m.py", "python", "parse", "function", 1,
		"def parse(): pass")
	rs := entitySnippet("r", "a.rs", "rust", "get", "function", 1,
		"fn get<'a>(s: &'a str) -> u8 {",
		"    decode(s)",
		"}")
	decode := entitySnippet("d", "a.rs", "rust", "decode", "function", 5,
		"fn decode(s: &str) -> u8 { 0 }")

	got := edgeSet(BuildCallEdges([]*Snippet{py, pyParse, rs, decode}))
	if _, ok := got["y->pp"]; ok {
		t.Errorf("triple-quoted text must not create edges, got %v", got)
	}
	if _, ok := got["r->d"]; !ok {
		t.Errorf("lifetime 'a must not open a char literal, got %v", got)
	}
}

func TestBuildCallEdges_NoCrossLanguageEdges(t *testing.T) {
	goFn := entitySnippet("g", "main.go", "go", "run", "function", 1,
		"func run() {}")
	py := entitySnippet("p", "tool.py", "python", "main", "function", 1,
		"def main():",
		"    run()")
	js := entitySnippet("j", "a.js", "javascript", "boot", "function", 1,
		"function boot() { start(); }")
	ts := entitySnippet("s", "b.ts", "typescript", "start", "function", 1,
		"function start() {}")

	got := edgeSet(BuildCallEdges([]*Snippet{goFn, py, js, ts}))
	if _, ok := got["p->g"]; ok {
		t.Errorf("python must not call a Go function, got %v", got)
	}
	if _, ok := got["j->s"]; !ok {
		t.Errorf("JS → TS calls are one family, got %v", got)
	}
}

func TestBuildCallEdges_DeclarationLineIsNotACall(t *testing.T) {
	// Same-named members of two classes both changed: each one's own
	// declaration line names the other in call shape.
	cases := []struct{ name, path, lang, member, decl string }{
		{"python __init__", "pets.py", "python", "__init__", "def __init__(self, name):"},
		{"rust new", "lib.rs", "rust", "new", "pub fn new() -> Self {"},
		{"js render", "ui.js", "javascript", "render", "render() {"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := entitySnippet("a", tc.path, tc.lang, "A."+tc.member, "function", 2, tc.decl, "  x()")
			b := entitySnippet("b", tc.path, tc.lang, "B."+tc.member, "function", 8, tc.decl, "  y()")
			if edges := BuildCallEdges([]*Snippet{a, b}); len(edges) != 0 {
				t.Errorf("declaration lines made edges: %v", edges)
			}
		})
	}
}

func TestBuildCallEdges_UnchangedMethodDeclarationInAChangedClass(t *testing.T) {
	// Dog changed only at class level; its unchanged __init__ is scanned as
	// part of the Dog snippet and must not read as a call to Cat.__init__.
	src := `class Cat:
    def __init__(self, name):
        self.name = name.title()


class Dog:
    sound = "woof!"

    def __init__(self, name):
        self.name = name
`
	files := []*FileDiff{{
		Path: "pets.py", OldPath: "pets.py", Language: "python", Source: []byte(src),
		Hunks: []Hunk{
			{OldStart: 3, OldLines: 1, NewStart: 3, NewLines: 1, Lines: []string{"@@ -3,1 +3,1 @@", "-        self.name = name", "+        self.name = name.title()"}},
			{OldStart: 7, OldLines: 1, NewStart: 7, NewLines: 1, Lines: []string{"@@ -7,1 +7,1 @@", `-    sound = "woof"`, `+    sound = "woof!"`}},
		},
	}}
	snippets := BuildSnippets(files)
	if got := symbolPaths(snippets); len(got) != 2 {
		t.Fatalf("snippets %v, want Cat.__init__ and Dog", got)
	}
	if edges := BuildCallEdges(snippets); len(edges) != 0 {
		t.Errorf("Dog's unchanged def __init__( made an edge: %v", edges)
	}
}

func TestAnnotateUnchangedCallers_DeclarationIsNotACaller(t *testing.T) {
	src := `class Cat:
    def __init__(self, name):
        self.name = name.title()


class Dog:
    def __init__(self, name):
        self.name = name
`
	files := []*FileDiff{{
		Path: "pets.py", OldPath: "pets.py", Language: "python", Source: []byte(src),
		Hunks: []Hunk{{OldStart: 3, OldLines: 1, NewStart: 3, NewLines: 1,
			Lines: []string{"@@ -3,1 +3,1 @@", "-        self.name = name", "+        self.name = name.title()"}}},
	}}
	snippets := BuildSnippets(files)
	AnnotateUnchangedCallers(files, snippets)
	if len(snippets) != 1 || len(snippets[0].UnchangedCallers) != 0 {
		t.Errorf("unchanged Dog.__init__ reported as a caller: %v", snippets[0].UnchangedCallers)
	}
}

func TestBuildCallEdges_DelegationToASameNamedMethodLinks(t *testing.T) {
	// A method that forwards to a same-named method of another object calls
	// that method; it must not resolve to itself.
	cases := []struct {
		name           string
		caller, callee *Snippet
		wantEdge       bool
	}{{
		name: "python self.repo.save",
		caller: entitySnippet("c", "app/service.py", "python", "UserService.save", "function", 5,
			"def save(self, u):", "    self.repo.save(u)"),
		callee: entitySnippet("r", "app/repo.py", "python", "UserRepo.save", "function", 3,
			"def save(self, u):", "    db.put(u)"),
		wantEdge: true,
	}, {
		name: "go s.store.Get",
		caller: entitySnippet("c", "users/service.go", "go", "Get", "function", 9,
			"func (s *Service) Get(id string) User {", "\treturn s.store.Get(id)", "}"),
		callee: entitySnippet("r", "users/store.go", "go", "Get", "function", 4,
			"func (st *Store) Get(id string) User {", "\treturn st.m[id]", "}"),
		wantEdge: true,
	}, {
		name: "js arrow wrapper",
		caller: entitySnippet("c", "api/save.js", "javascript", "save", "function", 1,
			"const save = (x) => repo.save(x)"),
		callee: entitySnippet("r", "api/repo.js", "javascript", "Repo.save", "function", 2,
			"save(x) {", "  this.items.push(x)", "}"),
		wantEdge: true,
	}, {
		// Recursion through the receiver of a Go method stays a self call.
		name: "go receiver recursion",
		caller: entitySnippet("c", "a/parse.go", "go", "parseExpr", "function", 3,
			"func (p *parser) parseExpr() Expr {", "\treturn p.parseExpr()", "}"),
		callee: entitySnippet("r", "b/parse.go", "go", "parseExpr", "function", 3,
			"func (q *other) parseExpr() Expr {", "\treturn nil", "}"),
	}, {
		name: "python super().save",
		caller: entitySnippet("c", "shop/models.py", "python", "Order.save", "function", 3,
			"def save(self, *args, **kwargs):", "    super().save(*args, **kwargs)"),
		callee: entitySnippet("r", "shop/repo.py", "python", "AuditRepo.save", "function", 3,
			"def save(self, row):", "    log(row)"),
	}, {
		name: "js super.render",
		caller: entitySnippet("c", "ui/fancy.js", "javascript", "FancyButton.render", "function", 2,
			"render() {", "  return super.render();", "}"),
		callee: entitySnippet("r", "ui/chart.js", "javascript", "Chart.render", "function", 2,
			"render() {", "  draw();", "}"),
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, got := edgeSet(BuildCallEdges([]*Snippet{tc.caller, tc.callee}))["c->r"]
			if got != tc.wantEdge {
				t.Errorf("edge caller->callee = %v, want %v", got, tc.wantEdge)
			}
		})
	}
}

func TestBuildCallEdges_OwnNameCallsStayInTheirFile(t *testing.T) {
	// Recursion and calls on self, this or a sibling overload resolve to the
	// caller's own file as before: never to same-named entities elsewhere.
	cases := []struct {
		name string
		a, b *Snippet
	}{{
		"bare recursion",
		entitySnippet("a", "a/walk.go", "go", "walk", "function", 1, "func walk(n *Node) {", "\tfor _, c := range n.Kids {", "\t\twalk(c)", "\t}", "}"),
		entitySnippet("b", "b/walk.go", "go", "walk", "function", 1, "func walk(n *Node) {", "\tvisit(n)", "}"),
	}, {
		"js recursion in synced copies",
		entitySnippet("a", "shared/lib/render.js", "javascript", "renderTree", "function", 1, "function renderTree(kids) {", "  kids.forEach(k => renderTree(k.kids));", "}"),
		entitySnippet("b", "apps/pwa/lib/shared/render.js", "javascript", "renderTree", "function", 1, "function renderTree(kids) {", "  kids.forEach(k => renderTree(k.kids));", "}"),
	}, {
		"python self.visit",
		entitySnippet("a", "x/a.py", "python", "A.visit", "function", 2, "def visit(self, node):", "    for c in node.kids:", "        self.visit(c)"),
		entitySnippet("b", "y/b.py", "python", "B.visit", "function", 2, "def visit(self, node):", "    print(node)"),
	}, {
		"java bare overload",
		entitySnippet("a", "io/A.java", "java", "A.write", "function", 2, "void write(byte[] b) {", "    write(b, 0, b.length);", "}"),
		entitySnippet("b", "io/B.java", "java", "B.write", "function", 2, "void write(byte[] b) {", "    out.flush();", "}"),
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if edges := BuildCallEdges([]*Snippet{tc.a, tc.b}); len(edges) != 0 {
				t.Errorf("own-name call linked to another file: %v", edges)
			}
		})
	}
}

func TestBuildCallEdges_VarDeclarationIsNotAReference(t *testing.T) {
	// Each declaration names itself; same-named vars of sibling files must
	// not link (Python __all__, per-platform Go consts, JS module styles).
	cases := []struct {
		name string
		a, b *Snippet
	}{{
		"python __all__",
		entitySnippet("a", "pkg/a.py", "python", "__all__", "var", 1, `__all__ = ["load", "load_all"]`),
		entitySnippet("b", "pkg/b.py", "python", "__all__", "var", 1, `__all__ = ["save", "save_all"]`),
	}, {
		"go per-arch consts",
		entitySnippet("a", "goarch/zgoarch_riscv64.go", "go", "IsLoong64", "const", 9, "const IsLoong64 = 0"),
		entitySnippet("b", "goarch/zgoarch_wasm.go", "go", "IsLoong64", "const", 9, "const IsLoong64 = 0"),
	}, {
		"js module styles",
		entitySnippet("a", "src/components/Header.js", "javascript", "styles", "var", 3, "const styles = makeStyles({ root: {} });"),
		entitySnippet("b", "src/components/Footer.js", "javascript", "styles", "var", 3, "const styles = makeStyles({ root: {} });"),
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if edges := BuildCallEdges([]*Snippet{tc.a, tc.b}); len(edges) != 0 {
				t.Errorf("declarations linked to each other: %v", edges)
			}
		})
	}
	// A method named like a var still reads it.
	method := entitySnippet("m", "pkg/client.py", "python", "Client.retries", "function", 4,
		"def retries(self):", "    return config.retries")
	v := entitySnippet("v", "pkg/config.py", "python", "retries", "var", 1, "retries = 3")
	if _, ok := edgeSet(BuildCallEdges([]*Snippet{method, v}))["m->v"]; !ok {
		t.Error("Client.retries lost its reference to the retries var")
	}
}
