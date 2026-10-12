// Tree pane: expansion state, visible-row computation, and row rendering
// for the call-tree drill-down view.
package tui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/storymode7/astra/core"
)

// visRow is one visible row of the tree pane.
type visRow struct {
	node  *core.ReviewNode
	depth int
}

// treeState tracks which canonical nodes are expanded and which rows that
// makes visible.
type treeState struct {
	forest   *core.ReviewForest
	expanded map[string]bool
	visible  []visRow
	top      int // first tree row currently on screen
}

// autoExpandLimit: forests up to this many snippets start fully expanded;
// larger ones start with roots only.
const autoExpandLimit = 30

func newTreeState(forest *core.ReviewForest) *treeState {
	t := &treeState{forest: forest, expanded: make(map[string]bool)}
	if len(forest.ByID) <= autoExpandLimit {
		t.expandAllNodes()
	}
	t.recompute()
	return t
}

func (t *treeState) recompute() {
	t.visible = t.visible[:0]
	var walk func(n *core.ReviewNode, depth int)
	walk = func(n *core.ReviewNode, depth int) {
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

// renderTreeRow renders one tree row with its review status, truncated to
// width. The cursor row is drawn in inverse video. Annotations are short
// counts; the focus header lists the names.
func (t *treeState) renderTreeRow(row visRow, status string, cursor bool, width int) string {
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

	segs := []segment{
		{strings.Repeat("  ", row.depth) + marker + " ", ""},
		{plainGlyph(status), glyphColor(status)},
		{" ", ""},
	}
	switch {
	case s.NodeType == "imports":
		segs = append(segs, segment{s.FilePath, ""}, segment{" (imports)", dim})
	case s.SymbolPath == "":
		segs = append(segs, segment{s.FilePath, ""}, segment{" (hunks)", dim})
	default:
		segs = append(segs, segment{s.SymbolPath, bold}, segment{"  " + s.FilePath, dim})
	}

	var notes []string
	if n.Ref {
		notes = append(notes, "shown elsewhere")
	} else {
		if len(n.Children) > 0 && !t.expanded[s.ID] {
			notes = append(notes, plural(len(n.Children), "callee"))
		}
		if k := len(n.AlsoCalledBy); k > 0 {
			notes = append(notes, "+"+plural(k, "caller"))
		}
		if k := len(s.UnchangedCallers); k > 0 && n.Parent == nil {
			notes = append(notes, plural(k, "unchanged caller"))
		}
		if k := len(s.TestedBy); k > 0 {
			notes = append(notes, plural(k, "test"))
		}
	}
	if len(notes) > 0 {
		segs = append(segs, segment{"  · " + strings.Join(notes, " · "), dim})
	}

	if cursor {
		return "\033[7m" + renderSegments(segs, width, true) + "\033[27m"
	}
	line := renderSegments(segs, width, false)
	if n.Ref {
		line = dim + stripANSI(line) + reset
	}
	return line
}

// segment is a run of text with one style.
type segment struct{ text, style string }

// renderSegments joins styled segments, cutting the plain text at width
// (with "…") before styling so escape codes never split. plainOnly drops
// the styles (the inverse-video cursor row).
func renderSegments(segs []segment, width int, plainOnly bool) string {
	var b strings.Builder
	left := width - 1
	for _, sg := range segs {
		if left <= 0 {
			break
		}
		text := displaySafe(sg.text)
		if strWidth(text) > left {
			text = cutWidth(text, left-1) + "…"
		}
		left -= strWidth(text)
		if plainOnly || sg.style == "" {
			b.WriteString(text)
		} else {
			b.WriteString(sg.style + text + reset)
		}
	}
	return b.String()
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func glyphColor(status string) string {
	switch status {
	case "accepted":
		return green
	case "rejected":
		return red
	case "reviewed":
		return yellow
	default:
		return dim
	}
}

var reANSI = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")

func stripANSI(s string) string { return reANSI.ReplaceAllString(s, "") }

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
		w := runeWidth(r)
		if visible+w > width {
			return b.String() + reset
		}
		b.WriteRune(r)
		visible += w
	}
	return b.String()
}

// ansiWrap splits line into rows of at most width columns. Each
// continuation row repeats the line's gutter as blanks (up to and including
// "│ ") and re-applies the colours active at the split point.
func ansiWrap(line string, width int) []string {
	line = strings.ReplaceAll(line, "\t", "    ")
	plain := stripANSI(line)
	if width <= 0 || strWidth(plain) <= width {
		return []string{line}
	}
	indent, contWidth := "", width
	if i := strings.Index(plain, "│ "); i >= 0 {
		if n := strWidth(plain[:i]); n+2 < width/2 {
			indent = strings.Repeat(" ", n) + dim + "│" + reset + " "
			contWidth = width - n - 2
		}
	}

	var rows []string
	var b strings.Builder
	var active []string // SGR sequences in effect since the last reset
	visible, limit := 0, width
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		if runes[i] == 0x1b {
			j := i + 1
			for j < len(runes) && !((runes[j] >= 'a' && runes[j] <= 'z') || (runes[j] >= 'A' && runes[j] <= 'Z')) {
				j++
			}
			seq := string(runes[i:min(j+1, len(runes))])
			b.WriteString(seq)
			if seq == "\x1b[0m" || seq == "\x1b[m" {
				active = active[:0]
			} else if strings.HasSuffix(seq, "m") {
				active = append(active, seq)
			}
			i = j
			continue
		}
		w := runeWidth(runes[i])
		if visible+w > limit && visible > 0 {
			rows = append(rows, b.String()+reset)
			b.Reset()
			b.WriteString(indent + strings.Join(active, ""))
			visible, limit = 0, contWidth
		}
		b.WriteRune(runes[i])
		visible += w
	}
	return append(rows, b.String())
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
