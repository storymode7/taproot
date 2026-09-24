// Call-edge extraction via symbol-table token scan.
//
// We only need edges between snippets in the same diff, so the candidate
// set is small: the names of the changed entities themselves. Scanning each
// entity's post-image body for tokens that reference another changed
// entity's name is language-agnostic, needs no parser, and has a tiny
// false-positive surface (a stray match only matters if it happens to name
// another changed entity).
//
// A token counts as a reference when it is used like a call or construction:
//   - followed by "(":            foo(...), pkg.Foo(...), x.foo(...)
//   - followed by "{" (Go only):  Foo{...} composite literal
//   - preceded by "new":          new Foo   (JS/TS/Java)
//
// Bare type references (var c Config) are deliberately not matched — calls
// and constructions are the edges that define reading order. Changed
// var/const entities are the exception: they have no call syntax, so any
// bare identifier token naming one creates a reference edge (references
// define reading order exactly like calls). To keep that from matching
// noise, tokens shorter than three characters or in a keyword/builtin
// stopword set never resolve to a variable, tokens already matched as call
// sites don't double as references, and bare references only resolve to
// declarations in the referencer's own file or directory (same language).
package core

import (
	"sort"
	"strings"
)

// BuildCallEdges scans each snippet's entity body for call sites that name
// another changed entity — and for bare identifier references to changed
// var/const entities — and returns EdgeCall edges (caller → callee).
// Edges are deduplicated per (From, To), keeping the earliest call line.
//
// Body lines that fall inside a nested same-file snippet (e.g. a method
// inside a class-container snippet) are skipped, so the container does not
// duplicate every edge its nested snippets create.
func BuildCallEdges(snippets []*Snippet) []SymbolEdge {
	// Symbol tables: last dotted segment of the entity name → snippets
	// declaring it. Keying on the last segment lets method call sites like
	// self.parse( or obj.parse( resolve "ProductSpider.parse".
	table := make(map[string][]*Snippet)    // call-site targets
	varTable := make(map[string][]*Snippet) // bare-reference targets
	for _, s := range snippets {
		name := lastSegment(s.SymbolPath)
		if len(name) < 2 {
			continue
		}
		switch s.NodeType {
		case "function", "class", "type":
			table[name] = append(table[name], s)
		case "var", "const":
			varTable[name] = append(varTable[name], s)
		}
	}
	if len(table) == 0 && len(varTable) == 0 {
		return nil
	}

	type edgeKey struct{ from, to string }
	best := make(map[edgeKey]int) // (from, to) → earliest call line

	addEdges := func(from *Snippet, targets []*Snippet, callLine int) {
		for _, t := range resolveTargets(from, targets) {
			if t.ID == from.ID {
				continue
			}
			k := edgeKey{from.ID, t.ID}
			if prev, seen := best[k]; !seen || callLine < prev {
				best[k] = callLine
			}
		}
	}

	for _, s := range snippets {
		nested := nestedSpans(s, snippets)
		for i, line := range maskCode(s.Body, s.Language) {
			callLine := s.StartLine + i
			if inSpans(callLine, nested) {
				continue
			}
			sites := callSites(line, s.Language)
			for _, name := range sites {
				if targets, ok := table[name]; ok {
					addEdges(s, targets, callLine)
				}
			}
			if len(varTable) == 0 {
				continue
			}
			for _, tok := range identTokens(line, s.Language) {
				if len(tok) < 3 || refStopwords[tok] || contains(sites, tok) {
					continue
				}
				if targets, ok := varTable[tok]; ok {
					addEdges(s, varRefTargets(s, targets), callLine)
				}
			}
		}
	}

	edges := make([]SymbolEdge, 0, len(best))
	for k, line := range best {
		edges = append(edges, SymbolEdge{From: k.from, To: k.to, Kind: EdgeCall, CallLine: line})
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})
	return edges
}

// resolveTargets narrows same-named candidates: only declarations in the
// caller's language family count, then prefer the caller's file, then its
// directory, else keep all (tolerated ambiguity).
func resolveTargets(from *Snippet, candidates []*Snippet) []*Snippet {
	var sameLang []*Snippet
	for _, c := range candidates {
		if langFamily(c.Language) == langFamily(from.Language) {
			sameLang = append(sameLang, c)
		}
	}
	if len(sameLang) <= 1 {
		return sameLang
	}
	var sameFile, sameDir []*Snippet
	fromDir := pathDir(from.FilePath)
	for _, c := range sameLang {
		if c.FilePath == from.FilePath {
			sameFile = append(sameFile, c)
		} else if pathDir(c.FilePath) == fromDir {
			sameDir = append(sameDir, c)
		}
	}
	if len(sameFile) > 0 {
		return sameFile
	}
	if len(sameDir) > 0 {
		return sameDir
	}
	return sameLang
}

// langFamily groups languages that call each other directly (JS ↔ TS).
func langFamily(lang string) string {
	if lang == "typescript" {
		return "javascript"
	}
	return lang
}

