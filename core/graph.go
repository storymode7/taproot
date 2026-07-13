package core

// EdgeKind distinguishes edge provenance in the symbol graph.
type EdgeKind int

const (
	// EdgeCall means From's entity body references To's entity name as a
	// call site. Call edges define review order and tree structure.
	EdgeCall EdgeKind = iota

	// EdgeImport is the file-level import heuristic from BuildEdgesFromDiff.
	// Import edges only group snippets into clusters; they never define
	// parent/child structure.
	EdgeImport
)

// SymbolEdge represents a directed dependency from one snippet to another.
type SymbolEdge struct {
	From string // snippet ID
	To   string // snippet ID
	Kind EdgeKind
	// CallLine is the post-image line of the first call site in From's body
	// (EdgeCall only). Used to order children in the review tree.
	CallLine int
}
