package core

import (
	"strings"
)

// BuildEdgesFromDiff builds a heuristic symbol graph from snippet diff lines.
// It scans for import/require statements in added lines and creates an edge
// from the importing snippet to any snippet in the same diff whose file path
// matches the import target.
//
// This is a fast heuristic pass over the patch text. Full AST-based edge
// extraction (via wazero + tree-sitter queries) will supersede this when the
// wazero integration is complete.
func BuildEdgesFromDiff(snippets []*Snippet) []SymbolEdge {
	// Index snippets by file path for fast lookup.
	byFile := make(map[string][]*Snippet, len(snippets))
	for _, s := range snippets {
		byFile[s.FilePath] = append(byFile[s.FilePath], s)
	}

	var edges []SymbolEdge
	for _, s := range snippets {
		imports := extractImports(s.DiffLines, s.FilePath)
		for _, imp := range imports {
			// Try exact match and common extensions.
			for _, candidate := range importCandidates(imp) {
				targets, ok := byFile[candidate]
				if !ok {
					continue
				}
				for _, t := range targets {
					if t.ID != s.ID {
						edges = append(edges, SymbolEdge{From: s.ID, To: t.ID, Kind: EdgeImport})
					}
				}
			}
		}
	}
	return edges
}

// extractImports heuristically extracts imported file paths from diff lines.
// Handles JS/TS import/require, Python from-import, Go import strings.
func extractImports(lines []string, fromFile string) []string {
	dir := ""
	if idx := strings.LastIndex(fromFile, "/"); idx >= 0 {
		dir = fromFile[:idx]
	}

	var paths []string
	for _, line := range lines {
		if strings.HasPrefix(line, "-") {
			continue // skip removed lines
		}
		l := strings.TrimPrefix(line, "+")
		l = strings.TrimPrefix(l, " ")
		l = strings.TrimSpace(l)

		// JS/TS: import ... from './foo'  or  require('./foo')
		if p := extractQuoted(l, "from"); p != "" {
			paths = append(paths, resolveRelative(p, dir))
			continue
		}
		if p := extractQuoted(l, "require("); p != "" {
			paths = append(paths, resolveRelative(p, dir))
			continue
		}
		// Go: "some/package/path"
		if strings.HasPrefix(l, `"`) && strings.HasSuffix(l, `"`) {
			paths = append(paths, strings.Trim(l, `"`))
			continue
		}
		// Python: from .foo import bar
		if strings.HasPrefix(l, "from ") {
			parts := strings.Fields(l)
			if len(parts) >= 2 {
				paths = append(paths, strings.ReplaceAll(parts[1], ".", "/"))
			}
		}
	}
	return paths
}

func extractQuoted(line, keyword string) string {
	idx := strings.Index(line, keyword)
	if idx < 0 {
		return ""
	}
	rest := line[idx+len(keyword):]
	for _, quote := range []string{`"`, `'`} {
		s := strings.Index(rest, quote)
		if s < 0 {
			continue
		}
		e := strings.Index(rest[s+1:], quote)
		if e < 0 {
			continue
		}
		return rest[s+1 : s+1+e]
	}
	return ""
}

func resolveRelative(path, dir string) string {
	if !strings.HasPrefix(path, ".") {
		return path
	}
	combined := dir + "/" + path
	parts := strings.Split(combined, "/")
	var out []string
	for _, p := range parts {
		switch p {
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		case ".", "":
			// skip
		default:
			out = append(out, p)
		}
	}
	return strings.Join(out, "/")
}

// importCandidates returns plausible file paths for an import string,
// trying the bare path and common extensions.
func importCandidates(imp string) []string {
	candidates := []string{imp}
	for _, ext := range []string{".ts", ".tsx", ".js", ".jsx", ".go", ".py", ".rs", ".java"} {
		if !strings.HasSuffix(imp, ext) {
			candidates = append(candidates, imp+ext)
		}
	}
	candidates = append(candidates, imp+"/index.ts", imp+"/index.tsx", imp+"/index.js", imp+"/index.jsx")
	return candidates
}
