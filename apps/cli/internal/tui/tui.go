// Package tui provides the terminal UI for the Astra CLI.
//
// It uses only the Go standard library — no TUI framework dependency.
// Output is ANSI-escaped plain text with keyboard control via raw terminal mode.
package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/astra-review/astra/apps/cli/internal/state"
	"github.com/astra-review/astra/core"
)

// Colors (ANSI escape codes, reset-safe)
const (
	reset    = "\033[0m"
	bold     = "\033[1m"
	dim      = "\033[2m"
	red      = "\033[31m"
	green    = "\033[32m"
	yellow   = "\033[33m"
	blue     = "\033[34m"
	cyan     = "\033[36m"
	white    = "\033[37m"
	bgGreen  = "\033[42m"
	bgRed    = "\033[41m"
	bgYellow = "\033[43m"
)

// View renders the full list of snippets to stdout.
// This is the non-interactive "print and exit" mode used when stdout is
// not a TTY (e.g. piped to `less`).
func View(snippets []*core.Snippet, store *state.Store) {
	for i, s := range snippets {
		st := store.Get(s.ID)
		// Indent by call-tree depth so the drill-down structure survives
		// in plain output.
		printSnippetHeader(i+1, len(snippets), s, st.Status, strings.Repeat("  ", s.Depth))
		printDiffLines(s.FilePath, s.DiffLines)
		fmt.Println()
	}
}

func printSnippetHeader(idx, total int, s *core.Snippet, status, indent string) {
	statusBadge := statusBadge(status)
	depth := fmt.Sprintf("depth:%d", s.Depth)

	fmt.Printf("\n%s%s─── %d/%d%s  %s%s%s  %s%s%s  %s%s%s\n",
		indent,
		dim, idx, total, reset,
		bold+cyan, s.FilePath, reset,
		dim, depth, reset,
		dim, s.Language, reset,
	)
	fmt.Printf("%s    %s%s\n", indent, statusBadge, reset)

	if s.SymbolPath != "" {
		fmt.Printf("%s%s  ∷ %s%s\n", indent, dim, s.SymbolPath, reset)
	}
	if len(s.UnchangedCallers) > 0 {
		fmt.Printf("%s%s  called by unchanged %s%s\n", indent, dim, strings.Join(s.UnchangedCallers, ", "), reset)
	}
	fmt.Println(strings.Repeat("─", 72))
}

func printDiffLines(filePath string, lines []string) {
	// Delta must write directly to os.Stdout so it sees the real TTY — auto-detecting
	// terminal width, the user's theme, and emitting correct background fills / \033[K.
	if activeHighlighter == HighlightDelta {
		if err := runDeltaDirect(os.Stdout, filePath, lines); err == nil {
			return
		}
		// fall through to bat/builtin if delta fails
	}
	for _, line := range highlightLines(filePath, lines) {
		fmt.Println(line)
	}
}

func statusBadge(status string) string {
	switch status {
	case "accepted":
		return bgGreen + " ✓ accepted "
	case "rejected":
		return bgRed + " ✗ rejected "
	case "reviewed":
		return bgYellow + " ● reviewed "
	default:
		return dim + " · pending "
	}
}

// ---------------------------------------------------------------------------
// Interactive mode (raw terminal) — call-tree drill-down view
// ---------------------------------------------------------------------------

// Runner holds interactive session state.
type Runner struct {
	forest      *core.ReviewForest
	flat        []*core.Snippet // pre-order canonical snippets (]/[ nav, progress)
	store       *state.Store
	autoAdvance bool // jump to the next pending snippet after accept/reject/reviewed
	applyMode   bool // stage accepted hunks on quit (--apply mode)
	onQuit      func() error

	tree       *treeState
	cursor     int // index into tree.visible
	diffScroll int
	diffCache  map[string][]string // rendered diff lines per snippet ID
	cacheCols  int                 // width the cache was rendered at
}

// NewRunner creates a Runner for interactive tree review.
// autoAdvance controls whether the cursor jumps to the next pending snippet
// after the user accepts, rejects, or marks a snippet as reviewed.
// applyMode enables git-staging on quit; onQuit is the staging callback (may be nil).
func NewRunner(forest *core.ReviewForest, store *state.Store, autoAdvance, applyMode bool, onQuit func() error) *Runner {
	return &Runner{
		forest:      forest,
		flat:        forest.Flatten(core.OrderPre),
		store:       store,
		autoAdvance: autoAdvance,
		applyMode:   applyMode,
		onQuit:      onQuit,
		tree:        newTreeState(forest),
		diffCache:   make(map[string][]string),
	}
}

