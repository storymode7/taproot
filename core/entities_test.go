package core

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Go entity extraction
// ---------------------------------------------------------------------------

const goSrc = `package foo

import (
	"fmt"
	"strings"
)

func formatDate(t string) string {
	return t
}

func parseCSV(r string) ([]string, error) {
	return nil, nil
}

type Config struct {
	Host string
	Port int
}

const DefaultTimeout = 30

var globalState = struct{}{}
`

func TestExtractEntities_Go(t *testing.T) {
	entities := ExtractEntities([]byte(goSrc), "go")
	if len(entities) == 0 {
		t.Fatal("expected entities, got none")
	}

	byName := make(map[string]Entity)
	for _, e := range entities {
		byName[e.Name] = e
	}

	cases := []struct{ name, kind string }{
		{"imports", "imports"},
		{"formatDate", "function"},
		{"parseCSV", "function"},
		{"Config", "type"},
		{"DefaultTimeout", "const"},
		{"globalState", "var"},
	}
	for _, tc := range cases {
		e, ok := byName[tc.name]
		if !ok {
			t.Errorf("entity %q not found (got: %v)", tc.name, entityNames(entities))
			continue
		}
		if e.Kind != tc.kind {
			t.Errorf("entity %q: kind = %q, want %q", tc.name, e.Kind, tc.kind)
		}
		if e.StartLine <= 0 || e.EndLine < e.StartLine {
			t.Errorf("entity %q: bad lines [%d, %d]", tc.name, e.StartLine, e.EndLine)
		}
	}
}

func TestExtractEntities_Go_SingleLineVarWithParens(t *testing.T) {
	// A single-line var whose initializer contains parens must NOT be
	// treated as a `var ( ... )` block — that would swallow every
	// declaration until the next `)` line (regression: interface guard
	// `var _ Source = (*GitDiffAdapter)(nil)` absorbed the functions
	// below it).
	src := `package foo

var _ Iface = (*Impl)(nil)

func After() int {
	return 1
}
`
	entities := ExtractEntities([]byte(src), "go")
	byName := make(map[string]Entity)
	for _, e := range entities {
		byName[e.Name] = e
	}

	guard, ok := byName["_"]
	if !ok {
		t.Fatalf("interface-guard var not found (got: %v)", entityNames(entities))
	}
	if guard.StartLine != guard.EndLine {
		t.Errorf("single-line var spans [%d, %d], want single line", guard.StartLine, guard.EndLine)
	}
	if _, ok := byName["After"]; !ok {
		t.Errorf("function after single-line var not extracted (got: %v)", entityNames(entities))
	}
}

func TestExtractEntities_Go_OrderPreserved(t *testing.T) {
	entities := ExtractEntities([]byte(goSrc), "go")
	idx := make(map[string]int)
	for i, e := range entities {
		idx[e.Name] = i
	}
	if idx["formatDate"] >= idx["parseCSV"] {
		t.Errorf("expected formatDate before parseCSV, got %d >= %d", idx["formatDate"], idx["parseCSV"])
	}
}

// ---------------------------------------------------------------------------
// JS entity extraction
// ---------------------------------------------------------------------------

const jsSrc = `import { foo } from './foo';
import bar from './bar';

export function greet(name) {
  return 'Hello ' + name;
}

export class Greeter {
  constructor(name) {
    this.name = name;
  }
}

const TIMEOUT = 5000;
`

func TestExtractEntities_JS(t *testing.T) {
	entities := ExtractEntities([]byte(jsSrc), "javascript")
	byName := make(map[string]Entity)
	for _, e := range entities {
		byName[e.Name] = e
	}

	if _, ok := byName["imports"]; !ok {
		t.Errorf("imports entity missing (got %v)", entityNames(entities))
	}
	if e, ok := byName["greet"]; !ok {
		t.Error("greet function missing")
	} else if e.Kind != "function" {
		t.Errorf("greet kind = %q, want function", e.Kind)
	}
	if e, ok := byName["Greeter"]; !ok {
		t.Error("Greeter class missing")
	} else if e.Kind != "class" {
		t.Errorf("Greeter kind = %q, want class", e.Kind)
	}
}

func TestExtractEntities_TS_ArrowConsts(t *testing.T) {
	src := `const handler = async (req, res) => {
  return res.send(ok(req));
};

export const ok = (x) => x;

const double = x => x * 2;

const fn = function (a) {
  return a;
};

const TIMEOUT = 5000;
`
	entities := ExtractEntities([]byte(src), "typescript")
	byName := make(map[string]Entity)
	for _, e := range entities {
		byName[e.Name] = e
	}

	cases := []struct{ name, kind string }{
		{"handler", "function"},
		{"ok", "function"},
		{"double", "function"},
		{"fn", "function"},
		{"TIMEOUT", "var"},
	}
	for _, tc := range cases {
		e, ok := byName[tc.name]
		if !ok {
			t.Errorf("entity %q not found (got %v)", tc.name, entityNames(entities))
			continue
		}
		if e.Kind != tc.kind {
			t.Errorf("entity %q: kind = %q, want %q", tc.name, e.Kind, tc.kind)
		}
	}
	if e := byName["handler"]; e.EndLine != 3 {
		t.Errorf("handler span = [%d, %d], want end 3", e.StartLine, e.EndLine)
	}
	if e := byName["ok"]; e.StartLine != e.EndLine {
		t.Errorf("single-line arrow ok spans [%d, %d], want single line", e.StartLine, e.EndLine)
	}
}

