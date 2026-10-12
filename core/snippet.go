// Package core is Astra's pure diff-review engine: snippets, entities, call
// edges, and the review forest. It does no I/O — adapters on each surface
// (CLI git, extension, PWA) feed it FileDiffs with post-image Source and it
// returns structure. That purity is what lets one implementation serve the
// CLI natively and the browser surfaces via a WASM build.
//
// The core idea: each Snippet is the smallest named entity (function, method,
// class, type declaration, const/var block) that encloses one or more changed
// hunks. Multiple hunks that fall inside the same entity are merged into a
// single Snippet so the reviewer sees the whole function at once.
//
// When FileDiff.Source is set, we extract entity boundaries using
// language-aware heuristics and map every hunk to its enclosing entity.
// Hunks that fall outside any named entity (e.g. top-level package
// declarations, bare imports) are grouped into an "imports" or "preamble"
// snippet.
//
// When no source is available the fallback is one snippet per raw hunk —
// used only for unsupported languages or sourceless adapters.
package core

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// Snippet is a reviewable unit: the smallest named AST entity that encloses
// one or more diff hunks from a single file.
//
// A single file may produce multiple Snippets — one per distinct enclosing
// entity. This allows the ordering engine to place, e.g., utils.go::formatDate
// earlier in the review than utils.go::parseCSV, if the call graph demands it.
type Snippet struct {
	// ID is a stable hash of the file and the entity's kind and name
	// (entitySnippetID), or of the file and start line for a snippet with
	// no entity (snippetID).
	ID string

	// FilePath is the relative path of the changed file (new path on renames).
	FilePath string

	// OldPath is set on renames; equals FilePath otherwise.
	OldPath string

	// IsNew is true when the file was added in this diff.
	IsNew bool

	// Language is the detected language ("go", "typescript", etc.)
	Language string

	// SymbolPath is the dotted name of the enclosing entity,
	// e.g. "MyClass.myMethod". Empty for top-level / unsupported languages.
	SymbolPath string

	// NodeType is the kind of entity ("function", "class", "type", "var",
	// "const", "imports", "hunk"). "hunk" is used only in fallback mode.
	NodeType string

	// StartLine and EndLine are 1-indexed line numbers in the post-image.
	StartLine int
	EndLine   int

	// DiffLines are the raw unified-diff lines for this snippet.
	DiffLines []string

	// Body is the full post-image source of the entity ([StartLine, EndLine]),
	// without diff sigils. Set only for entity snippets; used by the call
	// scanner to find references to other changed entities.
	Body []string

	// Depth is set by the ordering engine (call-graph distance from root).
	Depth int

	// UnchangedCallers lists callers found in unchanged regions of changed
	// files, in "path::Entity" form (set by AnnotateUnchangedCallers).
	UnchangedCallers []string

	// TestedBy lists the changed tests that call this snippet, by symbol
	// path (set by BuildForest, which keeps tests out of the code's tree).
	TestedBy []string

	// IsTest marks test code its source declares, such as a Rust
	// #[cfg(test)] module; BuildForest treats it like a test file.
	IsTest bool

	// inner holds the entities within the snippet's span, its own among
	// them: the call scanner skips their declaration lines.
	inner []Entity

	// Status tracks review progress.
	Status SnippetStatus
}

// SnippetStatus represents the review state of a snippet.
type SnippetStatus int

const (
	StatusPending  SnippetStatus = iota
	StatusReviewed               // seen but no explicit judgement
	StatusAccepted               // explicitly accepted
	StatusRejected               // needs changes
)

func (s SnippetStatus) String() string {
	return [...]string{"pending", "reviewed", "accepted", "rejected"}[s]
}

// BuildSnippets converts FileDiffs into Snippets.
//
// For each file with post-image Source, entity boundaries (functions,
// classes, etc.) are extracted and each diff hunk mapped to its enclosing
// entity; hunks sharing an entity merge into a single Snippet.
//
// Files without Source (or with an unsupported language) fall back to one
// Snippet per raw hunk with no entity grouping.
func BuildSnippets(files []*FileDiff) []*Snippet {
	var snippets []*Snippet
	for _, f := range files {
		if f.IsDeleted || f.IsBinary || len(f.Hunks) == 0 {
			continue
		}
		snippets = append(snippets, fileSnippets(f)...)
	}
	return snippets
}

