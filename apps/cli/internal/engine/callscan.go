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
// and constructions are the edges that define reading order.
package engine

import (
	"sort"
	"strings"
)

// BuildCallEdges scans each snippet's entity body for call sites that name
// another changed entity and returns EdgeCall edges (caller → callee).
// Edges are deduplicated per (From, To), keeping the earliest call line.
func BuildCallEdges(snippets []*Snippet) []SymbolEdge {
	// Symbol table: entity name → snippets declaring it.
	table := make(map[string][]*Snippet)
	for _, s := range snippets {
		if s.SymbolPath == "" || len(s.SymbolPath) < 2 {
			continue
		}
		switch s.NodeType {
		case "function", "class", "type":
			table[s.SymbolPath] = append(table[s.SymbolPath], s)
		}
	}
	if len(table) == 0 {
		return nil
	}

	type edgeKey struct{ from, to string }
	best := make(map[edgeKey]int) // (from, to) → earliest call line

	for _, s := range snippets {
		for i, line := range s.Body {
			for _, name := range callSites(line, s.Language) {
				targets, ok := table[name]
				if !ok {
					continue
				}
				callLine := s.StartLine + i
				for _, t := range resolveTargets(s, targets) {
					if t.ID == s.ID {
						continue
					}
					k := edgeKey{s.ID, t.ID}
					if prev, seen := best[k]; !seen || callLine < prev {
						best[k] = callLine
					}
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

// resolveTargets narrows same-named candidates: prefer declarations in the
// caller's file, then its directory, else keep all (tolerated ambiguity).
func resolveTargets(from *Snippet, candidates []*Snippet) []*Snippet {
	if len(candidates) <= 1 {
		return candidates
	}
	var sameFile, sameDir []*Snippet
	fromDir := pathDir(from.FilePath)
	for _, c := range candidates {
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
	return candidates
}

func pathDir(p string) string {
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		return p[:idx]
	}
	return ""
}

// callSites returns the identifiers used as call/construction sites on one
// source line. Line comments are stripped first (crudely — a comment marker
// inside a string literal will truncate the scan; acceptable tolerance).
func callSites(line, lang string) []string {
	line = stripLineComment(line, lang)

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