func TestExtractEntities_TS_ClassMethods(t *testing.T) {
	src := `export class Store<T> {
  private items: T[] = [];

  constructor(initial: T[]) {
    this.items = initial;
  }

  static create<T>(): Store<T> {
    return new Store([]);
  }

  async put(item: T) {
    this.items.push(item);
  }

  get size() {
    return this.items.length;
  }

  onClear = (e) => {
    this.items = [];
  };

  #reset() {
    this.items = [];
  }
}
`
	entities := ExtractEntities([]byte(src), "typescript")
	byName := make(map[string]Entity)
	for _, e := range entities {
		byName[e.Name] = e
	}

	if e, ok := byName["Store"]; !ok || e.Kind != "class" {
		t.Fatalf("Store class missing or wrong kind (got %v)", entityNames(entities))
	}
	for _, name := range []string{"Store.constructor", "Store.create", "Store.put", "Store.size", "Store.onClear", "Store.#reset"} {
		e, ok := byName[name]
		if !ok {
			t.Errorf("method %q not found (got %v)", name, entityNames(entities))
			continue
		}
		if e.Kind != "function" {
			t.Errorf("method %q: kind = %q, want function", name, e.Kind)
		}
	}
	// this.items.push(...) inside put must not become a method
	for _, e := range entities {
		if e.Name == "Store.this" || e.Name == "Store.push" {
			t.Errorf("body line extracted as method: %v", e)
		}
	}
}

// ---------------------------------------------------------------------------
// Python entity extraction
// ---------------------------------------------------------------------------

const pySrc = `import os
from pathlib import Path

def read_file(path):
    with open(path) as f:
        return f.read()

class FileParser:
    def __init__(self, path):
        self.path = path

def write_file(path, content):
    pass
`

func TestExtractEntities_Python(t *testing.T) {
	entities := ExtractEntities([]byte(pySrc), "python")
	byName := make(map[string]Entity)
	for _, e := range entities {
		byName[e.Name] = e
	}

	cases := []struct{ name, kind string }{
		{"imports", "imports"},
		{"read_file", "function"},
		{"FileParser", "class"},
		{"write_file", "function"},
	}
	for _, tc := range cases {
		e, ok := byName[tc.name]
		if !ok {
			t.Errorf("entity %q not found (got %v)", tc.name, entityNames(entities))
			continue
		}
		if e.Kind != tc.kind {
			t.Errorf("entity %q: kind = %q, want %q", tc.name, e.Kind, tc.kind)
		}
	}
}

func TestExtractEntities_Python_ClassMethodsAndModuleVars(t *testing.T) {
	src := `import os

MAX_RETRIES = 3

config: dict = {
    "a": 1,
}

class Parser:
    version = 1

    def __init__(self, path):
        self.path = path

    async def parse(self):
        def helper():
            pass
        return helper()

count = MAX_RETRIES == 3
`
	entities := ExtractEntities([]byte(src), "python")
	byName := make(map[string]Entity)
	for _, e := range entities {
		byName[e.Name] = e
	}

	cases := []struct{ name, kind string }{
		{"MAX_RETRIES", "const"},
		{"config", "var"},
		{"Parser", "class"},
		{"Parser.__init__", "function"},
		{"Parser.parse", "function"},
		{"count", "var"},
	}
	for _, tc := range cases {
		e, ok := byName[tc.name]
		if !ok {
			t.Errorf("entity %q not found (got %v)", tc.name, entityNames(entities))
			continue
		}
		if e.Kind != tc.kind {
			t.Errorf("entity %q: kind = %q, want %q", tc.name, e.Kind, tc.kind)
		}
	}
	// multi-line dict value spans to its closing brace
	if e := byName["config"]; e.EndLine != 7 {
		t.Errorf("config span = [%d, %d], want end 7", e.StartLine, e.EndLine)
	}
	// nested def helper must not be a method
	if _, ok := byName["Parser.helper"]; ok {
		t.Error("nested def extracted as method")
	}
	// class attribute assignment must not become a module var
	if e, ok := byName["version"]; ok {
		t.Errorf("class attribute extracted as module var: %v", e)
	}
}

func TestExtractEntities_Java_ClassMethods(t *testing.T) {
	src := `import java.util.List;

public class Cache {
    private List<String> items;

    public void put(String item) {
        items.add(item);
    }

    private static int size() {
        return 0;
    }
}
`
	entities := ExtractEntities([]byte(src), "java")
	byName := make(map[string]Entity)
	for _, e := range entities {
		byName[e.Name] = e
	}

	if e, ok := byName["Cache"]; !ok || e.Kind != "class" {
		t.Fatalf("Cache class missing or wrong kind (got %v)", entityNames(entities))
	}
	for _, name := range []string{"Cache.put", "Cache.size"} {
		e, ok := byName[name]
		if !ok {
			t.Errorf("method %q not found (got %v)", name, entityNames(entities))
			continue
		}
		if e.Kind != "function" {
			t.Errorf("method %q: kind = %q, want function", name, e.Kind)
		}
	}
}

func TestExtractEntities_Rust_ImplMethods(t *testing.T) {
	src := `use std::fmt;

struct Point {
    x: i32,
}

impl Point {
    pub fn new(x: i32) -> Self {
        Point { x }
    }

    fn norm(&self) -> i32 {
        self.x
    }
}

impl fmt::Display for Point {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        write!(f, "{}", self.x)
    }
}
`
	entities := ExtractEntities([]byte(src), "rust")
	byName := make(map[string]Entity)
	for _, e := range entities {
		byName[e.Name] = e
	}

	cases := []struct{ name, kind string }{
		{"Point", "type"},
		{"Point.new", "function"},
		{"Point.norm", "function"},
		{"Point.fmt", "function"},
	}
	for _, tc := range cases {
		e, ok := byName[tc.name]
		if !ok {
			t.Errorf("entity %q not found (got %v)", tc.name, entityNames(entities))
			continue
		}
		if e.Kind != tc.kind {
			t.Errorf("entity %q: kind = %q, want %q", tc.name, e.Kind, tc.kind)
		}
	}
}

