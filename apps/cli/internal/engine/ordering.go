// Package engine provides the snippet ordering engine.
package engine

import "sort"

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

// SymbolEdge represents a directed import/call dependency from one snippet to another.
type SymbolEdge struct {
	From string // snippet ID
	To   string // snippet ID
}

// OrderSnippets reorders snippets according to the selected preset.
// It assigns Snippet.Depth for each snippet and returns a new ordered slice.
// The input slice is not modified.
func OrderSnippets(snippets []*Snippet, edges []SymbolEdge, preset Preset) []*Snippet {
	if len(snippets) == 0 {
		return snippets
	}

	// Build adjacency map and in-degree count.
	adj := make(map[string][]string, len(snippets))      // id → []id (forward edges)
	inDegree := make(map[string]int, len(snippets))

	for _, s := range snippets {
		if _, ok := adj[s.ID]; !ok {
			adj[s.ID] = nil
			inDegree[s.ID] = 0
		}
	}
	for _, e := range edges {
		adj[e.From] = append(adj[e.From], e.To)
		inDegree[e.To]++
	}

	// BFS from roots (in-degree 0) to assign depth.
	depth := make(map[string]int, len(snippets))
	visited := make(map[string]bool, len(snippets))
	queue := make([]string, 0, len(snippets))

	for _, s := range snippets {
		if inDegree[s.ID] == 0 {
			queue = append(queue, s.ID)
			visited[s.ID] = true
		}
	}

	for i := 0; i < len(queue); i++ {
		id := queue[i]
		d := depth[id]
		for _, neighbor := range adj[id] {
			if !visited[neighbor] {
				visited[neighbor] = true
				depth[neighbor] = d + 1
				queue = append(queue, neighbor)
			}
		}
	}

	// Assign max+1 depth to any snippet not reached (cycles / disconnected).
	maxDepth := 0
	for _, d := range depth {
		if d > maxDepth {
			maxDepth = d
		}
	}
	for _, s := range snippets {
		if !visited[s.ID] {
			depth[s.ID] = maxDepth + 1
		}
		s.Depth = depth[s.ID]
	}

	// Make a sorted copy.
	out := make([]*Snippet, len(snippets))
	copy(out, snippets)

	switch preset {
	case PresetTopDown:
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].Depth != out[j].Depth {
				return out[i].Depth < out[j].Depth
			}
			return out[i].FilePath < out[j].FilePath
		})

	case PresetBottomUp:
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].Depth != out[j].Depth {
				return out[i].Depth > out[j].Depth
			}
			return out[i].FilePath < out[j].FilePath
		})

	case PresetClusters:
		out = clusterOrder(out, adj)
	}

	return out
}

// ---------------------------------------------------------------------------
// Cluster ordering: union-find connected components
// ---------------------------------------------------------------------------

func clusterOrder(snippets []*Snippet, adj map[string][]string) []*Snippet {
	parent := make(map[string]string, len(snippets))
	for _, s := range snippets {
		parent[s.ID] = s.ID
	}

	var find func(string) string
	find = func(x string) string {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}

	union := func(x, y string) {
		rx, ry := find(x), find(y)
		if rx != ry {
			parent[rx] = ry
		}
	}

	// Union both directions to get undirected components.
	for from, neighbors := range adj {
		for _, to := range neighbors {
			union(from, to)
		}
	}

	// Group snippets by component root.
	groups := make(map[string][]*Snippet)
	for _, s := range snippets {
		root := find(s.ID)
		groups[root] = append(groups[root], s)
	}

	// Collect components and sort each by depth (top-down within cluster).
	components := make([][]*Snippet, 0, len(groups))
	for _, g := range groups {
		sort.SliceStable(g, func(i, j int) bool {
			if g[i].Depth != g[j].Depth {
				return g[i].Depth < g[j].Depth
			}
			return g[i].FilePath < g[j].FilePath
		})
		components = append(components, g)
	}

	// Sort components largest-first (biggest feature cluster up front).
	sort.SliceStable(components, func(i, j int) bool {
		return len(components[i]) > len(components[j])
	})

	out := make([]*Snippet, 0, len(snippets))
	for _, c := range components {
		out = append(out, c...)
	}
	return out
}
