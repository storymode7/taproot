// Package engine builds Snippets from FileDiffs and a WASM-based tree-sitter parser.
package engine

import (
	"crypto/sha256"
	"fmt"

	"github.com/astra-review/astra/apps/cli/internal/diff"
)

// Snippet is a reviewable unit: the smallest named AST node that encloses
// one or more diff hunks from a single file.
//
// A single file may produce multiple Snippets — one per distinct enclosing
// AST node. This allows the ordering engine to place, e.g., utils.go::formatDate
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

	// SymbolPath is the dotted name of the enclosing AST node,
	// e.g. "MyClass.myMethod". Empty for top-level / unsupported languages.
	SymbolPath string

	// NodeType is the tree-sitter node type ("function_declaration", "class_body", etc.)
	NodeType string

	// StartLine and EndLine are 1-indexed line numbers in the post-image.
	StartLine int
	EndLine   int

	// DiffLines are the raw unified-diff lines for this snippet.
	DiffLines []string

	// Depth is set by the ordering engine (call-graph distance from root).
	Depth int

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
// When a tree-sitter parser is available for the file's language, each hunk
// is mapped to the smallest enclosing named AST node. Multiple hunks in the
// same AST node are merged into a single Snippet.
//
// For unsupported languages (or when parsing fails), the entire file's
// hunks are collapsed into one Snippet with an empty SymbolPath.
//
// parser is optional; pass nil to use heuristic-only mode (one snippet
// per hunk, no AST node grouping). A real WASM parser will be wired in
// once wazero integration is complete.
func BuildSnippets(files []*diff.FileDiff, parser ASTParser) []*Snippet {
	var snippets []*Snippet
	for _, f := range files {
		if f.IsDeleted || f.IsBinary || len(f.Hunks) == 0 {
			continue
		}
		snippets = append(snippets, fileSnippets(f, parser)...)
	}
	return snippets
}

// ASTParser is the interface for tree-sitter WASM integration.
// The nil-safe heuristic path is used until wazero wiring is complete.
type ASTParser interface {
	// SmallestEnclosingNode returns the symbol path and node type of the
	// smallest named AST node that contains lines [startLine, endLine]
	// in the given source bytes.
	SmallestEnclosingNode(src []byte, lang string, startLine, endLine int) (symbolPath, nodeType string, err error)
}

// fileSnippets builds snippets for one changed file.
func fileSnippets(f *diff.FileDiff, parser ASTParser) []*Snippet {
	if parser == nil || f.Language == "" {
		return heuristicSnippets(f)
	}

	// For each hunk, ask the parser for its enclosing AST node.
	// Merge adjacent hunks that share the same enclosing node.
	type nodeKey struct{ symbolPath, nodeType string }
	type nodeGroup struct {
		nodeKey
		startLine, endLine int
		diffLines          []string
	}

	// We'd need the full file source to parse properly; for now fall back
	// to heuristic until wazero is wired. This is the correct structure to fill.
	// TODO: fetch file blob via Source.FileBlob() and parse with wazero.
	_ = parser
	return heuristicSnippets(f)
}

// heuristicSnippets creates one snippet per hunk (no AST grouping).
// Used as a fallback for unsupported languages or before wazero is wired.
func heuristicSnippets(f *diff.FileDiff) []*Snippet {
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
func snippetID(filePath, symbolPath string, startLine int) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s::%s::%d", filePath, symbolPath, startLine)))
	return fmt.Sprintf("%x", h[:8])
}
