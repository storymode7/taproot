package core

import "testing"

func TestBuildEdgesFromDiff_FolderImportReachesIndex(t *testing.T) {
	for _, index := range []string{"index.ts", "index.tsx", "index.js", "index.jsx"} {
		home := &Snippet{ID: "home", FilePath: "src/pages/Home.tsx", NodeType: "imports",
			DiffLines: []string{`+import { Button } from "../components/Button";`}}
		button := &Snippet{ID: "button", FilePath: "src/components/Button/" + index, SymbolPath: "Button"}
		edges := BuildEdgesFromDiff([]*Snippet{home, button})
		if len(edges) != 1 || edges[0].From != "home" || edges[0].To != "button" || edges[0].Kind != EdgeImport {
			t.Errorf("%s: edges = %v, want home -> button import", index, edges)
		}
	}
}
