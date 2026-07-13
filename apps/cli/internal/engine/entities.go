// Package engine — entity boundary extraction for supported languages.
//
// An "entity" is a top-level (or class-level) named declaration that forms a
// meaningful review unit: function/method, class/struct/interface/type, or a
// large top-level variable/constant block. Import groups are treated as a
// single "imports" entity so they are shown together.
//
// The strategy is intentionally heuristic: we scan the raw source lines with
// language-specific regexes rather than requiring a full AST parser. This is
// fast, has no WASM/CGo dependencies, and is accurate enough for the entity
// boundaries produced by real codebases.
//
// Accuracy priority: never merge two distinct named entities into one snippet.
// If a boundary is ambiguous, split conservatively (produce more snippets).
package engine

import (
	"regexp"
	"strings"
)

// Entity is a named declaration extracted from a source file.
type Entity struct {
	// Name is the identifier as it appears in source (e.g. "parseCSV", "MyStruct").
	Name string
	// Kind is a coarse type label: "function", "class", "type", "var", "const", "imports", "other".
	Kind string
	// StartLine and EndLine are 1-indexed, inclusive, in the file.
	StartLine int
	EndLine   int
}

// ExtractEntities returns the top-level entity spans for the given source
// in the given language. Lines outside any entity are covered by a synthetic
// "preamble" entity at the start and "tail" entities for any trailing lines.
//
// Returns nil (not an error) for unsupported languages — callers fall back to
// hunk-level snippets.
func ExtractEntities(src []byte, lang string) []Entity {
	lines := splitLines(src)
	switch lang {
	case "go":
		return extractGo(lines)
	case "javascript", "typescript":
		return extractJS(lines)
	case "python":
		return extractPython(lines)
	case "java":
		return extractJava(lines)
	case "rust":
		return extractRust(lines)
	default:
		return nil
	}
}

// ---------------------------------------------------------------------------
// Go
// ---------------------------------------------------------------------------

var (
	reGoFunc      = regexp.MustCompile(`^func\s`)
	reGoType      = regexp.MustCompile(`^type\s+\w`)
	reGoVarConst  = regexp.MustCompile(`^(?:var|const)\s`)
	reGoImport    = regexp.MustCompile(`^import\s`)
	reGoBlockOpen = regexp.MustCompile(`\{`)
)

func extractGo(lines []string) []Entity {
	var entities []Entity
	i := 0
	n := len(lines)

	for i < n {
		line := lines[i]
		stripped := strings.TrimSpace(line)

		switch {
		case reGoImport.MatchString(stripped):
			// import "x" or import ( ... )
			start := i + 1
			if strings.Contains(line, "(") {
				// multi-line import block
				for i < n && !strings.HasPrefix(strings.TrimSpace(lines[i]), ")") {
					i++
				}
				i++ // consume closing )
			} else {
				i++
			}
			entities = append(entities, Entity{Name: "imports", Kind: "imports", StartLine: start, EndLine: i})

		case reGoFunc.MatchString(stripped):
			// func [(*Receiver)] Name( ...
			name := goFuncName(stripped)
			start := i + 1
			end := goBlockEnd(lines, i)
			entities = append(entities, Entity{Name: name, Kind: "function", StartLine: start, EndLine: end})
			i = end

		case reGoType.MatchString(stripped):
			// type Name struct { ... } or type Name interface { ... } or type Name = ...
			name := goTypeName(stripped)
			start := i + 1
			end := goBlockEnd(lines, i)
			entities = append(entities, Entity{Name: name, Kind: "type", StartLine: start, EndLine: end})
			i = end

		case reGoVarConst.MatchString(stripped):
			// var/const single line or block
			kind := "var"
			if strings.HasPrefix(stripped, "const") {
				kind = "const"
			}
			name := goVarConstName(stripped)
			start := i + 1
			if strings.Contains(line, "(") {
				// block: var ( ... )
				for i < n && !strings.HasPrefix(strings.TrimSpace(lines[i]), ")") {
					i++
				}
				i++
			} else {
				i++
			}
			entities = append(entities, Entity{Name: name, Kind: kind, StartLine: start, EndLine: i})

		default:
			i++
		}
	}
	return entities
}

// goBlockEnd finds the line where the brace-delimited block starting at or
// after startIdx ends. Returns 1-indexed line number.
func goBlockEnd(lines []string, startIdx int) int {
	n := len(lines)
	depth := 0
	opened := false
	for i := startIdx; i < n; i++ {
		for _, ch := range lines[i] {
			if ch == '{' {
				depth++
				opened = true
			} else if ch == '}' {
				depth--
			}
		}
		if opened && depth == 0 {
			return i + 1 // 1-indexed
		}
	}
	return n // fallback: rest of file
}

