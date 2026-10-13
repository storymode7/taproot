// Review forest: the call-tree structure behind the drill-down review UX.
//
// Call edges (EdgeCall) define parent/child structure: a snippet's children
// are the changed entities its body calls, ordered by call site. Import
// edges never create structure; they only group snippets into components so
// related work stays adjacent.
//
// Every snippet appears exactly once as a canonical node. When a snippet is
// called from more than one changed entity (or takes part in a cycle), the
// extra callers get a Ref leaf pointing at the canonical node, and the
// canonical node records them in AlsoCalledBy — review state stays
// unambiguous, one node per snippet.
package core

import "sort"

// ReviewNode is one entry in the review forest.
type ReviewNode struct {
	Snippet *Snippet

	// Children are the changed entities this node calls, ordered by the
	// call site line in this node's body.
	Children []*ReviewNode

	// Parent is nil for roots.
	Parent *ReviewNode

	// AlsoCalledBy lists snippet IDs of changed callers beyond the parent
	// (set on canonical nodes only).
	AlsoCalledBy []string

	// Ref marks a reference leaf: this snippet is shown (canonically)
	// elsewhere in the forest. Ref nodes have no children and carry no
	// review state of their own.
	Ref bool
}

// ReviewForest is the full review structure for one diff.
type ReviewForest struct {
	// Roots holds, in review order: call-tree roots (code components
	// largest first, then test components), then non-entity snippets (imports, preamble, fallback hunks)
	// as trailing childless roots sorted by file, with the hunks of files in
	// other languages last.
	Roots []*ReviewNode

	// ByID maps snippet ID to its canonical node.
	ByID map[string]*ReviewNode
}

// FlattenOrder selects the traversal used by Flatten.
type FlattenOrder int

const (
	// OrderPre yields parents before children (callers first).
	OrderPre FlattenOrder = iota
	// OrderPost yields children before parents (callees first).
	OrderPost
)

