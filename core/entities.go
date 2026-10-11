// Package engine — entity boundary extraction for supported languages.
//
// An "entity" is a top-level (or class-level) named declaration that forms a
// meaningful review unit: function/method, class/struct/interface/type, or a
// large top-level variable/constant block. Import groups are treated as a
// single "imports" entity so they are shown together.
//
// The strategy is intentionally heuristic: we scan the source lines with
// language-specific regexes rather than requiring a full AST parser. This is
// fast, has no WASM/CGo dependencies, and is accurate enough for the entity
// boundaries produced by real codebases. The scanners read the lines with
// strings and comments blanked by maskCode, so a brace, bracket, quote or #
// inside a literal or comment never moves a boundary.
//
// Accuracy priority: never merge two distinct named entities into one snippet.
// If a boundary is ambiguous, split conservatively (produce more snippets).
package core

import (
	"regexp"
	"slices"
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
	raw := splitLines(src)
	lines, open := maskCodeState(raw, lang)
	var entities []Entity
	switch lang {
	case "go":
		entities = extractGo(lines, open)
	case "javascript", "typescript":
		entities = extractJS(lines, raw, open)
	case "python":
		entities = extractPython(lines, raw, open)
	case "java":
		entities = extractJava(lines)
	case "rust":
		entities = extractRust(lines, raw)
	default:
		return nil
	}
	extendLeads(entities, lines, raw, lang)
	return entities
}

var (
	// a decorator or annotation with nothing after it: @Get(':id'), not
	// @Input() name: string
	reLeadAt       = regexp.MustCompile(`^@[\w.$]+\s*(?:\(.*\))?$`)
	reLeadRustAttr = regexp.MustCompile(`^#\[.*\]$`)
)

// extendLeads moves each entity's start up over the decorators,
// annotations, attributes and doc comments written directly above it, so a
// change to them belongs to the entity and staging the entity takes them
// along. code holds the masked lines, raw the source lines.
func extendLeads(entities []Entity, code, raw []string, lang string) {
	ends := make([]int, len(entities))
	for i, e := range entities {
		ends[i] = e.EndLine
	}
	slices.Sort(ends)
	for i := range entities {
		e := &entities[i]
		if e.Kind == "imports" || e.StartLine < 1 || e.StartLine > len(raw) {
			continue
		}
		floor := 0 // the last line of the entities above; a lead never reaches into one
		if k, _ := slices.BinarySearch(ends, e.StartLine); k > 0 {
			floor = ends[k-1]
		}
		e.StartLine = leadStart(code, raw, e.StartLine-1, floor, lang) + 1
	}
}

// leadStart returns the index of the first lead line directly above line
// decl, at its indent and at index floor or below: comments (a block comment
// counts whole) and decorators, annotations or attributes, whose arguments
// may wrap. Python takes decorators only; Rust leaves //!, /*! and #![...]
// to the enclosing module.
func leadStart(code, raw []string, decl, floor int, lang string) int {
	indent := indentLen(raw[decl])
	start := decl
	for start > floor {
		k := start - 1
		t := strings.TrimSpace(raw[k])
		switch {
		case t == "":
			return start
		case lang != "python" && strings.TrimSpace(code[k]) == "" && strings.HasSuffix(t, "*/"):
			o := k
			for o > floor && !strings.Contains(raw[o], "/*") {
				o--
			}
			ot := strings.TrimSpace(raw[o])
			if !strings.HasPrefix(ot, "/*") || lang == "rust" && strings.HasPrefix(ot, "/*!") || indentLen(raw[o]) != indent {
				return start
			}
			for j := o; j < k; j++ {
				if strings.TrimSpace(code[j]) != "" {
					return start
				}
			}
			start = o
		case lang != "python" && strings.HasPrefix(t, "//") && indentLen(raw[k]) == indent:
			if lang == "rust" && strings.HasPrefix(t, "//!") {
				return start
			}
			start = k
		default:
			o, depth := k, bracketDelta(code[k])
			for depth < 0 && o > floor {
				o--
				depth += bracketDelta(code[o])
			}
			group := make([]string, 0, k-o+1)
			for j := o; j <= k; j++ {
				group = append(group, strings.TrimSpace(code[j]))
			}
			attr := strings.Join(group, " ")
			ok := depth == 0 && indentLen(raw[o]) == indent
			switch lang {
			case "python":
				ok = ok && strings.HasPrefix(attr, "@")
			case "rust":
				ok = ok && reLeadRustAttr.MatchString(attr)
			case "go":
				ok = false
			default:
				ok = ok && reLeadAt.MatchString(attr)
			}
			if !ok {
				return start
			}
			start = o
		}
	}
	return start
}

