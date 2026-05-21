// Package tui provides the terminal UI for the Astra CLI.
//
// It uses only the Go standard library — no TUI framework dependency.
// Output is ANSI-escaped plain text with keyboard control via raw terminal mode.
package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/astra-review/astra/apps/cli/internal/engine"
	"github.com/astra-review/astra/apps/cli/internal/state"
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
func View(snippets []*engine.Snippet, store *state.Store) {
	for i, s := range snippets {
		st := store.Get(s.ID)
		printSnippetHeader(i+1, len(snippets), s, st.Status)
		printDiffLines(s.FilePath, s.DiffLines)
		fmt.Println()
	}
}

func printSnippetHeader(idx, total int, s *engine.Snippet, status string) {
	statusBadge := statusBadge(status)
	depth := fmt.Sprintf("depth:%d", s.Depth)

	fmt.Printf("\n%s─── %d/%d%s  %s%s%s  %s%s%s  %s%s%s\n",
		dim, idx, total, reset,
		bold+cyan, s.FilePath, reset,
		dim, depth, reset,
		dim, s.Language, reset,
	)
	fmt.Printf("    %s%s\n", statusBadge, reset)

	if s.SymbolPath != "" {
		fmt.Printf("%s  ∷ %s%s\n", dim, s.SymbolPath, reset)
	}
	fmt.Println(strings.Repeat("─", 72))
}

func printDiffLines(filePath string, lines []string) {
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
// Interactive mode (raw terminal)
// ---------------------------------------------------------------------------

// Runner holds interactive session state.
type Runner struct {
	snippets []*engine.Snippet
	store    *state.Store
	preset   engine.Preset
	index    int // current snippet index
}

// NewRunner creates a Runner for interactive review.
func NewRunner(snippets []*engine.Snippet, store *state.Store, preset engine.Preset) *Runner {
	return &Runner{snippets: snippets, store: store, preset: preset}
}

// Run starts the interactive loop. Returns when the user quits (q) or
// reaches the last snippet.
func (r *Runner) Run() error {
	raw, err := enableRawMode()
	if err != nil {
		// Fall back to non-interactive view if we can't get raw mode.
		View(r.snippets, r.store)
		return nil
	}
	defer raw.restore()

	r.render()

	for {
		b := make([]byte, 3)
		n, err := os.Stdin.Read(b)
		if err != nil || n == 0 {
			break
		}
		key := string(b[:n])

		switch key {
		case "q", "Q":
			fmt.Print("\033[?25h") // show cursor
			return nil
		case "j", "\033[B": // j or down arrow
			if r.index < len(r.snippets)-1 {
				r.index++
				r.render()
			}
		case "k", "\033[A": // k or up arrow
			if r.index > 0 {
				r.index--
				r.render()
			}
		case "a", "A":
			r.setStatus("accepted")
		case "x", "X":
			r.setStatus("rejected")
		case "r", "R":
			r.setStatus("reviewed")
		case "1":
			r.preset = engine.PresetTopDown
			r.reorder()
		case "2":
			r.preset = engine.PresetBottomUp
			r.reorder()
		case "3":
			r.preset = engine.PresetClusters
			r.reorder()
		case "?", "h":
			r.renderHelp()
		}
	}
	return nil
}

func (r *Runner) setStatus(status string) {
	s := r.snippets[r.index]
	st := r.store.Get(s.ID)
	_ = r.store.Set(s.ID, status, st.Comment)
	r.render()
}

func (r *Runner) reorder() {
	// Re-order is handled upstream in the CLI command by re-calling OrderSnippets.
	// For now, just re-render with updated preset label.
	r.render()
}

func (r *Runner) render() {
	// Hide cursor during redraw, clear screen, move to top.
	fmt.Print("\033[?25l\033[2J\033[H")

	s := r.snippets[r.index]
	st := r.store.Get(s.ID)

	// Header bar
	presetName := [...]string{"top-down", "bottom-up", "clusters"}[r.preset]
	fmt.Printf("%s Astra  %s[%d/%d]%s  preset:%s  %s q=quit j/k=nav a=accept x=reject r=reviewed ?=help%s\n",
		bold+blue+"⭐", yellow, r.index+1, len(r.snippets), reset,
		presetName, dim, reset)
	fmt.Println(strings.Repeat("─", 80))

	printSnippetHeader(r.index+1, len(r.snippets), s, st.Status)
	printDiffLines(s.FilePath, s.DiffLines)

	// Progress bar
	done := 0
	for _, sn := range r.snippets {
		if st2 := r.store.Get(sn.ID); st2.Status != "pending" {
			done++
		}
	}
	pct := done * 100 / len(r.snippets)
	filled := pct * 40 / 100
	bar := strings.Repeat("█", filled) + strings.Repeat("░", 40-filled)
	fmt.Printf("\n%s[%s]%s %d%% (%d/%d reviewed)%s\n",
		dim, bar, reset, pct, done, len(r.snippets), reset)
}

func (r *Runner) renderHelp() {
	fmt.Print("\033[2J\033[H")
	fmt.Println(bold + "⭐ Astra keyboard shortcuts" + reset)
	fmt.Println()
	fmt.Println("  j / ↓       Next snippet")
	fmt.Println("  k / ↑       Previous snippet")
	fmt.Println("  a           Accept snippet")
	fmt.Println("  x           Reject snippet")
	fmt.Println("  r           Mark as reviewed (no judgement)")
	fmt.Println("  1           Switch to top-down preset")
	fmt.Println("  2           Switch to bottom-up preset")
	fmt.Println("  3           Switch to feature-clusters preset")
	fmt.Println("  q           Quit")
	fmt.Println("  ? / h       Show this help")
	fmt.Println()
	fmt.Println("Press any key to continue…")
	buf := make([]byte, 1)
	_, _ = os.Stdin.Read(buf)
	r.render()
}
