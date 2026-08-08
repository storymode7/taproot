package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/astra-review/astra/core"
)

// buildAcceptedPatch constructs a unified diff patch from the given accepted snippets.
//
// Snippets are grouped by file path; each snippet's DiffLines are split back
// into their @@ fragments and the fragments sorted by old start line so that
// git apply receives hunks in ascending order — a class-container snippet can
// hold fragments on both sides of its methods' snippets, so sorting whole
// snippets is not enough. Returns an empty string when the input slice is
// empty.
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

		// Collect every snippet's fragments and sort them ascending.
		var frags []fragment
		for _, s := range g.snippets {
			frags = append(frags, splitFragments(s)...)
		}
		sort.SliceStable(frags, func(i, j int) bool {
			if frags[i].oldStart != frags[j].oldStart {
				return frags[i].oldStart < frags[j].oldStart
			}
			return frags[i].newStart < frags[j].newStart
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
		for _, f := range frags {
			for _, line := range f.lines {
				fmt.Fprintln(&b, line)
			}
		}
	}

	return b.String()
}

// fragment is one @@-headed run of diff lines within a snippet.
type fragment struct {
	oldStart int
	newStart int
	lines    []string
}

var reHunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// splitFragments cuts a snippet's DiffLines at @@ headers. Lines before the
// first header (shouldn't happen) keep the snippet's own start line.
func splitFragments(s *core.Snippet) []fragment {
	var frags []fragment
	cur := &fragment{oldStart: s.StartLine, newStart: s.StartLine}
	for _, line := range s.DiffLines {
		if m := reHunkHeader.FindStringSubmatch(line); m != nil {
			if len(cur.lines) > 0 {
				frags = append(frags, *cur)
			}
			oldStart, _ := strconv.Atoi(m[1])
			newStart, _ := strconv.Atoi(m[2])
			cur = &fragment{oldStart: oldStart, newStart: newStart}
		}
		cur.lines = append(cur.lines, line)
	}
	if len(cur.lines) > 0 {
		frags = append(frags, *cur)
	}
	return frags
}