// ---------------------------------------------------------------------------
// Go
// ---------------------------------------------------------------------------

var (
	reGoFunc        = regexp.MustCompile(`^func\s`)
	reGoType        = regexp.MustCompile(`^type\s+\w`)
	reGoVarConst    = regexp.MustCompile(`^(?:var|const)\s`)
	reGoImport      = regexp.MustCompile(`^import(?:\s|$)`)
	reGoBlockOpen   = regexp.MustCompile(`\{`)
	reGoImportGroup = regexp.MustCompile(`^import\s*\($`)
)

func extractGo(lines []string, open []bool) []Entity {
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
			end := declEnd(lines, i, "go")
			entities = append(entities, Entity{Name: name, Kind: "function", StartLine: start, EndLine: end})
			i = end

		case reGoType.MatchString(stripped):
			// type Name struct { ... } or type Name interface { ... } or type Name = ...
			name := goTypeName(stripped)
			start := i + 1
			end := declEnd(lines, i, "go")
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
			i = goVarEnd(lines, open, i)
			entities = append(entities, Entity{Name: name, Kind: kind, StartLine: start, EndLine: i})

		default:
			i++
		}
	}
	return entities
}

// goVarEnd returns the 1-indexed last line of a var or const declaration:
// the first line where its brackets balance with no string left open, so a
// ( ... ) group, a composite or func literal, or a raw string stays whole.
func goVarEnd(lines []string, open []bool, startIdx int) int {
	depth := 0
	for i := startIdx; i < len(lines); i++ {
		depth += bracketDelta(lines[i])
		if depth <= 0 && !open[i] {
			return i + 1
		}
	}
	return len(lines)
}

// goBlockEnd finds the line where the brace-delimited block starting at or
// after startIdx ends. Returns 1-indexed line number.
func goBlockEnd(lines []string, startIdx int) int {
	return declEnd(lines, startIdx, "")
}

// declEnd is goBlockEnd for a declaration that may have no brace body.
// Until a '{' opens, Go ends one on the first line where its ()/[] balance
// closes (type ID string, a bodyless func), and Rust at a ';' outside
// brackets (struct Unit;, type A = B;, mod m;).
func declEnd(lines []string, startIdx int, lang string) int {
	n := len(lines)
	depth, paren := 0, 0
	opened := false
	for i := startIdx; i < n; i++ {
		d, o := braceDelta(lines[i])
		depth += d
		opened = opened || o
		if opened {
			if depth == 0 {
				return i + 1 // 1-indexed
			}
			continue
		}
		paren += parenDelta(lines[i])
		if paren <= 0 && (lang == "go" || lang == "rust" && strings.HasSuffix(strings.TrimSpace(lines[i]), ";")) {
			return i + 1
		}
	}
	return n // fallback: rest of file
}

// parenDelta counts one line's net ()/[] balance.
func parenDelta(line string) int {
	d := 0
	for _, c := range line {
		switch c {
		case '(', '[':
			d++
		case ')', ']':
			d--
		}
	}
	return d
}

// bracketDelta counts one masked line's net ()/[]/{} balance.
func bracketDelta(line string) int {
	d, _ := braceDelta(line)
	return d + parenDelta(line)
}