// Run starts the interactive loop. Returns when the user quits (q).
func (r *Runner) Run() error {
	raw, err := enableRawMode()
	if err != nil {
		// Fall back to non-interactive view if we can't get raw mode.
		View(r.flat, r.store)
		return nil
	}
	defer raw.restore()

	r.render()

	for {
		key := readKey()
		if key == "" {
			break
		}

		switch key {
		case "q", "Q":
			fmt.Print("\033[?25h") // show cursor
			if r.applyMode && r.onQuit != nil {
				if err := r.onQuit(); err != nil {
					fmt.Fprintf(os.Stderr, "\nastra: apply failed: %v\n", err)
				}
			}
			return nil
		case "j", "\033[B":
			r.moveCursor(1)
		case "k", "\033[A":
			r.moveCursor(-1)
		case "l", "\033[C", "\r": // expand / drill in / follow ref
			r.drillIn()
		case "h", "\033[D": // collapse / jump to parent
			r.drillOut()
		case "y", "Y", "a", "A": // y/a = accept (y mirrors git add -p)
			r.setStatus("accepted")
		case "n", "N", "x", "X": // n/x = reject (n mirrors git add -p)
			r.setStatus("rejected")
		case "r", "R":
			r.setStatus("reviewed")
		case "]":
			r.gotoPending(1)
		case "[":
			r.gotoPending(-1)
		case "J":
			r.diffScroll += 5
			r.render()
		case "K":
			r.diffScroll -= 5
			if r.diffScroll < 0 {
				r.diffScroll = 0
			}
			r.render()
		case "E":
			r.tree.expandAllNodes()
			r.refocus()
		case "C":
			r.collapseAll()
		case "\t": // Tab toggles auto-advance
			r.autoAdvance = !r.autoAdvance
			r.render()
		case "?":
			r.renderHelp()
		}
	}
	return nil
}

// readKey reads one keypress. Reading byte-by-byte (instead of a 3-byte
// buffer) keeps rapid keystrokes from batching into one unrecognized chunk;
// an ESC byte pulls the rest of its arrow-key sequence.
func readKey() string {
	var b [1]byte
	n, err := os.Stdin.Read(b[:])
	if err != nil || n == 0 {
		return ""
	}
	if b[0] != 0x1b {
		return string(b[:1])
	}
	var seq [2]byte
	n, _ = os.Stdin.Read(seq[:])
	return "\033" + string(seq[:n])
}

func (r *Runner) currentNode() *core.ReviewNode {
	if len(r.tree.visible) == 0 {
		return nil
	}
	if r.cursor >= len(r.tree.visible) {
		r.cursor = len(r.tree.visible) - 1
	}
	return r.tree.visible[r.cursor].node
}

func (r *Runner) moveCursor(delta int) {
	next := r.cursor + delta
	if next < 0 || next >= len(r.tree.visible) {
		return
	}
	r.cursor = next
	r.diffScroll = 0
	r.render()
}

// drillIn expands the current node, or moves to its first child when already
// expanded, or jumps to the canonical node when on a reference leaf.
func (r *Runner) drillIn() {
	n := r.currentNode()
	if n == nil {
		return
	}
	if n.Ref {
		r.focusNode(n.Snippet.ID)
		r.render()
		return
	}
	if len(n.Children) == 0 {
		return
	}
	if !r.tree.expanded[n.Snippet.ID] {
		r.tree.expanded[n.Snippet.ID] = true
		r.tree.recompute()
	} else {
		// First child is the next visible row.
		r.moveCursor(1)
		return
	}
	r.render()
}

// drillOut collapses the current node, or jumps to its parent when already
// collapsed (or a leaf).
func (r *Runner) drillOut() {
	n := r.currentNode()
	if n == nil {
		return
	}
	if !n.Ref && len(n.Children) > 0 && r.tree.expanded[n.Snippet.ID] {
		delete(r.tree.expanded, n.Snippet.ID)
		r.tree.recompute()
		r.render()
		return
	}
	if n.Parent != nil {
		r.focusNode(n.Parent.Snippet.ID)
		r.render()
	}
}