// varRefTargets scopes bare-reference resolution. Unlike a call site, a bare
// identifier has no syntax tying it to the declaration, so only var/const
// declarations in the referencer's own file or directory (same language) are
// credible targets — a lone same-named local in another package must not
// merge unrelated components.
func varRefTargets(from *Snippet, candidates []*Snippet) []*Snippet {
	var out []*Snippet
	fromDir := pathDir(from.FilePath)
	for _, c := range candidates {
		if c.Language != from.Language {
			continue
		}
		if c.FilePath == from.FilePath || pathDir(c.FilePath) == fromDir {
			out = append(out, c)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func pathDir(p string) string {
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		return p[:idx]
	}
	return ""
}

// lastSegment returns the final dotted component of a symbol path
// ("ProductSpider.parse" → "parse").
func lastSegment(path string) string {
	if idx := strings.LastIndex(path, "."); idx >= 0 {
		return path[idx+1:]
	}
	return path
}

// nestedSpans returns the line spans of other same-file snippets strictly
// nested inside s's span (e.g. method snippets inside a class-container
// snippet, whose Body covers the whole class).
func nestedSpans(s *Snippet, snippets []*Snippet) [][2]int {
	var spans [][2]int
	for _, t := range snippets {
		if t.ID == s.ID || t.FilePath != s.FilePath {
			continue
		}
		if t.StartLine >= s.StartLine && t.EndLine <= s.EndLine &&
			(t.EndLine-t.StartLine) < (s.EndLine-s.StartLine) {
			spans = append(spans, [2]int{t.StartLine, t.EndLine})
		}
	}
	return spans
}

func inSpans(line int, spans [][2]int) bool {
	for _, sp := range spans {
		if line >= sp[0] && line <= sp[1] {
			return true
		}
	}
	return false
}

// refStopwords are identifier tokens that never resolve as a bare reference
// to a var/const snippet: keywords and ubiquitous builtins whose collision
// with a changed variable name is far more likely noise than a reference.
var refStopwords = map[string]bool{
	"var": true, "const": true, "let": true, "func": true, "fn": true,
	"def": true, "class": true, "type": true, "struct": true, "enum": true,
	"trait": true, "impl": true, "interface": true, "return": true,
	"if": true, "else": true, "elif": true, "for": true, "while": true,
	"range": true, "switch": true, "case": true, "break": true,
	"continue": true, "new": true, "pub": true, "use": true, "mod": true,
	"import": true, "from": true, "package": true, "static": true,
	"final": true, "void": true, "self": true, "this": true, "super": true,
	"true": true, "false": true, "nil": true, "None": true, "null": true,
	"async": true, "await": true, "yield": true, "try": true,
	"except": true, "catch": true, "finally": true, "raise": true,
	"throw": true, "defer": true, "chan": true, "select": true,
	"map": true, "print": true, "len": true,
}

// callSites returns the identifiers used as call/construction sites on one
// source line. Callers pass lines through maskCode first, so strings and
// comments are already blank.
func callSites(line, lang string) []string {
	var names []string
	runes := []rune(line)
	n := len(runes)
	i := 0
	prevWord := ""
	for i < n {
		if !isIdentStart(runes[i]) {
			i++
			continue
		}
		start := i
		for i < n && isIdentPart(runes[i]) {
			i++
		}
		word := string(runes[start:i])

		// Next non-space rune decides whether this is a call site.
		j := i
		for j < n && (runes[j] == ' ' || runes[j] == '\t') {
			j++
		}
		isCall := false
		if j < n {
			switch runes[j] {
			case '(':
				isCall = true
			case '{':
				// Go composite literal: Foo{ — only immediately adjacent,
				// otherwise every `if x {` matches its condition's last word.
				isCall = lang == "go" && j == i
			}
		}
		if !isCall && prevWord == "new" {
			isCall = true // new Foo  (JS/TS/Java)
		}
		if isCall {
			names = append(names, word)
		}
		prevWord = word
	}
	return names
}

// identTokens returns every identifier token on one masked source line
// (same tokenizer as callSites). Used for bare-reference edges to
// var/const snippets; the caller applies the length/stopword guard.
func identTokens(line, lang string) []string {
	var names []string
	runes := []rune(line)
	n := len(runes)
	i := 0
	for i < n {
		if !isIdentStart(runes[i]) {
			i++
			continue
		}
		start := i
		for i < n && isIdentPart(runes[i]) {
			i++
		}
		names = append(names, string(runes[start:i]))
	}
	return names
}

func stripLineComment(line, lang string) string {
	marker := "//"
	if lang == "python" {
		marker = "#"
	}
	if idx := strings.Index(line, marker); idx >= 0 {
		return line[:idx]
	}
	return line
}

func isIdentStart(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isIdentPart(r rune) bool {
	return isIdentStart(r) || (r >= '0' && r <= '9')
}