// braceDelta counts one line's net {/} balance. Lines arrive masked, so
// braces in strings, char literals and comments are already blank.
func braceDelta(line string) (delta int, opened bool) {
	for _, c := range line {
		switch c {
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
	// export class Foo  /  class Foo  /  export abstract class Foo
	reJSClass = regexp.MustCompile(`^(?:export\s+(?:default\s+)?)?(?:abstract\s+)?class\s+(\w+)`)
	// export interface Foo  (TypeScript)
	reJSInterface = regexp.MustCompile(`^(?:export\s+)?interface\s+(\w+)`)
	// export type Foo =  /  type Box<T> =  (TypeScript)
	reJSTypeAlias = regexp.MustCompile(`^(?:export\s+)?type\s+(\w+)\s*(?:<.*>)?\s*=`)
	// export enum Foo  (TypeScript)
	reJSEnum = regexp.MustCompile(`^(?:export\s+)?(?:const\s+)?enum\s+(\w+)`)
	// const/let/var foo = ...  or  export const foo = ...
	reJSVar = regexp.MustCompile(`^(?:export\s+)?(?:const|let|var)\s+(\w+)`)
	// function-valued initializer: = [async] (…) =>  /  = [async] ident =>  /  = [async] function
	// (multi-line parameter lists before the arrow are not recognized)
	reJSFuncVal = regexp.MustCompile(`=\s*(?:async\s+)?(?:function\b|\(.*\)\s*=>|[A-Za-z_$][\w$]*\s*=>)`)
	// method inside a class body: [static async get set ...] [*] name[<T>](
	// (generics allow one nesting level: <T extends Base<K>>)
	reJSMethod = regexp.MustCompile(`^(?:(?:static|async|get|set|public|private|protected|readonly|override)\s+)*\*?\s*(#?[A-Za-z_$][\w$]*)\s*(?:<[^<>]*(?:<[^<>]*>[^<>]*)*>)?\s*\(`)
	// class-property arrow: [modifiers] name = [async] (…) =>
	reJSPropArrow = regexp.MustCompile(`^(?:(?:static|public|private|protected|readonly)\s+)*(#?[A-Za-z_$][\w$]*)\s*=\s*(?:async\s+)?\(.*\)\s*=>`)
	// start of a new top-level declaration — used to stop the no-brace
	// arrow-body scan in semicolon-less code
	reJSDeclStart = regexp.MustCompile(`^(?:export\s+(?:default\s+)?)?(?:async\s+)?(?:function\b|(?:abstract\s+)?class\s|interface\s|enum\s|const\s|let\s|var\s|type\s+\w+\s*(?:<.*>)?\s*=|import(?:\s|$))`)
	// import ...
	reJSImport = regexp.MustCompile(`^import(?:\s|$)`)
	// require(...)
	reJSRequire = regexp.MustCompile(`\brequire\s*\(`)
)

func extractJS(lines, raw []string, open []bool) []Entity {
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
			entities = append(entities, jsClassMethods(lines, raw, open, i, end, name)...)
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
			end := jsTypeAliasEnd(lines, raw, i)
			entities = append(entities, Entity{Name: name, Kind: "type", StartLine: start, EndLine: end})
			i = end

		case reJSFunc.MatchString(line):
			flushImports(i)
			name := reJSFunc.FindStringSubmatch(line)[1]
			start := i + 1
			end := jsFuncEnd(lines, raw, open, i)
			entities = append(entities, Entity{Name: name, Kind: "function", StartLine: start, EndLine: end})
			i = end

		case reJSVar.MatchString(line):
			flushImports(i)
			name := reJSVar.FindStringSubmatch(line)[1]
			start := i + 1
			if reJSFuncVal.MatchString(line) {
				// function-valued const/let/var: arrow or function expression
				end := jsVarEnd(lines, open, i)
				if strings.Contains(line, "{") || strings.HasSuffix(line, "=>") {
					end = jsFuncEnd(lines, raw, open, i)
				}
				entities = append(entities, Entity{Name: name, Kind: "function", StartLine: start, EndLine: end})
				i = end
			} else {
				end := jsVarEnd(lines, open, i)
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

// jsTypeAliasEnd returns the 1-indexed last line of a type alias: the first
// line outside brackets that ends in ';' or is blank, or the line before the
// next declaration in semicolon-less code. A union alias has no brackets
// around its members, so it runs to its ';'.
func jsTypeAliasEnd(lines, raw []string, startIdx int) int {
	depth := 0
	for i := startIdx; i < len(lines); i++ {
		tl := strings.TrimSpace(lines[i])
		if i > startIdx && depth <= 0 && reJSDeclStart.MatchString(tl) {
			return i
		}
		depth += bracketDelta(lines[i])
		if depth <= 0 && (strings.HasSuffix(tl, ";") || strings.TrimSpace(raw[i]) == "") {
			return i + 1
		}
	}
	return len(lines)
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
// raw holds the unmasked lines: a comment or template line masks to blank,
// but only a truly blank line ends a brace-less arrow body. open marks lines
// that end inside a template literal.
func jsFuncEnd(lines, raw []string, open []bool, startIdx int) int {
	line := strings.TrimSpace(lines[startIdx])
	k := strings.LastIndex(line, "=>")
	if k < 0 {
		return jsBlockEnd(lines, startIdx) // function foo() { ... }, methods
	}
	// An arrow's body decides: a block, an expression such as ( <jsx/> ) or
	// a template literal that ends where its brackets and literal close, or
	// nothing yet when the body starts on the next line. Braces before the
	// => belong to destructured parameters.
	switch body := strings.TrimSpace(line[k+2:]); {
	case strings.HasPrefix(body, "{"):
		return jsBraceEnd(lines, startIdx)
	case body != "" || !strings.HasSuffix(strings.TrimSpace(raw[startIdx]), "=>"):
		end := jsVarEnd(lines, open, startIdx)
		for i := startIdx + 1; i < end; i++ {
			if indentLen(lines[i]) == 0 && reJSDeclStart.MatchString(lines[i]) {
				return i // a stray ( in JSX text never swallows the next declaration
			}
		}
		return end
	}
	// Arrow without braces: single expression, end at semicolon or next blank.
	// In semicolon-less code no terminator ever comes, so also stop before a
	// line that starts a new declaration or method — never merge two entities.
	// The first body line is exempt from the method check (a call like
	// `wrap(x)` looks like a method signature).
	n := len(lines)
	depth := 0
	for i := startIdx + 1; i < n; i++ {
		tl := strings.TrimSpace(lines[i])
		if depth <= 0 && (reJSDeclStart.MatchString(tl) || i > startIdx+1 && jsMethodBoundary(tl)) {
			return i
		}
		depth += bracketDelta(lines[i])
		if strings.TrimSpace(raw[i]) == "" || depth <= 0 && (strings.HasSuffix(tl, ";") || strings.HasSuffix(tl, ",")) {
			return i + 1
		}
	}
	return n
}

// jsBlockEnd returns the 1-indexed last line of a function whose body is a
// block. Braces inside the parameter list (destructuring, defaults) are
// skipped; once it closes, the body runs until its braces balance, wherever
// the opening brace sits. A ';' before any brace means an overload or a
// declaration with no body.
func jsBlockEnd(lines []string, startIdx int) int {
	paren, closed := 0, false
	depth, opened := 0, false
	for i := startIdx; i < len(lines); i++ {
		for _, c := range lines[i] {
			switch {
			case !closed && c == '(':
				paren++
			case !closed && c == ')':
				paren--
				closed = paren == 0
			case !closed:
			case c == '{':
				depth++
				opened = true
			case c == '}':
				depth--
			case c == ';' && depth == 0:
				return i + 1
			}
		}
		if opened && depth <= 0 {
			return i + 1
		}
	}
	return len(lines)
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

// jsVarEnd finds the end of a const/let/var declaration (may be multi-line
// object/array). It never ends on a line that leaves a string open, such as
// the first line of styled.div`...` or of a Python """ docstring.
func jsVarEnd(lines []string, open []bool, startIdx int) int {
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
	if depth == 0 && !open[startIdx] {
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
		if depth <= 0 && !open[i] {
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
func jsClassMethods(lines, raw []string, open []bool, classIdx, classEnd int, className string) []Entity {
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
		end := jsFuncEnd(lines, raw, open, j)
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

func extractPython(lines, raw []string, open []bool) []Entity {
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
			end := pyBlockEnd(lines, raw, open, i)
			entities = append(entities, Entity{Name: name, Kind: "class", StartLine: start, EndLine: end})
			entities = append(entities, pyClassMethods(lines, raw, open, i, end, name)...)
			i = end

		case rePyTopFunc.MatchString(line):
			flushImports(i)
			name := rePyTopFunc.FindStringSubmatch(line)[1]
			start := i + 1
			end := pyBlockEnd(lines, raw, open, i)
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
			end := jsVarEnd(lines, open, i) // bracket-balance handles multi-line values
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

// pyClassMethods extracts defs directly inside a class body as
// "Class.method" entities. The shallowest def indent in the body defines
// the method indent (a nested class's deeper defs must not lock it);
// deeper defs are absorbed by their method's span. Method spans are
// clamped to the class end; extendLeads adds their decorators.
func pyClassMethods(lines, raw []string, open []bool, classIdx, classEnd int, className string) []Entity {
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
		end := pyBlockEnd(lines, raw, open, j)
		if end > classEnd {
			end = classEnd
		}
		methods = append(methods, Entity{Name: className + "." + m[1], Kind: "function", StartLine: j + 1, EndLine: end})
	}
	return methods
}

// pyBlockEnd finds the end of a Python block based on indentation.
// It first skips past the function/class signature (which may span multiple
// lines) by looking for the bracket-depth-zero ':' that opens the body —
// a one-liner body after that colon ("def __repr__(self): return x") ends
// the block on the signature line itself.
// Then it tracks indentation to find where the body ends. lines are masked;
// raw holds the source lines, and open marks lines that end inside a string.
func pyBlockEnd(lines, raw []string, open []bool, startIdx int) int {
	n := len(lines)
	baseIndent := indentLen(lines[startIdx])

	// Step 1: advance past the signature (annotations and defaults keep
	// their colons inside brackets, so depth accumulates across lines).
	bodyStart := startIdx
	depth := 0
	for bodyStart < n {
		l := lines[bodyStart]
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

	// Step 2: scan body lines. End at the first statement line indented at
	// or left of baseIndent. Blank and comment lines, and lines inside a
	// multi-line string, never end it. A string-only statement masks to a
	// blank line but still does, unless it continues a bracket or a \ line.
	depth = 0
	cont := false
	for i := bodyStart; i < n; i++ {
		l := lines[i]
		stripped := strings.TrimSpace(l)
		top := depth <= 0 && !cont
		depth += bracketDelta(l)
		cont = strings.HasSuffix(stripped, `\`)
		if i > 0 && open[i-1] {
			continue
		}
		if r := strings.TrimLeft(raw[i], " \t"); stripped == "" && !(top && r != "" && (r[0] == '"' || r[0] == '\'')) {
			continue
		}
		if indentLen(raw[i]) <= baseIndent {
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
				if !reJavaMethod.MatchString(tl) || !javaSigOpensBody(lines, j, end) {
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

// javaSigOpensBody reports whether the method signature at line j opens a
// body: its parameter list closes and a '{' follows before any ';'. A
// wrapped signature or throws clause puts the '{' on a later line; an
// abstract or interface method ends in ';' instead.
func javaSigOpensBody(lines []string, j, end int) bool {
	depth, closed := 0, false
	for k := j; k < end && k < len(lines); k++ {
		for _, c := range lines[k] {
			switch c {
			case '(':
				depth++
			case ')':
				depth--
				closed = closed || depth == 0
			case ';':
				return false
			case '{':
				if closed && depth == 0 {
					return true
				}
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Rust
// ---------------------------------------------------------------------------

var (
	reRustFn     = regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(?:(?:const|async|unsafe|extern(?:\s+"[^"]*")?)\s+)*fn\s+(\w+)`)
	reRustStruct = regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(?:unsafe\s+)?(?:struct|enum|trait|impl|type)\s+(\w+)`)
	reRustImpl   = regexp.MustCompile(`^(?:unsafe\s+)?impl\b`)
	reRustUse    = regexp.MustCompile(`^use\s`)
	reRustMod    = regexp.MustCompile(`^(?:pub\s+)?mod\s+(\w+)`)
	reRustConst  = regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(?:const|static(?:\s+mut)?)\s+(\w+)`)
	reRustWord   = regexp.MustCompile(`\w+`)
	// a line opening with a string literal: "", r#"", b"", br"", c""
	reRustStrLead = regexp.MustCompile(`^(?:[bc]?r#*|[bc])?"`)
)

func extractRust(lines, raw []string) []Entity {
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
		if reRustStrLead.MatchString(strings.TrimSpace(raw[i])) {
			// Macro input such as `"_" pub struct Underscore/1` declares
			// nothing, though masking leaves the words after the literal
			// at the start of the line.
			line = ""
		}
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
			end := declEnd(lines, i, "rust")
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
			end := declEnd(lines, i, "rust")
			entities = append(entities, Entity{Name: name, Kind: "type", StartLine: start, EndLine: end})
			i = end
		case reRustMod.MatchString(line):
			flushImports(i)
			name := reRustMod.FindStringSubmatch(line)[1]
			start := i + 1
			end := declEnd(lines, i, "rust")
			entities = append(entities, Entity{Name: name, Kind: "type", StartLine: start, EndLine: end})
			i = end
		case reRustConst.MatchString(line):
			flushImports(i)
			name := reRustConst.FindStringSubmatch(line)[1]
			start := i + 1
			end := i + 1
			// find semicolon end
			for end < n && !strings.HasSuffix(strings.TrimSpace(lines[end-1]), ";") {
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
	rest := strings.TrimPrefix(strings.TrimPrefix(line, "unsafe "), "impl")
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