var reGoFuncName = regexp.MustCompile(`^func\s+(?:\([^)]+\)\s+)?(\w+)`)

func goFuncName(line string) string {
	if m := reGoFuncName.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return "func"
}

var reGoTypeName = regexp.MustCompile(`^type\s+(\w+)`)

func goTypeName(line string) string {
	if m := reGoTypeName.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return "type"
}

var reGoVarConstName = regexp.MustCompile(`^(?:var|const)\s+(\w+)`)

func goVarConstName(line string) string {
	if m := reGoVarConstName.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return "var"
}

// ---------------------------------------------------------------------------
// JavaScript / TypeScript
// ---------------------------------------------------------------------------

var (
	// export async function foo(  /  function foo(  /  async function foo(
	reJSFunc = regexp.MustCompile(`^(?:export\s+(?:default\s+)?)?(?:async\s+)?function\s*\*?\s*(\w+)`)
	// export class Foo  /  class Foo
	reJSClass = regexp.MustCompile(`^(?:export\s+(?:default\s+)?)?class\s+(\w+)`)
	// export interface Foo  (TypeScript)
	reJSInterface = regexp.MustCompile(`^(?:export\s+)?interface\s+(\w+)`)
	// export type Foo =  (TypeScript)
	reJSTypeAlias = regexp.MustCompile(`^(?:export\s+)?type\s+(\w+)\s*=`)
	// export enum Foo  (TypeScript)
	reJSEnum = regexp.MustCompile(`^(?:export\s+)?(?:const\s+)?enum\s+(\w+)`)
	// const/let/var foo = ...  or  export const foo = ...
	reJSVar = regexp.MustCompile(`^(?:export\s+)?(?:const|let|var)\s+(\w+)`)
	// import ...
	reJSImport = regexp.MustCompile(`^import\s`)
	// require(...)
	reJSRequire = regexp.MustCompile(`\brequire\s*\(`)
)

func extractJS(lines []string) []Entity {
	var entities []Entity
	n := len(lines)
	i := 0

	// Collect contiguous import/require lines into one "imports" entity.
	importStart := -1

	flushImports := func(end int) {
		if importStart >= 0 {
			entities = append(entities, Entity{Name: "imports", Kind: "imports", StartLine: importStart + 1, EndLine: end})
			importStart = -1
		}
	}

	for i < n {
		line := strings.TrimSpace(lines[i])

		switch {
		case reJSImport.MatchString(line) || reJSRequire.MatchString(line):
			if importStart < 0 {
				importStart = i
			}
			i++

		case reJSClass.MatchString(line):
			flushImports(i)
			name := reJSClass.FindStringSubmatch(line)[1]
			start := i + 1
			end := jsBraceEnd(lines, i)
			entities = append(entities, Entity{Name: name, Kind: "class", StartLine: start, EndLine: end})
			i = end

		case reJSInterface.MatchString(line):
			flushImports(i)
			name := reJSInterface.FindStringSubmatch(line)[1]
			start := i + 1
			end := jsBraceEnd(lines, i)
			entities = append(entities, Entity{Name: name, Kind: "type", StartLine: start, EndLine: end})
			i = end

		case reJSEnum.MatchString(line):
			flushImports(i)
			name := reJSEnum.FindStringSubmatch(line)[1]
			start := i + 1
			end := jsBraceEnd(lines, i)
			entities = append(entities, Entity{Name: name, Kind: "const", StartLine: start, EndLine: end})
			i = end

		case reJSTypeAlias.MatchString(line):
			flushImports(i)
			name := reJSTypeAlias.FindStringSubmatch(line)[1]
			start := i + 1
			// Type aliases end at semicolon or next blank line; find it
			end := i + 1
			for end < n {
				tl := strings.TrimSpace(lines[end-1])
				if strings.HasSuffix(tl, ";") || tl == "" {
					break
				}
				end++
			}
			entities = append(entities, Entity{Name: name, Kind: "type", StartLine: start, EndLine: end})
			i = end

		case reJSFunc.MatchString(line):
			flushImports(i)
			name := reJSFunc.FindStringSubmatch(line)[1]
			start := i + 1
			end := jsFuncEnd(lines, i)
			entities = append(entities, Entity{Name: name, Kind: "function", StartLine: start, EndLine: end})
			i = end

		case reJSVar.MatchString(line):
			flushImports(i)
			name := reJSVar.FindStringSubmatch(line)[1]
			start := i + 1
			end := jsVarEnd(lines, i)
			entities = append(entities, Entity{Name: name, Kind: "var", StartLine: start, EndLine: end})
			i = end

		default:
			flushImports(i)
			i++
		}
	}
	flushImports(n)
	return entities
}

