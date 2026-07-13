// Package engine provides the snippet ordering engine.
package engine

// Preset controls the snippet ordering algorithm.
type Preset int

const (
	// PresetTopDown orders from the entry-point caller downward: the snippet
	// that nothing else calls comes first, then its callees, etc.
	// This is the default: read the "final implementation", then drill down.
	PresetTopDown Preset = iota

	// PresetBottomUp orders leaves (utilities, pure functions) first and ends
	// at the entry point. For reviewers who prefer to build context before
	// seeing usage.
	PresetBottomUp

	// PresetClusters groups snippets by connected component in the symbol
	// graph, then applies PresetTopDown within each cluster.
	// Largest cluster first (most-changed feature up front).
	PresetClusters
)

// OrderSnippets flattens the review forest into the selected preset order.
// It assigns Snippet.Depth for each snippet and returns a new ordered slice.
// The input slice is not modified.
//
// Structure comes from BuildForest: call edges define parent/child order and
// depth; import edges only group components. Top-down and clusters are the
// forest's pre-order (clustering is inherent — components are contiguous);
// bottom-up is its post-order.
func OrderSnippets(snippets []*Snippet, edges []SymbolEdge, preset Preset) []*Snippet {
	if len(snippets) == 0 {
		return snippets
	}
	order := OrderPre
	if preset == PresetBottomUp {
		order = OrderPost
	}
	return BuildForest(snippets, edges).Flatten(order)
}