func TestExtractEntities_MethodSpansNestInsideClass(t *testing.T) {
	entities := ExtractEntities([]byte(pySrc), "python")
	byName := make(map[string]Entity)
	for _, e := range entities {
		byName[e.Name] = e
	}

	class, ok := byName["FileParser"]
	if !ok {
		t.Fatalf("FileParser class missing (got %v)", entityNames(entities))
	}
	method, ok := byName["FileParser.__init__"]
	if !ok {
		t.Fatalf("FileParser.__init__ method missing (got %v)", entityNames(entities))
	}
	if method.StartLine <= class.StartLine || method.EndLine > class.EndLine {
		t.Errorf("method span [%d, %d] not strictly inside class span [%d, %d]",
			method.StartLine, method.EndLine, class.StartLine, class.EndLine)
	}
}

func TestExtractEntities_JS_SemicolonlessArrow(t *testing.T) {
	// standard.js-style code: a multi-line arrow const with no semicolons
	// must not swallow the declarations that follow it.
	src := `const double = x =>
  x * 2
const NAME = 'astra'
function after() {
  return NAME
}
`
	entities := ExtractEntities([]byte(src), "javascript")
	byName := make(map[string]Entity)
	for _, e := range entities {
		byName[e.Name] = e
	}

	d, ok := byName["double"]
	if !ok {
		t.Fatalf("double not found (got %v)", entityNames(entities))
	}
	if d.EndLine > 2 {
		t.Errorf("double span = [%d, %d], want end <= 2 (must stop before const NAME)", d.StartLine, d.EndLine)
	}
	if _, ok := byName["NAME"]; !ok {
		t.Errorf("NAME swallowed by arrow body (got %v)", entityNames(entities))
	}
	if _, ok := byName["after"]; !ok {
		t.Errorf("after swallowed by arrow body (got %v)", entityNames(entities))
	}
}

func TestExtractEntities_JS_NoSemiArrowProperty(t *testing.T) {
	// A class arrow property without a trailing semicolon (Prettier semi:false)
	// must not extend into the following method.
	src := `class A {
  f = (x) => x + 1
  g() {
    return helper(2)
  }
}
`
	entities := ExtractEntities([]byte(src), "javascript")
	byName := make(map[string]Entity)
	for _, e := range entities {
		byName[e.Name] = e
	}

	f, ok := byName["A.f"]
	if !ok {
		t.Fatalf("A.f not found (got %v)", entityNames(entities))
	}
	if f.EndLine != 2 {
		t.Errorf("A.f span = [%d, %d], want [2, 2] (must not leak into g)", f.StartLine, f.EndLine)
	}
	g, ok := byName["A.g"]
	if !ok {
		t.Fatalf("A.g not found (got %v)", entityNames(entities))
	}
	if g.StartLine != 3 || g.EndLine != 5 {
		t.Errorf("A.g span = [%d, %d], want [3, 5]", g.StartLine, g.EndLine)
	}
}

func TestExtractEntities_JS_BraceInStringMethod(t *testing.T) {
	// A lone brace inside a string literal must not unbalance the method span.
	src := `class Parser {
  isOpen(ch) {
    return ch === "{";
  }
  isClose(ch) {
    return ch === "}";
  }
  next() {
    return 0;
  }
}
`
	entities := ExtractEntities([]byte(src), "javascript")
	byName := make(map[string]Entity)
	for _, e := range entities {
		byName[e.Name] = e
	}

	cases := []struct {
		name       string
		start, end int
	}{
		{"Parser.isOpen", 2, 4},
		{"Parser.isClose", 5, 7},
		{"Parser.next", 8, 10},
	}
	for _, tc := range cases {
		e, ok := byName[tc.name]
		if !ok {
			t.Errorf("method %q not found (got %v)", tc.name, entityNames(entities))
			continue
		}
		if e.StartLine != tc.start || e.EndLine != tc.end {
			t.Errorf("%s span = [%d, %d], want [%d, %d]", tc.name, e.StartLine, e.EndLine, tc.start, tc.end)
		}
	}
}

func TestExtractEntities_JS_MultiLineMethodSignature(t *testing.T) {
	// Prettier-style multi-line parameter list: the span must cover the whole
	// body, not stop at the first parameter line ending in ','.
	src := `class Widget {
  render(
    props,
    state,
  ) {
    return props;
  }
}
`
	entities := ExtractEntities([]byte(src), "javascript")
	byName := make(map[string]Entity)
	for _, e := range entities {
		byName[e.Name] = e
	}

	r, ok := byName["Widget.render"]
	if !ok {
		t.Fatalf("Widget.render not found (got %v)", entityNames(entities))
	}
	if r.StartLine != 2 || r.EndLine != 7 {
		t.Errorf("Widget.render span = [%d, %d], want [2, 7]", r.StartLine, r.EndLine)
	}
}

func TestExtractEntities_TS_GenericMethod(t *testing.T) {
	src := `class Repo {
  find<T extends Base<K>>(id: string): T {
    return null;
  }
}
`
	entities := ExtractEntities([]byte(src), "typescript")
	byName := make(map[string]Entity)
	for _, e := range entities {
		byName[e.Name] = e
	}
	if _, ok := byName["Repo.find"]; !ok {
		t.Errorf("generic method Repo.find not found (got %v)", entityNames(entities))
	}
}