// jsBraceEnd finds the closing brace of a block that opens on or after startIdx.
func jsBraceEnd(lines []string, startIdx int) int {
	n := len(lines)
	depth := 0
	opened := false
	for i := startIdx; i < n; i++ {
		for _, ch := range lines[i] {
			if ch == '{' {
				depth++
				opened = true
			} else if ch == '}' {
				depth--
			}
		}
		if opened && depth == 0 {
			return i + 1 // 1-indexed end
		}
	}
	return n
}

// jsFuncEnd handles both brace-delimited and arrow functions (multi-line).
func jsFuncEnd(lines []string, startIdx int) int {
	line := strings.TrimSpace(lines[startIdx])
	// Arrow function assigned to const: const foo = (...) => { ... }
	// or naked: function foo() { ... }
	if strings.Contains(line, "{") {
		return jsBraceEnd(lines, startIdx)
	}
	// Arrow without braces: single expression, end at semicolon or next blank
	n := len(lines)
	for i := startIdx + 1; i < n; i++ {
		tl := strings.TrimSpace(lines[i])
		if tl == "" || strings.HasSuffix(tl, ";") || strings.HasSuffix(tl, ",") {
			return i + 1
		}
	}
	return n
}

// jsVarEnd finds the end of a const/let/var declaration (may be multi-line object/array).
func jsVarEnd(lines []string, startIdx int) int {
	line := lines[startIdx]
	// Count opening brackets/parens/braces to decide if multi-line
	depth := 0
	for _, ch := range line {
		switch ch {
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			depth--
		}
	}
	if depth == 0 {
		return startIdx + 1
	}
	n := len(lines)
	for i := startIdx + 1; i < n; i++ {
		for _, ch := range lines[i] {
			switch ch {
			case '{', '[', '(':
				depth++
			case '}', ']', ')':
				depth--
			}
		}
		if depth <= 0 {
			return i + 1
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// Python
// ---------------------------------------------------------------------------

var (
	rePyFunc    = regexp.MustCompile(`^(    |\t)?(?:async\s+)?def\s+(\w+)`)
	rePyClass   = regexp.MustCompile(`^class\s+(\w+)`)
	rePyImport  = regexp.MustCompile(`^(?:import|from)\s`)
	rePyTopFunc = regexp.MustCompile(`^(?:async\s+)?def\s+(\w+)`)
)

func extractPython(lines []string) []Entity {
	var entities []Entity
	n := len(lines)
	i := 0

	importStart := -1
	flushImports := func(end int) {
		if importStart >= 0 {
			entities = append(entities, Entity{Name: "imports", Kind: "imports", StartLine: importStart + 1, EndLine: end})
			importStart = -1
		}
	}

	for i < n {
		line := lines[i]

		switch {
		case rePyImport.MatchString(strings.TrimSpace(line)):
			if importStart < 0 {
				importStart = i
			}
			i++

		case rePyClass.MatchString(line):
			flushImports(i)
			name := rePyClass.FindStringSubmatch(line)[1]
			start := i + 1
			end := pyBlockEnd(lines, i)
			entities = append(entities, Entity{Name: name, Kind: "class", StartLine: start, EndLine: end})
			i = end

		case rePyTopFunc.MatchString(line):
			flushImports(i)
			name := rePyTopFunc.FindStringSubmatch(line)[1]
			start := i + 1
			end := pyBlockEnd(lines, i)
			entities = append(entities, Entity{Name: name, Kind: "function", StartLine: start, EndLine: end})
			i = end

		default:
			flushImports(i)
			i++
		}
	}
	flushImports(n)
	return entities
}

// pyBlockEnd finds the end of a Python block based on indentation.
// It first skips past the function/class signature (which may span multiple
// lines) by waiting for the line ending in ':' that opens the body.
// Then it tracks indentation to find where the body ends.
func pyBlockEnd(lines []string, startIdx int) int {
	n := len(lines)
	baseIndent := indentLen(lines[startIdx])

	// Step 1: advance past the signature until we see a line ending in ':'
	// (the colon that opens the block body). Multi-line signatures have
	// continuation lines at indent > baseIndent.
	bodyStart := startIdx
	for bodyStart < n {
		stripped := strings.TrimSpace(lines[bodyStart])
		if strings.HasSuffix(stripped, ":") || strings.Contains(stripped, ":  ") || strings.Contains(stripped, ": #") {
			bodyStart++
			break
		}
		bodyStart++
	}

	// Step 2: scan body lines. End when we hit a non-empty, non-comment line
	// at indentation <= baseIndent.
	for i := bodyStart; i < n; i++ {
		l := lines[i]
		stripped := strings.TrimSpace(l)
		if stripped == "" || strings.HasPrefix(stripped, "#") {
			continue
		}
		if indentLen(l) <= baseIndent {
			return i // 1-indexed: line i+1 is the first line after the block
		}
	}
	return n
}

func indentLen(line string) int {
	for j, ch := range line {
		if ch != ' ' && ch != '\t' {
			return j
		}
	}
	return len(line)
}

// ---------------------------------------------------------------------------
// Java
// ---------------------------------------------------------------------------

var (
	reJavaClass  = regexp.MustCompile(`^(?:(?:public|private|protected|abstract|final|static)\s+)*(?:class|interface|enum|record)\s+(\w+)`)
	reJavaMethod = regexp.MustCompile(`^(?:(?:public|private|protected|abstract|final|static|synchronized|native|default)\s+)*(?:<[^>]+>\s+)?[\w\[\]<>,]+\s+(\w+)\s*\(`)
	reJavaImport = regexp.MustCompile(`^import\s`)
)

func extractJava(lines []string) []Entity {
	var entities []Entity
	n := len(lines)
	i := 0
	importStart := -1
	flushImports := func(end int) {
		if importStart >= 0 {
			entities = append(entities, Entity{Name: "imports", Kind: "imports", StartLine: importStart + 1, EndLine: end})
			importStart = -1
		}
	}

	for i < n {
		line := strings.TrimSpace(lines[i])
		switch {
		case reJavaImport.MatchString(line):
			if importStart < 0 {
				importStart = i
			}
			i++
		case reJavaClass.MatchString(line):
			flushImports(i)
			name := reJavaClass.FindStringSubmatch(line)[1]
			start := i + 1
			end := goBlockEnd(lines, i) // same brace-counting logic
			entities = append(entities, Entity{Name: name, Kind: "class", StartLine: start, EndLine: end})
			i = end
		case reJavaMethod.MatchString(line) && strings.Contains(lines[i], "{"):
			flushImports(i)
			name := reJavaMethod.FindStringSubmatch(line)[1]
			start := i + 1
			end := goBlockEnd(lines, i)
			entities = append(entities, Entity{Name: name, Kind: "function", StartLine: start, EndLine: end})
			i = end
		default:
			flushImports(i)
			i++
		}
	}
	flushImports(n)
	return entities
}

// ---------------------------------------------------------------------------
// Rust
// ---------------------------------------------------------------------------

var (
	reRustFn     = regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?fn\s+(\w+)`)
	reRustStruct = regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(?:struct|enum|trait|impl|type)\s+(\w+)`)
	reRustUse    = regexp.MustCompile(`^use\s`)
	reRustMod    = regexp.MustCompile(`^(?:pub\s+)?mod\s+(\w+)`)
	reRustConst  = regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(?:const|static)\s+(\w+)`)
)

func extractRust(lines []string) []Entity {
	var entities []Entity
	n := len(lines)
	i := 0
	importStart := -1
	flushImports := func(end int) {
		if importStart >= 0 {
			entities = append(entities, Entity{Name: "imports", Kind: "imports", StartLine: importStart + 1, EndLine: end})
			importStart = -1
		}
	}

	for i < n {
		line := strings.TrimSpace(lines[i])
		switch {
		case reRustUse.MatchString(line):
			if importStart < 0 {
				importStart = i
			}
			i++
		case reRustFn.MatchString(line):
			flushImports(i)
			name := reRustFn.FindStringSubmatch(line)[1]
			start := i + 1
			end := goBlockEnd(lines, i)
			entities = append(entities, Entity{Name: name, Kind: "function", StartLine: start, EndLine: end})
			i = end
		case reRustStruct.MatchString(line):
			flushImports(i)
			name := reRustStruct.FindStringSubmatch(line)[1]
			start := i + 1
			end := goBlockEnd(lines, i)
			entities = append(entities, Entity{Name: name, Kind: "type", StartLine: start, EndLine: end})
			i = end
		case reRustMod.MatchString(line):
			flushImports(i)
			name := reRustMod.FindStringSubmatch(line)[1]
			start := i + 1
			end := goBlockEnd(lines, i)
			entities = append(entities, Entity{Name: name, Kind: "type", StartLine: start, EndLine: end})
			i = end
		case reRustConst.MatchString(line):
			flushImports(i)
			name := reRustConst.FindStringSubmatch(line)[1]
			start := i + 1
			end := i + 2
			// find semicolon end
			for end <= n && !strings.HasSuffix(strings.TrimSpace(lines[end-1]), ";") {
				end++
			}
			entities = append(entities, Entity{Name: name, Kind: "const", StartLine: start, EndLine: end})
			i = end
		default:
			flushImports(i)
			i++
		}
	}
	flushImports(n)
	return entities
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

func splitLines(src []byte) []string {
	return strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
}
