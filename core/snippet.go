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
	// ID is a stable hash: sha256(filePath + "::" + symbolPath + "::" + startLine).
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
//  1. For every hunk, find the entity whose [StartLine, EndLine] contains the
//     hunk's [NewStart, NewStart+NewLines-1]. If multiple entities overlap
//     (shouldn't happen with well-formed source), pick the smallest span.
//  2. Hunks that don't fall inside any entity go into a "preamble" bucket
//     (keyed on the file path so all uncovered hunks collapse together).
//     Preamble groups with no substantive change are dropped entirely.
//  3. Group hunks by entity, then build one Snippet per group from the merged
//     diff lines in hunk order. Entity groups whose fragments carry no +/-
//     lines at all (pure context spill) are dropped.
func entitySnippets(f *FileDiff, entities []Entity, src []byte) []*Snippet {
	type group struct {
		entity    *Entity // nil == preamble
		hunkLines []string
		startLine int
		endLine   int
	}

	// ordered list of groups, keyed by entity name for deduplication.
	var order []string
	groups := make(map[string]*group)

	addHunk := func(key string, ent *Entity, h Hunk) {
		if _, ok := groups[key]; !ok {
			order = append(order, key)
			sl, el := h.NewStart, h.NewStart+h.NewLines-1
			if ent != nil {
				sl, el = ent.StartLine, ent.EndLine
			}
			groups[key] = &group{entity: ent, startLine: sl, endLine: el}
		}
		groups[key].hunkLines = append(groups[key].hunkLines, h.Lines...)
	}

	for i := range f.Hunks {
		// Split hunks that straddle entity boundaries (context overlap,
		// whole-new-file hunks) so each piece maps to exactly one entity.
		for _, h := range splitHunkAtEntities(f.Hunks[i], entities) {
			// A piece with no +/- lines is context spilled across an entity
			// boundary; git apply rejects such a hunk as corrupt.
			if !hasChangedLine(h.Lines) {
				continue
			}
			hStart := h.NewStart
			hEnd := h.NewStart + h.NewLines - 1
			if hEnd < hStart {
				hEnd = hStart
			}

			best := findEnclosingEntity(entities, hStart, hEnd)
			if best != nil {
				key := best.Kind + "::" + best.Name
				addHunk(key, best, h)
			} else {
				// Preamble: lines not inside any named entity.
				// Group all such hunks under a single preamble snippet.
				addHunk("__preamble__", nil, h)
			}
		}
	}

	sourceLines := splitLines(src)
	snippets := make([]*Snippet, 0, len(order))
	for _, key := range order {
		g := groups[key]

		// Blank-line-only fragments between entities would otherwise become
		// empty preamble snippets — pure noise. Entity snippets are kept even
		// when their only change is whitespace, but dropped when the fragment
		// is pure context spill (no +/- lines at all) from a neighboring hunk.
		if g.entity == nil && !substantiveChange(g.hunkLines) {
			continue
		}
		if g.entity != nil && !hasChangedLine(g.hunkLines) {
			continue
		}

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
			Status:     StatusPending,
		}
		s.ID = snippetID(f.Path, s.SymbolPath, s.StartLine)
		snippets = append(snippets, s)
	}
	return snippets
}

// findEnclosingEntity returns the entity that best matches the line range
// [hStart, hEnd]. It prefers the smallest entity that fully contains the
// range; if none fully contains it, it falls back to the entity with the
// greatest line overlap (e.g. when a diff adds lines that push past the
// current entity boundary, or when a hunk straddles two entities).
func findEnclosingEntity(entities []Entity, hStart, hEnd int) *Entity {
	// First pass: exact containment — smallest entity that fully wraps the hunk.
	var best *Entity
	for i := range entities {
		e := &entities[i]
		if e.StartLine <= hStart && e.EndLine >= hEnd {
			if best == nil || (e.EndLine-e.StartLine) < (best.EndLine-best.StartLine) {
				best = e
			}
		}
	}
	if best != nil {
		return best
	}

	// Second pass: maximum overlap — for hunks that extend past entity
	// boundaries (e.g. new lines added at end of a function).
	bestOverlap := 0
	for i := range entities {
		e := &entities[i]
		// Overlap = intersection length
		overlapStart := hStart
		if e.StartLine > overlapStart {
			overlapStart = e.StartLine
		}
		overlapEnd := hEnd
		if e.EndLine < overlapEnd {
			overlapEnd = e.EndLine
		}
		overlap := overlapEnd - overlapStart + 1
		if overlap > bestOverlap {
			bestOverlap = overlap
			best = e
		}
	}
	return best
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

// substantiveChange reports whether diff lines carry a real change: at least
// one added or removed line with non-whitespace content after the sigil.
// Hunk headers and context lines never count.
func substantiveChange(lines []string) bool {
	for _, l := range lines {
		if len(l) == 0 || strings.HasPrefix(l, "@@") {
			continue
		}
		if (l[0] == '+' || l[0] == '-') && strings.TrimSpace(l[1:]) != "" {
			return true
		}
	}
	return false
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

// snippetID computes a stable ID for a snippet.
//
// Named entities are keyed by (file, symbol) only, so review state survives
// when lines shift above the entity. Nameless snippets (preamble, fallback
// hunks) have no symbol to key on and fall back to the start line.
func snippetID(filePath, symbolPath string, startLine int) string {
	key := fmt.Sprintf("%s::%s", filePath, symbolPath)
	if symbolPath == "" {
		key = fmt.Sprintf("%s::%d", key, startLine)
	}
	h := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%x", h[:8])
}