func TestExtractEntities_Python_DocstringAssignments(t *testing.T) {
	// Assignment-looking lines inside a module docstring must not become
	// entities — and an unclosed bracket in a docstring example must not
	// swallow the rest of the file.
	src := `"""Module docs.

Example configuration:

DEFAULT_TIMEOUT = 30
SERVERS = [
    "a",
"""

REAL = 1

def real_func():
    pass
`
	entities := ExtractEntities([]byte(src), "python")
	byName := make(map[string]Entity)
	for _, e := range entities {
		byName[e.Name] = e
	}

	if _, ok := byName["DEFAULT_TIMEOUT"]; ok {
		t.Error("docstring text DEFAULT_TIMEOUT extracted as entity")
	}
	if _, ok := byName["SERVERS"]; ok {
		t.Error("docstring text SERVERS extracted as entity")
	}
	if _, ok := byName["REAL"]; !ok {
		t.Errorf("REAL not extracted (got %v)", entityNames(entities))
	}
	if _, ok := byName["real_func"]; !ok {
		t.Errorf("real_func not extracted (got %v)", entityNames(entities))
	}
}

func TestExtractEntities_Python_NestedClassBeforeMethods(t *testing.T) {
	// A nested class with a deeper def preceding the real methods must not
	// lock the method indent: run/stop are the outer class's methods.
	src := `class Foo:
    class Config:
        def validate(self):
            pass

    def run(self):
        pass

    def stop(self):
        pass
`
	entities := ExtractEntities([]byte(src), "python")
	byName := make(map[string]Entity)
	for _, e := range entities {
		byName[e.Name] = e
	}

	if _, ok := byName["Foo.run"]; !ok {
		t.Errorf("Foo.run not extracted (got %v)", entityNames(entities))
	}
	if _, ok := byName["Foo.stop"]; !ok {
		t.Errorf("Foo.stop not extracted (got %v)", entityNames(entities))
	}
	if _, ok := byName["Foo.validate"]; ok {
		t.Error("nested class's method misattributed as Foo.validate")
	}
}

func TestExtractEntities_Python_OneLinerMethod(t *testing.T) {
	// A single-line method body must not extend its span over the next method.
	src := `class Foo:
    def __repr__(self): return "Foo"

    def compute(self):
        a = 1
        return a
`
	entities := ExtractEntities([]byte(src), "python")
	byName := make(map[string]Entity)
	for _, e := range entities {
		byName[e.Name] = e
	}

	r, ok := byName["Foo.__repr__"]
	if !ok {
		t.Fatalf("Foo.__repr__ not found (got %v)", entityNames(entities))
	}
	if r.EndLine != 2 {
		t.Errorf("Foo.__repr__ span = [%d, %d], want [2, 2]", r.StartLine, r.EndLine)
	}
	c, ok := byName["Foo.compute"]
	if !ok {
		t.Fatalf("Foo.compute not found (got %v)", entityNames(entities))
	}
	if r.EndLine >= c.StartLine {
		t.Errorf("Foo.__repr__ [%d, %d] overlaps Foo.compute [%d, %d]", r.StartLine, r.EndLine, c.StartLine, c.EndLine)
	}
}

func TestExtractEntities_Python_DecoratedMethodSpan(t *testing.T) {
	// Decorator lines belong to the decorated method's span, so a diff
	// touching only "@property" is attributed to the method.
	src := `class Foo:
    @property
    def value(self):
        return self._v
`
	entities := ExtractEntities([]byte(src), "python")
	byName := make(map[string]Entity)
	for _, e := range entities {
		byName[e.Name] = e
	}

	v, ok := byName["Foo.value"]
	if !ok {
		t.Fatalf("Foo.value not found (got %v)", entityNames(entities))
	}
	if v.StartLine != 2 {
		t.Errorf("Foo.value starts at %d, want 2 (decorator included)", v.StartLine)
	}
}

func TestExtractEntities_Java_GenericReturnMethod(t *testing.T) {
	src := `public class Svc {
    public Map<String, List<Integer>> transform(Input in) {
        return null;
    }

    public int simple() {
        return 0;
    }
}
`
	entities := ExtractEntities([]byte(src), "java")
	byName := make(map[string]Entity)
	for _, e := range entities {
		byName[e.Name] = e
	}
	if _, ok := byName["Svc.transform"]; !ok {
		t.Errorf("generic-return method Svc.transform not found (got %v)", entityNames(entities))
	}
	if _, ok := byName["Svc.simple"]; !ok {
		t.Errorf("Svc.simple not found (got %v)", entityNames(entities))
	}
}

func TestExtractEntities_Rust_ImplBraceInChar(t *testing.T) {
	// A brace inside a char literal must not unbalance method spans.
	src := `impl Parser {
    fn is_open(c: char) -> bool {
        c == '{'
    }

    fn next(&self) -> u8 {
        0
    }
}
`
	entities := ExtractEntities([]byte(src), "rust")
	byName := make(map[string]Entity)
	for _, e := range entities {
		byName[e.Name] = e
	}

	o, ok := byName["Parser.is_open"]
	if !ok {
		t.Fatalf("Parser.is_open not found (got %v)", entityNames(entities))
	}
	if o.StartLine != 2 || o.EndLine != 4 {
		t.Errorf("Parser.is_open span = [%d, %d], want [2, 4]", o.StartLine, o.EndLine)
	}
	if _, ok := byName["Parser.next"]; !ok {
		t.Errorf("Parser.next not found (got %v)", entityNames(entities))
	}
}