// fileSnippets builds snippets for one changed file.
func fileSnippets(f *FileDiff) []*Snippet {
	// Attempt entity-based splitting when we have source and a known language.
	if len(f.Source) > 0 && f.Language != "" {
		entities := f.Entities
		if len(entities) == 0 {
			entities = ExtractEntities(f.Source, f.Language)
		}
		if len(entities) > 0 {
			return entitySnippets(f, entities, f.Source)
		}
	}
	return heuristicSnippets(f)
}

// entitySnippets maps each hunk to its enclosing entity and merges hunks that
// share an entity into a single Snippet.
//
// Algorithm:
//  1. Split every hunk at entity boundaries (splitHunkAtEntities); each
//     piece belongs to one entity, or to none.
//  2. Pieces outside every entity go into a "preamble" bucket (one per file,
//     so all uncovered pieces collapse together).
//  3. Group pieces by entity identity — never by name, so Go methods on two
//     receivers, Java overloads and property getter/setter pairs stay apart
//     — then build one Snippet per group from its diff lines in hunk order.
//     The file's import blocks share one group, and so do entities whose
//     changes cannot be staged apart. Pieces with no +/- lines at all (pure
//     context spill) are dropped.
func entitySnippets(f *FileDiff, entities []Entity, src []byte) []*Snippet {
	type group struct {
		entity    *Entity // nil == preamble
		hunkLines []string
		startLine int
		endLine   int
	}

	// ordered list of groups, keyed by entity (or "imports") for dedup.
	var order []*group
	groups := make(map[any]*group)

	// The pre-image's entities tell which entity each old line belonged to.
	// Spans supplied with the file came from another parser, so they cannot
	// be compared with the core's own spans of the pre-image.
	var old *oldFile
	if len(f.Entities) == 0 {
		old = readOldFile(src, f.Hunks, f.Language, entities)
	}

	// Split hunks that straddle entity boundaries (context overlap,
	// whole-new-file hunks) so each piece maps to exactly one entity.
	var pieces []piece
	lead := make(map[*Entity]*Entity) // entities whose changes stage together
	var find func(e *Entity) *Entity
	find = func(e *Entity) *Entity {
		if l, ok := lead[e]; ok && l != e {
			lead[e] = find(l)
			return lead[e]
		}
		return e
	}
	for i := range f.Hunks {
		ps, merged := splitHunkAtEntities(f.Hunks[i], entities, old)
		pieces = append(pieces, ps...)
		for _, m := range merged {
			a, b := find(m[0]), find(m[1])
			if b != nil && (a == nil || b.StartLine < a.StartLine) {
				a, b = b, a
			}
			if a != b {
				lead[b] = a
			}
		}
	}
	for _, p := range pieces {
		// A piece with no +/- lines is context spilled across an entity
		// boundary; git apply rejects such a hunk as corrupt.
		if !hasChangedLine(p.Lines) {
			continue
		}
		ent := find(p.entity)
		key := any(ent)
		if ent != nil && ent.Kind == "imports" {
			key = "imports"
		}
		g := groups[key]
		if g == nil {
			sl, el := p.NewStart, p.NewStart+p.NewLines-1
			if ent != nil {
				sl, el = ent.StartLine, ent.EndLine
			}
			g = &group{entity: ent, startLine: sl, endLine: el}
			groups[key] = g
			order = append(order, g)
		}
		g.hunkLines = append(g.hunkLines, p.Lines...)
	}

	// ordinal numbers same-kind same-name entities in source order; the
	// 2nd and later get it in their ID.
	ordinal := make(map[*Entity]int, len(entities))
	seen := make(map[string]int)
	for i := range entities {
		e := &entities[i]
		seen[e.Kind+"::"+e.Name]++
		ordinal[e] = seen[e.Kind+"::"+e.Name]
	}

	sourceLines := splitLines(src)
	snippets := make([]*Snippet, 0, len(order))
	for _, g := range order {
		symbolPath := ""
		nodeType := "hunk"
		startLine := g.startLine
		endLine := g.endLine

		if g.entity != nil {
			symbolPath = g.entity.Name
			nodeType = g.entity.Kind
			startLine = g.entity.StartLine
			endLine = g.entity.EndLine
		}

		// For display, use the full entity source as context plus the diff
		// lines. We embed the diff lines as-is (they already carry +/- sigils).
		// If the entity is large we could trim, but for now keep it whole.
		diffLines := trimRedundantHunkHeaders(g.hunkLines)

		var body []string
		if g.entity != nil && startLine >= 1 && startLine <= len(sourceLines) {
			end := endLine
			if end > len(sourceLines) {
				end = len(sourceLines)
			}
			body = sourceLines[startLine-1 : end]
		}

		s := &Snippet{
			FilePath:   f.Path,
			OldPath:    f.OldPath,
			IsNew:      f.IsNew,
			Language:   f.Language,
			SymbolPath: symbolPath,
			NodeType:   nodeType,
			StartLine:  startLine,
			EndLine:    endLine,
			DiffLines:  diffLines,
			Body:       body,
			IsTest:     g.entity != nil && g.entity.Test,
			Status:     StatusPending,
		}
		if g.entity != nil {
			for _, e := range entities {
				if e.StartLine >= startLine && e.EndLine <= endLine {
					s.inner = append(s.inner, e)
				}
			}
			ord := ordinal[g.entity]
			if g.entity.Kind == "imports" {
				ord = 1
			}
			s.ID = entitySnippetID(f.Path, s.NodeType, s.SymbolPath, ord)
		} else {
			s.ID = snippetID(f.Path, s.SymbolPath, s.StartLine)
		}
		snippets = append(snippets, s)
	}
	return snippets
}