// BuildForest builds the review forest from snippets and their edges.
//
// Roots are entities no other changed entity calls. Components that are
// pure cycles use the node with the greatest (out-degree − in-degree) as
// the entry point. Non-entity snippets trail the forest as childless roots.
func BuildForest(snippets []*Snippet, edges []SymbolEdge) *ReviewForest {
	byID := make(map[string]*Snippet, len(snippets))
	isTest := make(map[string]bool, len(snippets))
	for _, s := range snippets {
		byID[s.ID] = s
		isTest[s.ID] = s.IsTest || isTestPath(s.FilePath)
		s.TestedBy = nil
	}
	// Edges between test and non-test code never shape the tree: a test
	// calling code becomes a TestedBy note on the callee instead, so the
	// code's own entry points lead the review and tests follow it.
	crossesTests := func(e SymbolEdge) bool { return isTest[e.From] != isTest[e.To] }
	testedBy := make(map[string]map[string]string) // callee → test ID → name

	type callEdge struct {
		to   string
		line int
	}
	callAdj := make(map[string][]callEdge)
	callIn := make(map[string]int)
	callOut := make(map[string]int)
	inCallGraph := make(map[string]bool)

	for _, e := range edges {
		if e.Kind != EdgeCall {
			continue
		}
		if byID[e.From] == nil || byID[e.To] == nil {
			continue
		}
		if crossesTests(e) {
			if isTest[e.From] {
				if testedBy[e.To] == nil {
					testedBy[e.To] = make(map[string]string)
				}
				testedBy[e.To][e.From] = byID[e.From].SymbolPath
			}
			continue
		}
		callAdj[e.From] = append(callAdj[e.From], callEdge{to: e.To, line: e.CallLine})
		callIn[e.To]++
		callOut[e.From]++
		inCallGraph[e.From] = true
		inCallGraph[e.To] = true
	}
	for id, names := range testedBy {
		s := byID[id]
		for _, name := range names {
			s.TestedBy = append(s.TestedBy, name)
		}
		sort.Strings(s.TestedBy)
	}
	for _, adj := range callAdj {
		sort.Slice(adj, func(i, j int) bool {
			if adj[i].line != adj[j].line {
				return adj[i].line < adj[j].line
			}
			return adj[i].to < adj[j].to
		})
	}

	// Primary snippets take part in the call tree; the rest trail.
	isPrimary := func(s *Snippet) bool {
		if inCallGraph[s.ID] {
			return true
		}
		return s.SymbolPath != "" && s.NodeType != "imports"
	}

	// Union-find over all edges (call ∪ import) for component grouping.
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
	for _, e := range edges {
		if byID[e.From] == nil || byID[e.To] == nil || crossesTests(e) {
			continue
		}
		rf, rt := find(e.From), find(e.To)
		if rf != rt {
			parent[rf] = rt
		}
	}

	snippetLess := func(a, b *Snippet) bool {
		if a.FilePath != b.FilePath {
			return a.FilePath < b.FilePath
		}
		if a.StartLine != b.StartLine {
			return a.StartLine < b.StartLine
		}
		return a.ID < b.ID
	}

	// Component size (primary members) and first member, for root ordering.
	compSize := make(map[string]int)
	compFirst := make(map[string]*Snippet)
	for _, s := range snippets {
		if !isPrimary(s) {
			continue
		}
		root := find(s.ID)
		compSize[root]++
		if f, ok := compFirst[root]; !ok || snippetLess(s, f) {
			compFirst[root] = s
		}
	}

	forest := &ReviewForest{ByID: make(map[string]*ReviewNode, len(snippets))}

	var attach func(n *ReviewNode)
	attach = func(n *ReviewNode) {
		for _, ce := range callAdj[n.Snippet.ID] {
			target := byID[ce.to]
			if canonical, seen := forest.ByID[ce.to]; seen {
				// Already shown elsewhere (shared callee or cycle):
				// leave a reference leaf and annotate the canonical node.
				n.Children = append(n.Children, &ReviewNode{Snippet: target, Parent: n, Ref: true})
				canonical.AlsoCalledBy = append(canonical.AlsoCalledBy, n.Snippet.ID)
				continue
			}
			child := &ReviewNode{Snippet: target, Parent: n}
			forest.ByID[ce.to] = child
			n.Children = append(n.Children, child)
			attach(child)
		}
	}

	addRoot := func(s *Snippet) {
		root := &ReviewNode{Snippet: s}
		forest.ByID[s.ID] = root
		forest.Roots = append(forest.Roots, root)
		attach(root)
	}

	// Group primary snippets by component; order code components before
	// test components, then by size desc, then by their first member (path,
	// line), so components that tie on size keep one order from run to run.
	compMembers := make(map[string][]*Snippet)
	for _, s := range snippets {
		if isPrimary(s) {
			root := find(s.ID)
			compMembers[root] = append(compMembers[root], s)
		}
	}
	comps := make([]string, 0, len(compMembers))
	for c := range compMembers {
		comps = append(comps, c)
	}
	sort.Slice(comps, func(i, j int) bool {
		if ti, tj := isTest[comps[i]], isTest[comps[j]]; ti != tj {
			return tj
		}
		if compSize[comps[i]] != compSize[comps[j]] {
			return compSize[comps[i]] > compSize[comps[j]]
		}
		return snippetLess(compFirst[comps[i]], compFirst[comps[j]])
	})

	for _, c := range comps {
		members := compMembers[c]
		sort.Slice(members, func(i, j int) bool { return snippetLess(members[i], members[j]) })

		// Natural roots: primary members no changed entity calls.
		for _, s := range members {
			if callIn[s.ID] == 0 {
				addRoot(s)
			}
		}
		// Leftovers are cycle-only members. Repeatedly pick the best entry
		// point (max out−in degree, then path order) until all attached.
		for {
			var entry *Snippet
			for _, s := range members {
				if _, attached := forest.ByID[s.ID]; attached {
					continue
				}
				if entry == nil {
					entry = s
					continue
				}
				db, da := callOut[s.ID]-callIn[s.ID], callOut[entry.ID]-callIn[entry.ID]
				if db > da || (db == da && snippetLess(s, entry)) {
					entry = s
				}
			}
			if entry == nil {
				break
			}
			addRoot(entry)
		}
	}

	// Trailing non-entity snippets, grouped by file order. Files in other
	// languages come last, so the surfaces can show them as one group.
	var trailing []*Snippet
	for _, s := range snippets {
		if !isPrimary(s) {
			trailing = append(trailing, s)
		}
	}
	sort.Slice(trailing, func(i, j int) bool {
		if oi, oj := trailing[i].OtherLanguage(), trailing[j].OtherLanguage(); oi != oj {
			return oj
		}
		return snippetLess(trailing[i], trailing[j])
	})
	for _, s := range trailing {
		node := &ReviewNode{Snippet: s}
		forest.ByID[s.ID] = node
		forest.Roots = append(forest.Roots, node)
	}

	return forest
}

// Flatten returns the snippets in traversal order, skipping Ref leaves, and
// assigns Snippet.Depth from the tree depth.
func (f *ReviewForest) Flatten(order FlattenOrder) []*Snippet {
	out := make([]*Snippet, 0, len(f.ByID))
	var walk func(n *ReviewNode, depth int)
	walk = func(n *ReviewNode, depth int) {
		if n.Ref {
			return
		}
		n.Snippet.Depth = depth
		if order == OrderPre {
			out = append(out, n.Snippet)
		}
		for _, c := range n.Children {
			walk(c, depth+1)
		}
		if order == OrderPost {
			out = append(out, n.Snippet)
		}
	}
	for _, r := range f.Roots {
		walk(r, 0)
	}
	return out
}

// NextPending returns the canonical node after fromID (pre-order, wrapping
// around) for which isPending reports true, or nil when nothing is pending.
func (f *ReviewForest) NextPending(fromID string, isPending func(id string) bool) *ReviewNode {
	orderIDs := make([]string, 0, len(f.ByID))
	var walk func(n *ReviewNode)
	walk = func(n *ReviewNode) {
		if n.Ref {
			return
		}
		orderIDs = append(orderIDs, n.Snippet.ID)
		for _, c := range n.Children {
			walk(c)
		}
	}
	for _, r := range f.Roots {
		walk(r)
	}

	start := 0
	for i, id := range orderIDs {
		if id == fromID {
			start = i + 1
			break
		}
	}
	for i := 0; i < len(orderIDs); i++ {
		id := orderIDs[(start+i)%len(orderIDs)]
		if id != fromID && isPending(id) {
			return f.ByID[id]
		}
	}
	return nil
}