func TestExtractEntities_Rust_ImplNamesDistinctFromStruct(t *testing.T) {
	// impl blocks must not share (Kind, Name) with the struct — snippet
	// grouping keys on Kind::Name and would merge them (and two impl blocks
	// of the same type would merge with each other).
	src := `struct Foo {
    x: i32,
}

impl Foo {
    const LIMIT: i32 = 10;
}

impl Display for Foo {
    fn fmt(&self) -> i32 {
        self.x
    }
}
`
	entities := ExtractEntities([]byte(src), "rust")
	seen := make(map[string]int)
	for _, e := range entities {
		seen[e.Kind+"::"+e.Name]++
	}
	for key, n := range seen {
		if n > 1 {
			t.Errorf("duplicate entity key %q (%d times) — snippet grouping would merge them", key, n)
		}
	}
	if seen["type::Foo"] != 1 {
		t.Errorf("struct Foo missing or duplicated (got %v)", entityNames(entities))
	}
	// The trait impl's method still resolves under the target type's name.
	found := false
	for _, e := range entities {
		if e.Name == "Foo.fmt" && e.Kind == "function" {
			found = true
		}
	}
	if !found {
		t.Errorf("Foo.fmt not extracted (got %v)", entityNames(entities))
	}
}

func TestExtractEntities_Go_BracelessDeclarations(t *testing.T) {
	// A declaration with no brace body ends on its own line; it must not
	// run on to the next brace block and swallow the item there.
	checkSpans(t, "go", `package p

type Celsius float64

func (c Celsius) String() string {
	return "c"
}

type Option func(*Config)

func WithHost(h string) Option {
	return nil
}

type Handler = http.Handler

func nanotime() int64

func Now() int64 {
	return nanotime()
}

func Long(
	a int,
	b int,
) error {
	return nil
}

type Set[
	T comparable,
] struct {
	m map[T]bool
}

type Pair[K comparable, V any] []V
`, map[string][2]int{
		"Celsius": {3, 3}, "String": {5, 7}, "Option": {9, 9}, "WithHost": {11, 13},
		"Handler": {15, 15}, "nanotime": {17, 17}, "Now": {19, 21},
		"Long": {23, 28}, "Set": {30, 34}, "Pair": {36, 36},
	})
}

func TestExtractEntities_Rust_BracelessDeclarations(t *testing.T) {
	checkSpans(t, "rust", `mod config;

pub struct Parser;

impl Parser {
    pub fn parse(&self) {}
}

pub struct Meters(pub f64);

pub type Result<T> = std::result::Result<T, Error>;

pub struct Pair(
    pub u32,
    pub u32,
);

extern "C" {
    fn abs(x: i32) -> i32;
}

pub struct Wrapper<T>
where
    T: Clone,
{
    inner: T,
}

fn measure<T>(x: T) -> T
where
    T: Clone,
{
    x
}
`, map[string][2]int{
		"config": {1, 1}, "Parser": {3, 3}, "impl Parser": {5, 7}, "Parser.parse": {6, 6},
		"Meters": {9, 9}, "Result": {11, 11}, "Pair": {13, 16}, "abs": {19, 19},
		"Wrapper": {22, 27}, "measure": {29, 34},
	})
}

func TestExtractEntities_Rust_ConstsAndQualifiedFns(t *testing.T) {
	checkSpans(t, "rust", `const A: u8 = 1;
const B: u8 = 2;
static GREETING: &str = "hi";

fn greet() {
    println!("{}", GREETING);
}

pub const fn new_id() -> Id {
    Id(0)
}

impl Id {
    pub const fn zero() -> Self {
        Id(0)
    }
    pub fn get(&self) -> u32 {
        self.0
    }
}

pub unsafe fn raw() {}
pub extern "C" fn cb() {}
async unsafe fn both() {}

const TABLE: [u8; 3] = [
    1,
    2,
    3,
];
static mut COUNTER: u32 = 0;

unsafe impl Send for Id {
    fn helper(&self) {}
}
const LAST: u8 = 9;`, map[string][2]int{
		"A": {1, 1}, "B": {2, 2}, "GREETING": {3, 3}, "greet": {5, 7},
		"new_id": {9, 11}, "Id.zero": {14, 16}, "Id.get": {17, 19},
		"raw": {22, 22}, "cb": {23, 23}, "both": {24, 24},
		"TABLE": {26, 30}, "COUNTER": {31, 31}, "Id.helper": {34, 34}, "LAST": {36, 36},
	})
}