// trimRedundantHunkHeaders removes duplicate @@ headers when multiple hunks
// are merged into a single snippet, keeping one header per logical hunk block.
func trimRedundantHunkHeaders(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	out := make([]string, 0, len(lines))
	lastWasHeader := false
	for _, l := range lines {
		isHeader := strings.HasPrefix(l, "@@")
		if isHeader && lastWasHeader {
			// Replace the previous redundant header.
			out[len(out)-1] = l
			continue
		}
		out = append(out, l)
		lastWasHeader = isHeader
	}
	return out
}

// hasChangedLine reports whether any diff line is an addition or removal.
// Fragments with none are hunk context that spilled into a neighboring
// entity after splitting; the entity itself did not change.
func hasChangedLine(lines []string) bool {
	for _, l := range lines {
		if len(l) == 0 || strings.HasPrefix(l, "@@") {
			continue
		}
		if l[0] == '+' || l[0] == '-' {
			return true
		}
	}
	return false
}

// heuristicSnippets creates one snippet per hunk (no entity grouping).
// Used as a fallback for unsupported languages or when no file blob is available.
func heuristicSnippets(f *FileDiff) []*Snippet {
	snippets := make([]*Snippet, 0, len(f.Hunks))
	for _, h := range f.Hunks {
		s := &Snippet{
			FilePath:   f.Path,
			OldPath:    f.OldPath,
			IsNew:      f.IsNew,
			Language:   f.Language,
			SymbolPath: "",
			NodeType:   "hunk",
			StartLine:  h.NewStart,
			EndLine:    h.NewStart + h.NewLines - 1,
			DiffLines:  h.Lines,
			Status:     StatusPending,
		}
		s.ID = snippetID(f.Path, s.SymbolPath, s.StartLine)
		snippets = append(snippets, s)
	}
	return snippets
}

// snippetID computes a stable ID for a snippet without an entity. Nameless
// snippets (preamble, fallback hunks) have no symbol to key on and fall back
// to the start line.
func snippetID(filePath, symbolPath string, startLine int) string {
	key := fmt.Sprintf("%s::%s", filePath, symbolPath)
	if symbolPath == "" {
		key = fmt.Sprintf("%s::%d", key, startLine)
	}
	return hashID(key)
}

// entitySnippetID computes a stable ID for an entity snippet. It is keyed
// by (file, kind, name), not by line, so review state survives when lines
// shift above the entity; the kind keeps a const and a type that share a
// name (the zod schema pattern) apart. ordinal is the entity's place among
// the file's same-kind same-name entities (Go methods on two receivers,
// Java overloads); the first keeps the plain key.
func entitySnippetID(filePath, kind, name string, ordinal int) string {
	key := fmt.Sprintf("%s::%s::%s", filePath, kind, name)
	if ordinal > 1 {
		key = fmt.Sprintf("%s#%d", key, ordinal)
	}
	return hashID(key)
}

func hashID(key string) string {
	h := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%x", h[:8])
}
