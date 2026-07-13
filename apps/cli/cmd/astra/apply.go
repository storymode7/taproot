package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/astra-review/astra/core"
)

// buildAcceptedPatch constructs a unified diff patch from the given accepted snippets.
//
// Snippets are grouped by file path and sorted by StartLine within each file so
// that git apply receives hunks in order. Returns an empty string when the input
// slice is empty.
func buildAcceptedPatch(accepted []*core.Snippet) string {
	if len(accepted) == 0 {
		return ""
	}

	// Group snippets by file path, preserving the order files were first seen.
	type fileGroup struct {
		oldPath  string
		filePath string
		isNew    bool
		snippets []*core.Snippet
	}

	groups := make(map[string]*fileGroup)
	fileOrder := make([]string, 0)

	for _, s := range accepted {
		if _, ok := groups[s.FilePath]; !ok {
			oldPath := s.OldPath
			if oldPath == "" {
				// Fallback: treat as a same-path change (non-rename).
				oldPath = s.FilePath
			}
			groups[s.FilePath] = &fileGroup{
				oldPath:  oldPath,
				filePath: s.FilePath,
				isNew:    s.IsNew,
			}
			fileOrder = append(fileOrder, s.FilePath)
		}
		groups[s.FilePath].snippets = append(groups[s.FilePath].snippets, s)
	}

	var b strings.Builder
	for _, filePath := range fileOrder {
		g := groups[filePath]

		// Sort hunks by start line ascending to keep the patch coherent.
		sort.Slice(g.snippets, func(i, j int) bool {
			return g.snippets[i].StartLine < g.snippets[j].StartLine
		})

		// Construct file header lines.
		var oldHeader string
		if g.isNew {
			oldHeader = "/dev/null"
		} else {
			oldHeader = "a/" + g.oldPath
		}
		newHeader := "b/" + g.filePath
		fmt.Fprintf(&b, "--- %s\n+++ %s\n", oldHeader, newHeader)

		// Append all hunk lines for this file.
		for _, s := range g.snippets {
			for _, line := range s.DiffLines {
				fmt.Fprintln(&b, line)
			}
		}
	}

	return b.String()
}