func TestExtractEntities_StringsAndCommentsDoNotMoveBounds(t *testing.T) {
	checkSpans(t, "javascript", `const OPEN = '(';

function makeClient() {
  return new Client({ baseURL: 'https://api.example.com' });
}

const RE = /\[(\d+)/;

const COLORS = {
  warn: `+"`\\x1b[33m${'['}`"+`,
};

class Logger {
  log() {}
}
`, map[string][2]int{"OPEN": {1, 1}, "makeClient": {3, 5}, "RE": {7, 7}, "COLORS": {9, 11}, "Logger": {13, 15}})

	checkSpans(t, "python", `def color(hex="#fff"):
    return hex

SMILEY = ":)"
def nxt():
    pass

_MULTI_QUOTES = ('"""', "'''")

class Unparser:
    # use """ for docstrings
    def visit(self):
        return 1

def get_users():
    sql = """
SELECT *
FROM users
"""
    return sql
`, map[string][2]int{
		"color": {1, 3}, "SMILEY": {4, 4}, "nxt": {5, 7}, "_MULTI_QUOTES": {8, 8},
		"Unparser": {10, 14}, "Unparser.visit": {12, 14}, "get_users": {15, 21},
	})

	checkSpans(t, "go", `package p

import "fmt"
import "os" // why

type Mode int // see (docs

func (m Mode) String() string {
	return fmt.Sprint(int(m))
}

const tpl = `+"`"+`
func fake() {
}
`+"`"+`

var (
	a = f("x",
		"y")
	b = 2
)
`, map[string][2]int{"Mode": {6, 6}, "String": {8, 10}, "tpl": {12, 15}, "var": {17, 21}})

	checkSpans(t, "rust", `pub struct Marker; // zero-sized

impl Marker {
    fn new() -> Self { Marker }
}

mod config; // settings
const MAX_RETRIES: u32 = 3; // retry budget

fn main() {
    let s = r#"fn fake() {"#;
}
`, map[string][2]int{"Marker": {1, 1}, "Marker.new": {4, 4}, "config": {7, 7}, "MAX_RETRIES": {8, 8}, "main": {10, 12}})

	for _, tc := range []struct{ lang, src, name string }{
		{"go", "package p\n\nconst tpl = `\nfunc fake() {\n}\n`\n", "fake"},
		{"rust", "fn main() {\n    let s = r#\"\nfn fake() {\n\"#;\n}\n", "fake"},
	} {
		for _, e := range ExtractEntities([]byte(tc.src), tc.lang) {
			if e.Name == tc.name {
				t.Errorf("%s: text inside a string extracted as %q %d-%d", tc.lang, e.Name, e.StartLine, e.EndLine)
			}
		}
	}
}

func TestExtractEntities_ImportsWithoutTrailingText(t *testing.T) {
	// Masking blanks the path string; a bare `import` keyword is still an
	// import, and an unterminated side-effect import still ends the arrow
	// body above it.
	checkSpans(t, "go", "package p\n\nimport \"fmt\"\n\nfunc F() { fmt.Println() }\n",
		map[string][2]int{"imports": {3, 3}, "F": {5, 5}})
	checkSpans(t, "javascript", "import './polyfill'\n\nexport function f() {\n  return 1\n}\n",
		map[string][2]int{"imports": {1, 1}, "f": {3, 5}})
	checkSpans(t, "javascript", "const f = (x) =>\n  g(x)\nimport './late.css'\nfunction h() {}\n",
		map[string][2]int{"f": {1, 2}, "imports": {3, 3}, "h": {4, 4}})
}

func TestExtractEntities_JS_MaskedArrowBodies(t *testing.T) {
	// A template literal or a comment in a brace-less arrow body masks to a
	// blank line; only a truly blank line ends the body.
	checkSpans(t, "javascript", "const render = (x) =>\n  `<div>\n    ${x}\n  </div>`;\n\nconst f = (x) =>\n  // note\n  x + 1;\n",
		map[string][2]int{"render": {1, 4}, "f": {6, 8}})
}

func TestExtractEntities_TSX_TextDoesNotMoveBounds(t *testing.T) {
	// JSX text is not code: apostrophes, inch marks, URLs and self-closing
	// tags must not unbalance a component's braces.
	for _, line := range []string{
		`{error && <p className="error">Can't load: {formatError(error)}</p>}`,
		`<p>Don't panic. {formatError(error)}</p>`,
		`{fits && <p>Fits 12" laptops: {formatError(error)}</p>}`,
		`{items.map((i) => <Row key={i.id} />)}{formatError(error)}`,
		`{open && <Modal onClose={close} />}`,
		`{show && <a href="https://docs.example.com">https://docs.example.com</a>}`,
		`<span>{a} / {formatError(b)}</span>`,
		"<kbd>`</kbd> {formatError(error)}",
		`<p>Use /* for globs</p>`,
	} {
		src := `import React from "react";

export function Panel({ error, items, open, close }: Props) {
  return (
    <div>
      ` + line + `
      {items.map((i) =>
        <Row key={i.id}
        />)}
    </div>
  );
}

export function formatError(e: Error): string {
  return e.message;
}
`
		entities := ExtractEntities([]byte(src), "typescript")
		got := make(map[string][2]int)
		for _, e := range entities {
			got[e.Name] = [2]int{e.StartLine, e.EndLine}
		}
		if got["Panel"] != [2]int{3, 12} || got["formatError"] != [2]int{14, 16} {
			t.Errorf("line %s:\n  got %v, want Panel 3-12 and formatError 14-16", line, got)
		}
	}
}

func TestExtractEntities_Python_StringStatements(t *testing.T) {
	// A string-only statement masks to a blank line; it still ends the block
	// above it at its own indent.
	checkSpans(t, "python", `class A:
    def f(self):
        return 1

    """Section docstring."""

    def g(self):
        return 2

def top():
    return 1

'module string'

def nxt():
    x = \
"""text
"""
    return x

"abc".join([])
`, map[string][2]int{"A": {1, 9}, "A.f": {2, 4}, "A.g": {7, 9}, "top": {10, 12}, "nxt": {15, 20}})
	checkSpans(t, "python", "Q = '\"\"\"'\n\ndef f():\n    return 1\n\n\"\"\"Section.\"\"\"\n",
		map[string][2]int{"Q": {1, 1}, "f": {3, 5}})
}

