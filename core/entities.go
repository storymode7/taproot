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
package core

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
	reGoFunc          = regexp.MustCompile(`^func\s`)
	reGoType          = regexp.MustCompile(`^type\s+\w`)
	reGoVarConst      = regexp.MustCompile(`^(?:var|const)\s`)
	reGoImport        = regexp.MustCompile(`^import\s`)
	reGoBlockOpen     = regexp.MustCompile(`\{`)
	reGoImportGroup   = regexp.MustCompile(`^import\s*\($`)
	reGoVarConstGroup = regexp.MustCompile(`^(?:var|const)\s*\($`)
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
			if reGoImportGroup.MatchString(stripped) {
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
			if reGoVarConstGroup.MatchString(stripped) {
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
		d, o := braceDelta(lines[i])
		depth += d
		opened = opened || o
		if opened && depth == 0 {
			return i + 1 // 1-indexed
		}
	}
	return n // fallback: rest of file
}

// braceDelta counts one line's net {/} balance, ignoring braces inside
// string/char literals and // line comments. Double-quoted and backtick
// strings are skipped whole (with \ escapes); a single quote only opens a
// literal when it closes within two characters ('{', '\n'), so Rust
// lifetimes ('a) don't swallow the rest of the line. Multi-line strings,
// block comments, and long single-quoted JS strings remain the tolerated
// blind spots of this heuristic.
func braceDelta(line string) (delta int, opened bool) {
	runes := []rune(line)
	n := len(runes)
	for i := 0; i < n; i++ {
		switch runes[i] {
		case '/':
			if i+1 < n && runes[i+1] == '/' {
				return delta, opened // line comment
			}
		case '"', '`':
			q := runes[i]
			for i++; i < n && runes[i] != q; i++ {
				if runes[i] == '\\' {
					i++
				}
			}
		case '\'':
			if i+2 < n && runes[i+1] != '\\' && runes[i+2] == '\'' {
				i += 2 // 'x'
			} else if i+3 < n && runes[i+1] == '\\' && runes[i+3] == '\'' {
				i += 3 // '\n'
			}
			// otherwise: bare quote (lifetime, apostrophe) — not a literal
		case '{':
			delta++
			opened = true
		case '}':
			delta--
		}
	}
	return delta, opened
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
	// function-valued initializer: = [async] (…) =>  /  = [async] ident =>  /  = [async] function
	// (multi-line parameter lists before the arrow are not recognized)
	reJSFuncVal = regexp.MustCompile(`=\s*(?:async\s+)?(?:function\b|\(.*\)\s*=>|[A-Za-z_$][\w$]*\s*=>)`)
	// method inside a class body: [static async get set ...] [*] name[<T>](
	// (generics allow one nesting level: <T extends Base<K>>)
	reJSMethod = regexp.MustCompile(`^(?:(?:static|async|get|set|public|private|protected|readonly)\s+)*\*?\s*(#?[A-Za-z_$][\w$]*)\s*(?:<[^<>]*(?:<[^<>]*>[^<>]*)*>)?\s*\(`)
	// class-property arrow: [modifiers] name = [async] (…) =>
	reJSPropArrow = regexp.MustCompile(`^(?:(?:static|public|private|protected|readonly)\s+)*(#?[A-Za-z_$][\w$]*)\s*=\s*(?:async\s+)?\(.*\)\s*=>`)
	// start of a new top-level declaration — used to stop the no-brace
	// arrow-body scan in semicolon-less code
	reJSDeclStart = regexp.MustCompile(`^(?:export\s+(?:default\s+)?)?(?:async\s+)?(?:function\b|class\s|interface\s|enum\s|const\s|let\s|var\s|type\s+\w+\s*=|import\s)`)
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
			entities = append(entities, jsClassMethods(lines, i, end, name)...)
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
			if reJSFuncVal.MatchString(line) {
				// function-valued const/let/var: arrow or function expression
				end := jsVarEnd(lines, i)
				if strings.Contains(line, "{") || strings.HasSuffix(line, "=>") {
					end = jsFuncEnd(lines, i)
				}
				entities = append(entities, Entity{Name: name, Kind: "function", StartLine: start, EndLine: end})
				i = end
			} else {
				end := jsVarEnd(lines, i)
				entities = append(entities, Entity{Name: name, Kind: "var", StartLine: start, EndLine: end})
				i = end
			}

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
		d, o := braceDelta(lines[i])
		depth += d
		opened = opened || o
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
	// Arrow without braces: single expression, end at semicolon or next blank.
	// In semicolon-less code no terminator ever comes, so also stop before a
	// line that starts a new declaration or method — never merge two entities.
	// When the arrow line ends in "=>" the next line is the expression body
	// and is exempt from the method check (a call like `wrap(x)` looks like
	// a method signature).
	firstBody := startIdx
	if strings.HasSuffix(line, "=>") {
		firstBody = startIdx + 1
	}
	n := len(lines)
	for i := startIdx + 1; i < n; i++ {
		tl := strings.TrimSpace(lines[i])
		if tl == "" || strings.HasSuffix(tl, ";") || strings.HasSuffix(tl, ",") {
			return i + 1
		}
		if reJSDeclStart.MatchString(tl) {
			return i
		}
		if i > firstBody && jsMethodBoundary(tl) {
			return i
		}
	}
	return n
}

