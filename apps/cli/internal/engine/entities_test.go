package engine

import (
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