func TestExtractEntities_Rust_LeadingStringIsNotADeclaration(t *testing.T) {
	// Macro input that opens with a string literal is not an item, though
	// masking leaves the words after the literal at the start of the line.
	src := "define_punctuation_structs! {\n    \"_\" pub struct Underscore/1 /// wildcard\n}\n\nimpl ToTokens for Underscore {\n    fn to_tokens(&self) {\n    }\n}\n"
	checkSpans(t, "rust", src, map[string][2]int{"impl ToTokens for Underscore": {5, 8}, "Underscore.to_tokens": {6, 7}})
	for _, e := range ExtractEntities([]byte(src), "rust") {
		if e.Name == "Underscore" {
			t.Errorf("macro input extracted as %s %q %d-%d", e.Kind, e.Name, e.StartLine, e.EndLine)
		}
	}
}

func TestExtractEntities_MultiLineDeclarations(t *testing.T) {
	checkSpans(t, "go", "package p\n\n"+`var defaultConfig = Config{
	Timeout: 5,
	Retries: 3,
}

var routes = map[string]string{
	"/": "home",
}

var (
	x = foo(
		1,
	)
	y = 2
)

const usage = `+"`"+`usage: astra
  --base REF
`+"`"+`

var reOpen = regexp.MustCompile(`+"`"+`^import\s*\($`+"`"+`)

var handler = func() {
	run()
}

func After() {}
`, map[string][2]int{
		"defaultConfig": {3, 6}, "routes": {8, 10}, "var": {12, 17}, "usage": {19, 21},
		"reOpen": {23, 23}, "handler": {25, 27}, "After": {29, 29},
	})

	checkSpans(t, "typescript", `export type Props = {
  title: string;
  count: number;
};

type Kind =
  | "a"
  | "b";

type Box<T> = {
  value: T
}
export function Card() {
  return null;
}
`, map[string][2]int{"Props": {1, 4}, "Kind": {6, 8}, "Box": {10, 12}, "Card": {13, 15}})
}

func TestExtractEntities_TS_AbstractClasses(t *testing.T) {
	checkSpans(t, "typescript", `export abstract class Repo<T> {
  abstract find(id: string): T;

  save(item: T): void {
    validate(item);
  }
}

abstract class Base {
  run() {}
}

export default abstract class Service {
  start() {}
}

class Users extends Repo<User> {
  protected override find(id: string): User {
    return load(id);
  }
}

const double = x =>
  x * 2
export abstract class Late {
  go() {}
}
`, map[string][2]int{
		"Repo": {1, 7}, "Repo.save": {4, 6}, "Base": {9, 11}, "Base.run": {10, 10},
		"Service": {13, 15}, "Service.start": {14, 14}, "Users.find": {18, 20},
		"double": {23, 24}, "Late": {25, 27}, "Late.go": {26, 26},
	})
}

func TestExtractEntities_Java_WrappedSignatures(t *testing.T) {
	src := `public class UserController {
    public ResponseEntity<UserDto> updateUser(
            Long id, UpdateUserRequest request) {
        return service.update(id, request);
    }

    public void load()
            throws IOException {
        read();
    }

    public void run()
    {
        go();
    }
}

interface UserRepository {
    Optional<User> findActiveByEmail(
            String email);

    default void touch() {
        save();
    }
}
`
	checkSpans(t, "java", src, map[string][2]int{
		"UserController.updateUser": {2, 5}, "UserController.load": {7, 10}, "UserController.run": {12, 15},
		"UserRepository.touch": {22, 24},
	})
	for _, e := range ExtractEntities([]byte(src), "java") {
		if e.Name == "UserRepository.findActiveByEmail" {
			t.Errorf("bodiless interface method extracted as %d-%d", e.StartLine, e.EndLine)
		}
	}
}

func TestExtractEntities_TSX_ArrowBodies(t *testing.T) {
	checkSpans(t, "typescript", `export const Row = ({ item }: RowProps) => (
  <li>
    {formatName(item)}
  </li>
);

export const label = ({ first, last }: Name) =>
  first + last;

const Typed = (p: { item: Item }) => (
  <li>{p.item}</li>
);

export const Fc: React.FC<RowProps> = ({ item }) => (
  <li>{item}</li>
);

export const useThing = ({ id }: P) =>
  useQuery({
    queryKey: ['thing', id],
    queryFn: () => fetchThing(id),
  });

const Note = ({ a }) => (
  <p>Note (see below</p>
);
export function After() {
  return null;
}

class List {
  renderRow = ({ item }: P) => (
    <li>{item}</li>
  );
  braced = ({ a }) => {
    return a;
  };
}
`, map[string][2]int{
		"Row": {1, 5}, "label": {7, 8}, "Typed": {10, 12}, "Fc": {14, 16}, "useThing": {18, 22},
		"Note": {24, 26}, "After": {27, 29}, "List.renderRow": {32, 34}, "List.braced": {35, 37},
	})
}

func TestExtractEntities_JS_FunctionBlocks(t *testing.T) {
	// A function declaration's body is the block after its parameter list,
	// however the signature wraps; an overload has no body.
	src := `export function merge<T>(a: T): T;
export function merge<T>(a: T, b: T): T;
/** Merges its arguments. */
export function merge(...args: any[]): any {
  return args[0];
}

async function getTrustedRoot(
/* istanbul ignore next */
options = {}) {
  const client = createClient(options);
  return client;
}

function defer(fn)
{
  run(fn);
}

function build(
  a,
  b = {},
) {
  return a;
}

function shape(): { a: string } {
  return { a: "" };
}
`
	checkSpans(t, "typescript", src, map[string][2]int{
		"getTrustedRoot": {8, 13}, "defer": {15, 18}, "build": {20, 25}, "shape": {27, 29},
	})
	var merges [][2]int
	for _, e := range ExtractEntities([]byte(src), "typescript") {
		if e.Name == "merge" {
			merges = append(merges, [2]int{e.StartLine, e.EndLine})
		}
		if e.Name == "client" {
			t.Errorf("function body line extracted as %s %q", e.Kind, e.Name)
		}
	}
	if len(merges) != 3 || merges[0] != [2]int{1, 1} || merges[1] != [2]int{2, 2} || merges[2][1] != 6 {
		t.Errorf("merge spans = %v, want 1-1, 2-2 and the implementation ending at 6", merges)
	}
}