// jsMethodBoundary reports whether a trimmed line begins a new class member:
// a method signature opening its body brace, or an arrow property. The brace
// requirement keeps expression continuations like `.then(r)` from matching.
func jsMethodBoundary(tl string) bool {
	if reJSPropArrow.MatchString(tl) {
		return true
	}
	return reJSMethod.MatchString(tl) && strings.HasSuffix(tl, "{")
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

// classBodyLines invokes fn for every line index strictly inside the brace
// block opening at startIdx whose brace depth at line start (relative to the
// block) is exactly 1 — i.e. the direct body of a class/impl block.
// endLine is the 1-indexed last line of the block.
func classBodyLines(lines []string, startIdx, endLine int, fn func(j int)) {
	depth := 0
	opened := false
	for j := startIdx; j < endLine && j < len(lines); j++ {
		atStart := depth
		d, o := braceDelta(lines[j])
		depth += d
		opened = opened || o
		if j > startIdx && opened && atStart == 1 {
			fn(j)
		}
	}
}

// jsClassMethods extracts methods (and class-property arrows) declared
// directly inside a class body as "Class.method" entities. Method spans are
// clamped to the class end so they never leak past it.
func jsClassMethods(lines []string, classIdx, classEnd int, className string) []Entity {
	var methods []Entity
	classBodyLines(lines, classIdx, classEnd, func(j int) {
		tl := strings.TrimSpace(lines[j])
		var name string
		multiLineSig := false
		if m := reJSMethod.FindStringSubmatch(tl); m != nil {
			name = m[1]
			// Prettier-style multi-line parameter list: the body brace is on
			// a later line, so balance braces from the signature onward.
			multiLineSig = !strings.Contains(lines[j], "{") &&
				!strings.Contains(lines[j], "=>") && !strings.HasSuffix(tl, ";")
		} else if m := reJSPropArrow.FindStringSubmatch(tl); m != nil {
			name = m[1]
		} else {
			return
		}
		end := jsFuncEnd(lines, j)
		if multiLineSig {
			end = jsBraceEnd(lines, j)
		}
		if !strings.Contains(lines[j], "{") && (strings.HasSuffix(tl, ";") || strings.HasSuffix(tl, ",")) {
			end = j + 1 // single-line arrow property ends on its own line
		}
		if end > classEnd {
			end = classEnd
		}
		methods = append(methods, Entity{Name: className + "." + name, Kind: "function", StartLine: j + 1, EndLine: end})
	})
	return methods
}

// ---------------------------------------------------------------------------
// Python
// ---------------------------------------------------------------------------

var (
	rePyFunc    = regexp.MustCompile(`^(    |\t)?(?:async\s+)?def\s+(\w+)`)
	rePyClass   = regexp.MustCompile(`^class\s+(\w+)`)
	rePyImport  = regexp.MustCompile(`^(?:import|from)\s`)
	rePyTopFunc = regexp.MustCompile(`^(?:async\s+)?def\s+(\w+)`)
	rePyMethod  = regexp.MustCompile(`^\s*(?:async\s+)?def\s+(\w+)`)
	// module-level assignment: NAME[: annotation] = value ('=' not '==';
	// tuple unpacking and augmented assignment excluded)
	rePyAssign    = regexp.MustCompile(`^([A-Za-z_]\w*)\s*(?::[^=]*)?=(?:[^=]|$)`)
	rePyConstName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
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

	inString := pyTripleStringMask(lines)

	for i < n {
		line := lines[i]

		switch {
		case inString[i]:
			// Inside a triple-quoted string (docstring / multi-line literal):
			// assignment- or def-looking text in there is prose, not code.
			flushImports(i)
			i++

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
			entities = append(entities, pyClassMethods(lines, i, end, name)...)
			i = end

		case rePyTopFunc.MatchString(line):
			flushImports(i)
			name := rePyTopFunc.FindStringSubmatch(line)[1]
			start := i + 1
			end := pyBlockEnd(lines, i)
			entities = append(entities, Entity{Name: name, Kind: "function", StartLine: start, EndLine: end})
			i = end

		case indentLen(line) == 0 && rePyAssign.MatchString(line):
			flushImports(i)
			name := rePyAssign.FindStringSubmatch(line)[1]
			kind := "var"
			if rePyConstName.MatchString(name) {
				kind = "const"
			}
			start := i + 1
			end := jsVarEnd(lines, i) // bracket-balance handles multi-line values
			entities = append(entities, Entity{Name: name, Kind: kind, StartLine: start, EndLine: end})
			i = end

		default:
			flushImports(i)
			i++
		}
	}
	flushImports(n)
	return entities
}

// pyTripleStringMask marks lines that start inside a triple-quoted string
// (module docstring or multi-line literal), so the extractor never reads
// assignment- or def-looking prose inside one as an entity. The opening
// line itself is not masked.
func pyTripleStringMask(lines []string) []bool {
	mask := make([]bool, len(lines))
	delim := "" // "" = not inside a string
	for i, l := range lines {
		mask[i] = delim != ""
		rest := l
		for {
			if delim == "" {
				dq := strings.Index(rest, `"""`)
				sq := strings.Index(rest, `'''`)
				if dq < 0 && sq < 0 {
					break
				}
				if dq >= 0 && (sq < 0 || dq < sq) {
					delim, rest = `"""`, rest[dq+3:]
				} else {
					delim, rest = `'''`, rest[sq+3:]
				}
			} else {
				idx := strings.Index(rest, delim)
				if idx < 0 {
					break
				}
				rest = rest[idx+3:]
				delim = ""
			}
		}
	}
	return mask
}

// pyClassMethods extracts defs directly inside a class body as
// "Class.method" entities. The shallowest def indent in the body defines
// the method indent (a nested class's deeper defs must not lock it);
// deeper defs are absorbed by their method's span. Contiguous decorator
// lines belong to the method they decorate. Method spans are clamped to
// the class end.
func pyClassMethods(lines []string, classIdx, classEnd int, className string) []Entity {
	methodIndent := -1
	for j := classIdx + 1; j < classEnd && j < len(lines); j++ {
		if rePyMethod.MatchString(lines[j]) {
			if ind := indentLen(lines[j]); methodIndent < 0 || ind < methodIndent {
				methodIndent = ind
			}
		}
	}
	if methodIndent < 0 {
		return nil
	}

	var methods []Entity
	for j := classIdx + 1; j < classEnd && j < len(lines); j++ {
		m := rePyMethod.FindStringSubmatch(lines[j])
		if m == nil || indentLen(lines[j]) != methodIndent {
			continue
		}
		start := j
		for start > classIdx+1 && indentLen(lines[start-1]) == methodIndent &&
			strings.HasPrefix(strings.TrimSpace(lines[start-1]), "@") {
			start--
		}
		end := pyBlockEnd(lines, j)
		if end > classEnd {
			end = classEnd
		}
		methods = append(methods, Entity{Name: className + "." + m[1], Kind: "function", StartLine: start + 1, EndLine: end})
	}
	return methods
}

// pyBlockEnd finds the end of a Python block based on indentation.
// It first skips past the function/class signature (which may span multiple
// lines) by looking for the bracket-depth-zero ':' that opens the body —
// a one-liner body after that colon ("def __repr__(self): return x") ends
// the block on the signature line itself.
// Then it tracks indentation to find where the body ends.
func pyBlockEnd(lines []string, startIdx int) int {
	n := len(lines)
	baseIndent := indentLen(lines[startIdx])

	// Step 1: advance past the signature (annotations and defaults keep
	// their colons inside brackets, so depth accumulates across lines).
	bodyStart := startIdx
	depth := 0
	for bodyStart < n {
		l := stripLineComment(lines[bodyStart], "python")
		colon := -1
		for k := 0; k < len(l) && colon < 0; k++ {
			switch l[k] {
			case '(', '[', '{':
				depth++
			case ')', ']', '}':
				depth--
			case ':':
				if depth == 0 {
					colon = k
				}
			}
		}
		bodyStart++
		if colon >= 0 {
			if strings.TrimSpace(l[colon+1:]) != "" {
				return bodyStart // one-liner: the block is the signature line
			}
			break
		}
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
	reJavaClass = regexp.MustCompile(`^(?:(?:public|private|protected|abstract|final|static)\s+)*(?:class|interface|enum|record)\s+(\w+)`)
	// return-type atom allows spaces so generic types (Map<String, List<X>>)
	// match; '=' stays excluded, which keeps field initializers out
	reJavaMethod = regexp.MustCompile(`^(?:(?:public|private|protected|abstract|final|static|synchronized|native|default)\s+)*(?:<[^>]+>\s+)?[\w\[\]<>,\s]+\s+(\w+)\s*\(`)
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
			classBodyLines(lines, i, end, func(j int) {
				tl := strings.TrimSpace(lines[j])
				if !reJavaMethod.MatchString(tl) || !strings.Contains(lines[j], "{") {
					return
				}
				mEnd := goBlockEnd(lines, j)
				if mEnd > end {
					mEnd = end
				}
				mName := reJavaMethod.FindStringSubmatch(tl)[1]
				entities = append(entities, Entity{Name: name + "." + mName, Kind: "function", StartLine: j + 1, EndLine: mEnd})
			})
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
	reRustImpl   = regexp.MustCompile(`^impl\b`)
	reRustUse    = regexp.MustCompile(`^use\s`)
	reRustMod    = regexp.MustCompile(`^(?:pub\s+)?mod\s+(\w+)`)
	reRustConst  = regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(?:const|static)\s+(\w+)`)
	reRustWord   = regexp.MustCompile(`\w+`)
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
		case reRustImpl.MatchString(line):
			flushImports(i)
			name := rustImplName(line)
			start := i + 1
			end := goBlockEnd(lines, i)
			// The container is named by its full header ("impl Foo",
			// "impl Display for Foo") so it never shares Kind+Name with the
			// struct or a sibling impl block — snippet grouping keys on that
			// pair and would merge them. Methods still use the target type.
			header := strings.TrimSpace(strings.TrimSuffix(line, "{"))
			entities = append(entities, Entity{Name: header, Kind: "type", StartLine: start, EndLine: end})
			classBodyLines(lines, i, end, func(j int) {
				m := reRustFn.FindStringSubmatch(strings.TrimSpace(lines[j]))
				if m == nil {
					return
				}
				mEnd := goBlockEnd(lines, j)
				if mEnd > end {
					mEnd = end
				}
				entities = append(entities, Entity{Name: name + "." + m[1], Kind: "function", StartLine: j + 1, EndLine: mEnd})
			})
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

// rustImplName extracts the type an impl block targets: the type after
// "for" in trait impls (impl Trait for Type), else the first type name
// after "impl" (skipping <generics>).
func rustImplName(line string) string {
	rest := strings.TrimPrefix(line, "impl")
	if idx := strings.Index(rest, " for "); idx >= 0 {
		rest = rest[idx+5:]
	} else if strings.HasPrefix(rest, "<") {
		depth := 0
		for k, ch := range rest {
			if ch == '<' {
				depth++
			} else if ch == '>' {
				depth--
				if depth == 0 {
					rest = rest[k+1:]
					break
				}
			}
		}
	}
	if m := reRustWord.FindString(rest); m != "" {
		return m
	}
	return "impl"
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

func splitLines(src []byte) []string {
	return strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
}