// focusNode moves the cursor to the canonical node for id, expanding
// ancestors as needed.
func (r *Runner) focusNode(id string) {
	r.tree.expandAncestors(id)
	r.tree.recompute()
	if idx := r.tree.indexOf(id); idx >= 0 {
		r.cursor = idx
	}
	r.diffScroll = 0
}

func (r *Runner) collapseAll() {
	// Keep focus on the current node's root ancestor.
	n := r.currentNode()
	r.tree.collapseAllNodes()
	r.tree.recompute()
	if n != nil {
		root := n
		for root.Parent != nil {
			root = root.Parent
		}
		if idx := r.tree.indexOf(root.Snippet.ID); idx >= 0 {
			r.cursor = idx
		}
	}
	r.diffScroll = 0
	r.render()
}

// refocus keeps the cursor on the same snippet after a structural change.
func (r *Runner) refocus() {
	n := r.currentNode()
	r.tree.recompute()
	if n != nil {
		if idx := r.tree.indexOf(n.Snippet.ID); idx >= 0 {
			r.cursor = idx
		}
	}
	r.render()
}

func (r *Runner) setStatus(status string) {
	n := r.currentNode()
	if n == nil {
		return
	}
	st := r.store.Get(n.Snippet.ID)
	_ = r.store.Set(n.Snippet.ID, status, st.Comment)
	if r.autoAdvance {
		r.gotoPending(1)
		return
	}
	r.render()
}

// gotoPending moves to the next (dir > 0) or previous (dir < 0) pending
// snippet in pre-order, wrapping around.
func (r *Runner) gotoPending(dir int) {
	n := r.currentNode()
	if n == nil || len(r.flat) == 0 {
		return
	}
	cur := 0
	for i, s := range r.flat {
		if s.ID == n.Snippet.ID {
			cur = i
			break
		}
	}
	for i := 1; i <= len(r.flat); i++ {
		idx := (cur + dir*i%len(r.flat) + len(r.flat)) % len(r.flat)
		s := r.flat[idx]
		if s.ID != n.Snippet.ID && r.store.Get(s.ID).Status == "pending" {
			r.focusNode(s.ID)
			break
		}
	}
	r.render()
}

// renderedDiff returns (and caches) the highlighted diff lines for a snippet.
func (r *Runner) renderedDiff(s *core.Snippet, width int) []string {
	if lines, ok := r.diffCache[s.ID]; ok {
		return lines
	}
	var lines []string
	if activeHighlighter == HighlightDelta {
		if out, err := runDeltaCapture(s.FilePath, s.DiffLines, width); err == nil {
			lines = out
		}
	}
	if lines == nil {
		lines = highlightLines(s.FilePath, s.DiffLines)
	}
	r.diffCache[s.ID] = lines
	return lines
}

