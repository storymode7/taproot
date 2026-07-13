// Tree pane: expansion state, visible-row computation, and row rendering
// for the call-tree drill-down view.
package tui

import (
	"fmt"
	"strings"

	"github.com/astra-review/astra/apps/cli/internal/engine"
	"github.com/astra-review/astra/apps/cli/internal/state"
)

// visRow is one visible row of the tree pane.
type visRow struct {
	node  *engine.ReviewNode
	depth int
}

// treeState tracks which canonical nodes are expanded and which rows that
// makes visible.
type treeState struct {
	forest   *engine.ReviewForest
	expanded map[string]bool
	visible  []visRow
	top      int // first tree row currently on screen
}

// autoExpandLimit: forests up to this many snippets start fully expanded;
// larger ones start with roots only.
const autoExpandLimit = 30

func newTreeState(forest *engine.ReviewForest) *treeState {
	t := &treeState{forest: forest, expanded: make(map[string]bool)}
	if len(forest.ByID) <= autoExpandLimit {
		t.expandAllNodes()
	}
	t.recompute()
	return t
}

func (t *treeState) recompute() {
	t.visible = t.visible[:0]
	var walk func(n *engine.ReviewNode, depth int)
	walk = func(n *engine.ReviewNode, depth int) {
		t.visible = append(t.visible, visRow{node: n, depth: depth})
		if n.Ref || !t.expanded[n.Snippet.ID] {
			return
		}
		for _, c := range n.Children {
			walk(c, depth+1)
		}
	}
	for _, r := range t.forest.Roots {
		walk(r, 0)
	}
}

func (t *treeState) expandAllNodes() {
	for id, n := range t.forest.ByID {
		if len(n.Children) > 0 {
			t.expanded[id] = true
		}
	}
}

func (t *treeState) collapseAllNodes() {
	t.expanded = make(map[string]bool)
}

// expandAncestors makes the canonical node for id reachable in the visible
// rows by expanding every node on its parent chain.
func (t *treeState) expandAncestors(id string) {
	n := t.forest.ByID[id]
	if n == nil {
		return
	}
	for p := n.Parent; p != nil; p = p.Parent {
		t.expanded[p.Snippet.ID] = true
	}
}

// indexOf returns the visible row index of the canonical node for id, or -1.
func (t *treeState) indexOf(id string) int {
	for i, row := range t.visible {
		if !row.node.Ref && row.node.Snippet.ID == id {
			return i
		}
	}
	return -1
}

// scrollTo adjusts the window top so row idx is within a pane of the given
// height.
func (t *treeState) scrollTo(idx, height int) {
	if idx < t.top {
		t.top = idx
	}
	if idx >= t.top+height {
		t.top = idx - height + 1
	}
	if t.top < 0 {
		t.top = 0
	}
}

// statusGlyph is the one-character review-state marker used in tree rows.
func statusGlyph(status string) string {
	switch status {
	case "accepted":
		return green + "✓" + reset
	case "rejected":
		return red + "✗" + reset
	case "reviewed":
		return yellow + "●" + reset
	default:
		return dim + "·" + reset
	}
}

// renderTreeRow renders one tree row, truncated to width. The cursor row is
// drawn in inverse video (uncolored, so the inversion stays intact).
func (t *treeState) renderTreeRow(row visRow, store *state.Store, cursor bool, width int) string {
	n := row.node
	s := n.Snippet

	marker := " "
	switch {
	case n.Ref:
		marker = "→"
	case len(n.Children) > 0 && t.expanded[s.ID]:
		marker = "▼"
	case len(n.Children) > 0:
		marker = "▶"
	}

	label := s.FilePath
	switch {
	case s.NodeType == "imports":
		label += " (imports)"
	case s.SymbolPath == "":
		label += " (hunks)"
	default:
		label += " ∷ " + s.SymbolPath
	}

	annotation := ""
	if n.Ref {
		annotation = " (shown elsewhere)"
	} else if len(n.AlsoCalledBy) > 0 {
		names := make([]string, 0, len(n.AlsoCalledBy))
		for _, id := range n.AlsoCalledBy {
			if c := t.forest.ByID[id]; c != nil && c.Snippet.SymbolPath != "" {
				names = append(names, c.Snippet.SymbolPath)
			}
		}
		if len(names) > 0 {
			annotation = " (also called by " + strings.Join(names, ", ") + ")"
		}
	}

	status := store.Get(s.ID).Status

	// Truncate the plain text before coloring so escape codes never split.
	plain := fmt.Sprintf("%s%s %s %s%s",
		strings.Repeat("  ", row.depth), marker, plainGlyph(status), label, annotation)
	if width > 1 && len([]rune(plain)) > width {
		plain = string([]rune(plain)[:width-1]) + "…"
	}

	if cursor {
		return "\033[7m" + plain + "\033[27m"
	}
	// Swap the plain glyph for its colored form (first occurrence only —
	// the glyph is always the marker's right-hand neighbour).
	colored := strings.Replace(plain,
		" "+plainGlyph(status)+" ", " "+statusGlyph(status)+" ", 1)
	if n.Ref {
		colored = dim + colored + reset
	}
	return colored
}

// ansiTruncate cuts a line to the given visible width, expanding tabs to
// four spaces and skipping ANSI escape sequences when counting. A reset is
// appended when anything was cut so colors never bleed to the next row.
func ansiTruncate(line string, width int) string {
	line = strings.ReplaceAll(line, "\t", "    ")
	if width <= 0 {
		return line
	}
	var b strings.Builder
	visible := 0
	inEscape := false
	for _, r := range line {
		if inEscape {
			b.WriteRune(r)
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
			continue
		}
		if r == 0x1b {
			inEscape = true
			b.WriteRune(r)
			continue
		}
		if visible >= width {
			return b.String() + reset
		}
		b.WriteRune(r)
		visible++
	}
	return b.String()
}

func plainGlyph(status string) string {
	switch status {
	case "accepted":
		return "✓"
	case "rejected":
		return "✗"
	case "reviewed":
		return "●"
	default:
		return "·"
	}
}
