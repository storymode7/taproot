package core

import "testing"

func makeSnippet(id, filePath string, depth int) *Snippet {
	return &Snippet{ID: id, FilePath: filePath, Depth: depth}
}

func snippetIDs(snippets []*Snippet) []string {
	ids := make([]string, len(snippets))
	for i, s := range snippets {
		ids[i] = s.ID
	}
	return ids
}

func TestOrderSnippets_Empty(t *testing.T) {
	result := OrderSnippets(nil, nil, OrderPre)
	if len(result) != 0 {
		t.Fatalf("expected empty result, got %d snippets", len(result))
	}
}

func TestOrderSnippets_NoEdges_SortedByFilePath(t *testing.T) {
	snippets := []*Snippet{
		{ID: "c", FilePath: "c.go"},
		{ID: "a", FilePath: "a.go"},
		{ID: "b", FilePath: "b.go"},
	}
	result := OrderSnippets(snippets, nil, OrderPre)
	ids := snippetIDs(result)
	expected := []string{"a", "b", "c"}
	for i, id := range expected {
		if ids[i] != id {
			t.Errorf("result[%d] = %q, want %q (ids: %v)", i, ids[i], id, ids)
		}
	}
}

func TestOrderSnippets_CallersFirst(t *testing.T) {
	// A → B (A calls B), callers first: A first (depth 0), then B (depth 1)
	snippets := []*Snippet{
		{ID: "B", FilePath: "b.go"},
		{ID: "A", FilePath: "a.go"},
	}
	edges := []SymbolEdge{{From: "A", To: "B"}}
	result := OrderSnippets(snippets, edges, OrderPre)
	if result[0].ID != "A" {
		t.Errorf("callers first: expected A first, got %s", result[0].ID)
	}
	if result[1].ID != "B" {
		t.Errorf("callers first: expected B second, got %s", result[1].ID)
	}
}

func TestOrderSnippets_CalleesFirst(t *testing.T) {
	// A → B, callees first: B first (deeper), A last
	snippets := []*Snippet{
		{ID: "A", FilePath: "a.go"},
		{ID: "B", FilePath: "b.go"},
	}
	edges := []SymbolEdge{{From: "A", To: "B"}}
	result := OrderSnippets(snippets, edges, OrderPost)
	if result[0].ID != "B" {
		t.Errorf("callees first: expected B first, got %s", result[0].ID)
	}
	if result[1].ID != "A" {
		t.Errorf("callees first: expected A second, got %s", result[1].ID)
	}
}

func TestOrderSnippets_DepthAssignment(t *testing.T) {
	// A → B → C
	snippets := []*Snippet{
		{ID: "A", FilePath: "a.go"},
		{ID: "B", FilePath: "b.go"},
		{ID: "C", FilePath: "c.go"},
	}
	edges := []SymbolEdge{{From: "A", To: "B"}, {From: "B", To: "C"}}
	result := OrderSnippets(snippets, edges, OrderPre)
	byID := make(map[string]*Snippet)
	for _, s := range result {
		byID[s.ID] = s
	}
	if byID["A"].Depth != 0 {
		t.Errorf("A.Depth = %d, want 0", byID["A"].Depth)
	}
	if byID["B"].Depth != 1 {
		t.Errorf("B.Depth = %d, want 1", byID["B"].Depth)
	}
	if byID["C"].Depth != 2 {
		t.Errorf("C.Depth = %d, want 2", byID["C"].Depth)
	}
}

func TestOrderSnippets_Cycles(t *testing.T) {
	// A ↔ B: a pure cycle still gets a deterministic order — the entry
	// point (degree tie broken by file path) comes first, the rest follow
	// as its callees.
	snippets := []*Snippet{
		{ID: "A", FilePath: "a.go"},
		{ID: "B", FilePath: "b.go"},
	}
	edges := []SymbolEdge{{From: "A", To: "B"}, {From: "B", To: "A"}}
	result := OrderSnippets(snippets, edges, OrderPre)
	if len(result) != 2 {
		t.Fatalf("expected 2 snippets, got %d", len(result))
	}
	if result[0].ID != "A" || result[1].ID != "B" {
		t.Errorf("cycle: expected entry A then B, got %v", snippetIDs(result))
	}
	if result[0].Depth != 0 || result[1].Depth != 1 {
		t.Errorf("cycle: expected depths 0,1 got %d,%d", result[0].Depth, result[1].Depth)
	}
}

func TestOrderSnippets_LargestComponentFirst(t *testing.T) {
	// Two disconnected components: {A,B} and {C}
	// {A,B} is larger → comes first
	snippets := []*Snippet{
		{ID: "A", FilePath: "a.go"},
		{ID: "B", FilePath: "b.go"},
		{ID: "C", FilePath: "c.go"},
	}
	edges := []SymbolEdge{{From: "A", To: "B"}}
	result := OrderSnippets(snippets, edges, OrderPre)
	if len(result) != 3 {
		t.Fatalf("expected 3 snippets, got %d", len(result))
	}
	// Cluster {A,B} (size 2) should appear before cluster {C} (size 1)
	ids := snippetIDs(result)
	cIdx := -1
	for i, id := range ids {
		if id == "C" {
			cIdx = i
		}
	}
	aIdx, bIdx := -1, -1
	for i, id := range ids {
		if id == "A" {
			aIdx = i
		}
		if id == "B" {
			bIdx = i
		}
	}
	if aIdx == -1 || bIdx == -1 || cIdx == -1 {
		t.Fatalf("unexpected ids: %v", ids)
	}
	if cIdx < aIdx || cIdx < bIdx {
		t.Errorf("expected C (smaller cluster) after A and B, got order: %v", ids)
	}
}

func TestOrderSnippets_DoesNotMutateInput(t *testing.T) {
	snippets := []*Snippet{
		{ID: "B", FilePath: "b.go"},
		{ID: "A", FilePath: "a.go"},
	}
	original := snippetIDs(snippets)
	OrderSnippets(snippets, nil, OrderPre)
	after := snippetIDs(snippets)
	for i := range original {
		if original[i] != after[i] {
			t.Errorf("input slice mutated at index %d: was %q, now %q", i, original[i], after[i])
		}
	}
}