func (r *Runner) render() {
	rows, cols := terminalSize()
	if rows <= 0 {
		rows = 40
	}
	if cols <= 0 {
		cols = 80
	}
	// Rendered diff lines are wrapped at the terminal width — a resize
	// invalidates them.
	if cols != r.cacheCols {
		r.diffCache = make(map[string][]string)
		r.cacheCols = cols
	}

	// Layout: header(1) + rule(1) + tree(treeH) + focus rule(1) + diff + hints(1).
	treeH := rows / 3
	if treeH < 8 {
		treeH = 8
	}
	if len(r.tree.visible) < treeH {
		treeH = len(r.tree.visible)
	}
	if max := rows - 10; treeH > max && max >= 3 {
		treeH = max
	}
	// Budget: header(1) + rule(1) + tree + focus rule(1) + diff +
	// more-indicator(1) + hint(1, no trailing newline).
	diffH := rows - treeH - 5
	if diffH < 3 {
		diffH = 3
	}

	// Hide cursor during redraw, clear screen, move to top.
	fmt.Print("\033[?25l\033[2J\033[H")

	// Header: progress + toggles.
	done := 0
	for _, s := range r.flat {
		if r.store.Get(s.ID).Status != "pending" {
			done++
		}
	}
	total := len(r.flat)
	pct := 0
	if total > 0 {
		pct = done * 100 / total
	}
	filled := pct * 20 / 100
	bar := strings.Repeat("█", filled) + strings.Repeat("░", 20-filled)
	autoLabel := "auto:on"
	if !r.autoAdvance {
		autoLabel = "auto:off"
	}
	applyLabel := ""
	if r.applyMode {
		applyLabel = "  " + yellow + "[apply mode]" + reset
	}
	fmt.Printf("%s Astra%s  %d/%d reviewed %s%s%s %d%%  %s%s%s%s\n",
		bold+blue+"⭐", reset, done, total, dim, bar, reset, pct,
		dim, autoLabel, reset, applyLabel)
	fmt.Println(dim + strings.Repeat("─", cols) + reset)

	// Tree pane.
	r.tree.scrollTo(r.cursor, treeH)
	for i := r.tree.top; i < r.tree.top+treeH && i < len(r.tree.visible); i++ {
		fmt.Println(r.tree.renderTreeRow(r.tree.visible[i], r.store, i == r.cursor, cols))
	}

	// Focus rule + diff pane.
	n := r.currentNode()
	if n != nil {
		s := n.Snippet
		focus := fmt.Sprintf("── %s", s.FilePath)
		if s.SymbolPath != "" {
			focus += " ∷ " + s.SymbolPath
		}
		focus += " · " + r.store.Get(s.ID).Status + " "
		if pad := cols - len([]rune(focus)); pad > 0 {
			focus += strings.Repeat("─", pad)
		}
		fmt.Println(dim + focus + reset)

		lines := r.renderedDiff(s, cols)
		if r.diffScroll > len(lines)-1 {
			r.diffScroll = len(lines) - 1
		}
		if r.diffScroll < 0 {
			r.diffScroll = 0
		}
		end := r.diffScroll + diffH
		if end > len(lines) {
			end = len(lines)
		}
		for _, l := range lines[r.diffScroll:end] {
			fmt.Println(ansiTruncate(l, cols))
		}
		if end < len(lines) {
			fmt.Printf("%s… %d more lines (J/K to scroll)%s\n", dim, len(lines)-end, reset)
		}
	}

	// Hint line (truncated so it can never wrap and push the header off).
	hints := "j/k move · l/h drill in/out · y/n/r judge · ]/[ pending · J/K scroll · E/C fold · tab auto · ? help · q quit"
	fmt.Print(dim + ansiTruncate(hints, cols-1) + reset)
}

func (r *Runner) renderHelp() {
	fmt.Print("\033[2J\033[H")
	fmt.Println(bold + "⭐ Astra keyboard shortcuts" + reset)
	fmt.Println()
	fmt.Println("  j / ↓       Move down the tree")
	fmt.Println("  k / ↑       Move up the tree")
	fmt.Println("  l / → / ⏎   Expand node; if expanded, move to first callee;")
	fmt.Println("              on a → reference, jump to where it's shown")
	fmt.Println("  h / ←       Collapse node; if collapsed, jump to caller")
	fmt.Println("  y / a       Accept snippet")
	fmt.Println("  n / x       Reject snippet")
	fmt.Println("  r           Mark as reviewed (no judgement)")
	fmt.Println("  ] / [       Next / previous pending snippet")
	fmt.Println("  J / K       Scroll the diff pane")
	fmt.Println("  E / C       Expand / collapse the whole tree")
	fmt.Println("  tab         Toggle auto-advance (jump to next pending after judging)")
	fmt.Println("  q           Quit")
	fmt.Println("  ?           Show this help")
	fmt.Println()
	fmt.Println(dim + "The tree is the call graph of changed code: children are the" + reset)
	fmt.Println(dim + "functions/types the focused snippet calls. Roots are entry points" + reset)
	fmt.Println(dim + "nothing else in the diff calls." + reset)
	fmt.Println()
	fmt.Println(dim + "Note: accept/reject records state locally in .git/astra/state.json." + reset)
	fmt.Println(dim + "      Use --apply (working tree mode only) to stage accepted hunks on quit." + reset)
	fmt.Println()
	fmt.Println("Press any key to continue…")
	buf := make([]byte, 1)
	_, _ = os.Stdin.Read(buf)
	r.render()
}
