// Package engine provides the snippet ordering engine.
package core

// OrderSnippets flattens the review forest in the given order: OrderPre
// puts callers first, OrderPost callees first. It assigns Snippet.Depth for
// each snippet and returns a new ordered slice. The input slice is not
// modified.
//
// Structure comes from BuildForest: call edges define parent/child order and
// depth; import edges only group components, which stay contiguous.
func OrderSnippets(snippets []*Snippet, edges []SymbolEdge, order FlattenOrder) []*Snippet {
	if len(snippets) == 0 {
		return snippets
	}
	return BuildForest(snippets, edges).Flatten(order)
}