func TestBuildSnippets_TSX_ComponentsKeepTheirBodies(t *testing.T) {
	// Edits inside two arrow components' JSX bodies stay in two named
	// snippets, and their calls make edges.
	src := "export const Row = ({ item }: P) => (\n  <li>{formatName(item)}</li>\n);\n\nexport const Footer = ({ count }: Q) => (\n  <p>{formatCount(count)}</p>\n);\n\nexport function formatName(i: Item) {\n  return i.name;\n}\n\nexport function formatCount(n: number) {\n  return String(n);\n}\n"
	f := &FileDiff{Path: "src/List.tsx", OldPath: "src/List.tsx", Language: "typescript", Source: []byte(src), Hunks: []Hunk{
		{OldStart: 2, OldLines: 1, NewStart: 2, NewLines: 1, Lines: []string{"@@ -2,1 +2,1 @@", "-  <li>{item.name}</li>", "+  <li>{formatName(item)}</li>"}},
		{OldStart: 6, OldLines: 1, NewStart: 6, NewLines: 1, Lines: []string{"@@ -6,1 +6,1 @@", "-  <p>{count}</p>", "+  <p>{formatCount(count)}</p>"}},
		{OldStart: 10, OldLines: 1, NewStart: 10, NewLines: 1, Lines: []string{"@@ -10,1 +10,1 @@", "-  return i.title;", "+  return i.name;"}},
		{OldStart: 14, OldLines: 1, NewStart: 14, NewLines: 1, Lines: []string{"@@ -14,1 +14,1 @@", "-  return `${n}`;", "+  return String(n);"}},
	}}
	snippets := BuildSnippets([]*FileDiff{f})
	byID := make(map[string]string)
	for _, s := range snippets {
		byID[s.ID] = s.SymbolPath
	}
	var edges []string
	for _, e := range BuildCallEdges(snippets) {
		edges = append(edges, byID[e.From]+"->"+byID[e.To])
	}
	if got := strings.Join(symbolPaths(snippets), " "); got != "Row Footer formatName formatCount" {
		t.Errorf("snippets = %s, want Row Footer formatName formatCount", got)
	}
	if got := strings.Join(edges, " "); got != "Row->formatName Footer->formatCount" && got != "Footer->formatCount Row->formatName" {
		t.Errorf("edges = %s, want Row->formatName and Footer->formatCount", got)
	}
}

func TestBuildSnippets_MultiLineVarMemberChange(t *testing.T) {
	// A change inside a multi-line initializer belongs to the var, and its
	// users get a reference edge to it.
	src := "package p\n\nvar defaultConfig = Config{\n\tTimeout: 10,\n}\n\nfunc New() Config {\n\treturn defaultConfig\n}\n"
	f := &FileDiff{Path: "c.go", OldPath: "c.go", Language: "go", Source: []byte(src), Hunks: []Hunk{
		{OldStart: 4, OldLines: 1, NewStart: 4, NewLines: 1, Lines: []string{"@@ -4,1 +4,1 @@", "-\tTimeout: 5,", "+\tTimeout: 10,"}},
		{OldStart: 8, OldLines: 1, NewStart: 8, NewLines: 1, Lines: []string{"@@ -8,1 +8,1 @@", "-\treturn Config{}", "+\treturn defaultConfig"}},
	}}
	snippets := BuildSnippets([]*FileDiff{f})
	if got := symbolPaths(snippets); len(got) != 2 || got[0] != "defaultConfig" || got[1] != "New" {
		t.Fatalf("snippets = %v, want [defaultConfig New]", got)
	}
	edges := BuildCallEdges(snippets)
	if len(edges) != 1 || edges[0].From != snippets[1].ID || edges[0].To != snippets[0].ID {
		t.Errorf("edges = %v, want New -> defaultConfig", edges)
	}
}

// ---------------------------------------------------------------------------
// Unsupported language returns nil
// ---------------------------------------------------------------------------

func TestExtractEntities_Unsupported(t *testing.T) {
	if entities := ExtractEntities([]byte("hello"), "cobol"); entities != nil {
		t.Errorf("expected nil for unsupported language, got %v", entities)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func entityNames(entities []Entity) []string {
	names := make([]string, len(entities))
	for i, e := range entities {
		names[i] = e.Kind + ":" + e.Name
	}
	return names
}

// checkSpans asserts that src yields each named entity with exactly the
// given [start, end] lines.
func checkSpans(t *testing.T, lang, src string, want map[string][2]int) {
	t.Helper()
	entities := ExtractEntities([]byte(src), lang)
	got := make(map[string][2]int, len(entities))
	for _, e := range entities {
		got[e.Name] = [2]int{e.StartLine, e.EndLine}
	}
	for name, span := range want {
		if g, ok := got[name]; !ok {
			t.Errorf("%s: entity %q missing (got %v)", lang, name, entityNames(entities))
		} else if g != span {
			t.Errorf("%s: %q spans %d-%d, want %d-%d", lang, name, g[0], g[1], span[0], span[1])
		}
	}
}
