// Process is the single JSON entry point the browser surfaces call through
// the WASM build (core/wasmmain). Keeping it in the core package (plain Go,
// no syscall/js) means the exact same code path is exercised by native tests
// and by the browser — the parity fixtures in testdata/parity assert that.
package core

import (
	"encoding/json"
	"fmt"
)

// ProcessRequest is the wire format of one review computation.
type ProcessRequest struct {
	Files   []WireFile     `json:"files"`
	Options ProcessOptions `json:"options"`
}

// ProcessOptions selects the flatten order: "pre" (top-down, default) or
// "post" (bottom-up).
type ProcessOptions struct {
	Order string `json:"order"`
}

// WireFile mirrors FileDiff with JSON-friendly types (Source as a string,
// not base64).
type WireFile struct {
	Path      string       `json:"path"`
	OldPath   string       `json:"oldPath"`
	Language  string       `json:"language"`
	IsNew     bool         `json:"isNew"`
	IsDeleted bool         `json:"isDeleted"`
	IsBinary  bool         `json:"isBinary"`
	Hunks     []WireHunk   `json:"hunks"`
	Source    string       `json:"source"`
	Entities  []WireEntity `json:"entities"`
}

// WireHunk mirrors Hunk.
type WireHunk struct {
	OldStart int      `json:"oldStart"`
	OldLines int      `json:"oldLines"`
	NewStart int      `json:"newStart"`
	NewLines int      `json:"newLines"`
	Lines    []string `json:"lines"`
}

// WireEntity mirrors Entity (pre-parsed spans from e.g. web-tree-sitter).
type WireEntity struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
}

// WireSnippet is a Snippet on the wire.
type WireSnippet struct {
	ID         string   `json:"id"`
	FilePath   string   `json:"filePath"`
	OldPath    string   `json:"oldPath"`
	Language   string   `json:"language"`
	SymbolPath string   `json:"symbolPath"`
	NodeType   string   `json:"nodeType"`
	IsNew      bool     `json:"isNew"`
	StartLine  int      `json:"startLine"`
	EndLine    int      `json:"endLine"`
	Depth      int      `json:"depth"`
	DiffLines  []string `json:"diffLines"`
}

// WireEdge is a SymbolEdge on the wire.
type WireEdge struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Kind     string `json:"kind"` // "call" | "import"
	CallLine int    `json:"callLine,omitempty"`
}

// WireNode is one review-forest node on the wire.
type WireNode struct {
	ID           string     `json:"id"`
	Ref          bool       `json:"ref,omitempty"`
	AlsoCalledBy []string   `json:"alsoCalledBy,omitempty"`
	Children     []WireNode `json:"children,omitempty"`
}

// ProcessResponse is the full result of one review computation.
type ProcessResponse struct {
	Snippets  []WireSnippet `json:"snippets"`
	Edges     []WireEdge    `json:"edges"`
	Tree      WireTree      `json:"tree"`
	FlatOrder []string      `json:"flatOrder"`
}

// WireTree wraps the forest roots.
type WireTree struct {
	Roots []WireNode `json:"roots"`
}

// Process runs the full pipeline: snippets → edges → forest → flatten.
func Process(req ProcessRequest) ProcessResponse {
	files := make([]*FileDiff, 0, len(req.Files))
	for _, wf := range req.Files {
		f := &FileDiff{
			Path:      wf.Path,
			OldPath:   wf.OldPath,
			Language:  wf.Language,
			IsNew:     wf.IsNew,
			IsDeleted: wf.IsDeleted,
			IsBinary:  wf.IsBinary,
			Source:    []byte(wf.Source),
		}
		for _, h := range wf.Hunks {
			f.Hunks = append(f.Hunks, Hunk{
				OldStart: h.OldStart, OldLines: h.OldLines,
				NewStart: h.NewStart, NewLines: h.NewLines,
				Lines: h.Lines,
			})
		}
		for _, e := range wf.Entities {
			f.Entities = append(f.Entities, Entity{
				Name: e.Name, Kind: e.Kind,
				StartLine: e.StartLine, EndLine: e.EndLine,
			})
		}
		files = append(files, f)
	}

	snippets := BuildSnippets(files)
	edges := append(BuildCallEdges(snippets), BuildEdgesFromDiff(snippets)...)
	forest := BuildForest(snippets, edges)

	order := OrderPre
	if req.Options.Order == "post" {
		order = OrderPost
	}
	flat := forest.Flatten(order)

	resp := ProcessResponse{}
	for _, s := range flat {
		resp.Snippets = append(resp.Snippets, WireSnippet{
			ID: s.ID, FilePath: s.FilePath, OldPath: s.OldPath,
			Language: s.Language, SymbolPath: s.SymbolPath,
			NodeType: s.NodeType, IsNew: s.IsNew,
			StartLine: s.StartLine, EndLine: s.EndLine, Depth: s.Depth,
			DiffLines: s.DiffLines,
		})
		resp.FlatOrder = append(resp.FlatOrder, s.ID)
	}
	for _, e := range edges {
		kind := "call"
		if e.Kind == EdgeImport {
			kind = "import"
		}
		resp.Edges = append(resp.Edges, WireEdge{From: e.From, To: e.To, Kind: kind, CallLine: e.CallLine})
	}
	for _, r := range forest.Roots {
		resp.Tree.Roots = append(resp.Tree.Roots, wireNode(r))
	}
	return resp
}

func wireNode(n *ReviewNode) WireNode {
	w := WireNode{ID: n.Snippet.ID, Ref: n.Ref, AlsoCalledBy: n.AlsoCalledBy}
	for _, c := range n.Children {
		w.Children = append(w.Children, wireNode(c))
	}
	return w
}

// ProcessJSON is the string-in/string-out form used by the WASM export.
func ProcessJSON(reqJSON string) (string, error) {
	var req ProcessRequest
	if err := json.Unmarshal([]byte(reqJSON), &req); err != nil {
		return "", fmt.Errorf("parse request: %w", err)
	}
	out, err := json.Marshal(Process(req))
	if err != nil {
		return "", fmt.Errorf("encode response: %w", err)
	}
	return string(out), nil
}
