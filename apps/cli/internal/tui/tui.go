// Package tui provides the terminal UI for the Astra CLI.
//
// It uses only the Go standard library — no TUI framework dependency.
// Output is ANSI-escaped plain text with keyboard control via raw terminal mode.
package tui

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/storymode7/astra/apps/cli/internal/state"
	"github.com/storymode7/astra/core"
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

// Plain makes View print uncolored text with +/- markers: set when stdout
// is not a terminal or NO_COLOR is set.
var Plain bool

// View renders the full list of snippets to stdout.
// This is the non-interactive "print and exit" mode used when stdout is
// not a TTY (e.g. piped to `less`).
func View(snippets []*core.Snippet, store *state.Store) {
	if Plain {
		plainView(snippets, store)
		return
	}
	for i, s := range snippets {
		st := store.Get(state.Key(s.ID, s.DiffLines))
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
		bold+cyan, fmt.Sprintf("%s:%d", displaySafe(s.FilePath), firstChangedLine(s)), reset,
		dim, depth, reset,
		dim, s.Language, reset,
	)
	fmt.Printf("%s    %s%s\n", indent, statusBadge, reset)

	if s.SymbolPath != "" {
		fmt.Printf("%s%s  ∷ %s%s\n", indent, dim, displaySafe(s.SymbolPath), reset)
	}
	for _, note := range snippetNotes(s, nil) {
		fmt.Printf("%s%s  %s%s\n", indent, dim, note, reset)
	}
	fmt.Println(strings.Repeat("─", 72))
}

func plainView(snippets []*core.Snippet, store *state.Store) {
	for i, s := range snippets {
		indent := strings.Repeat("  ", s.Depth)
		title := fmt.Sprintf("%s:%d", displaySafe(s.FilePath), firstChangedLine(s))
		if s.SymbolPath != "" {
			title += " :: " + displaySafe(s.SymbolPath)
		}
		fmt.Printf("%s--- %d/%d  %s  [%s]\n", indent, i+1, len(snippets), title, store.Get(state.Key(s.ID, s.DiffLines)).Status)
		for _, note := range snippetNotes(s, nil) {
			fmt.Printf("%s    %s\n", indent, note)
		}
		for _, l := range s.DiffLines {
			fmt.Println(displaySafe(l))
		}
		fmt.Println()
	}
}

// snippetNotes lists who else calls s and which tests cover it. alsoCalledBy
// holds the symbol paths of changed callers beyond the tree parent.
func snippetNotes(s *core.Snippet, alsoCalledBy []string) []string {
	var notes []string
	if len(alsoCalledBy) > 0 {
		notes = append(notes, "also called by "+strings.Join(alsoCalledBy, ", "))
	}
	if len(s.UnchangedCallers) > 0 {
		notes = append(notes, "called by unchanged "+strings.Join(s.UnchangedCallers, ", "))
	}
	if len(s.TestedBy) > 0 {
		notes = append(notes, "tested by "+strings.Join(s.TestedBy, ", "))
	}
	return displaySafeLines(notes)
}

// firstChangedLine is the post-image line of the snippet's first change,
// the place an editor should open at.
func firstChangedLine(s *core.Snippet) int {
	newN := 0
	for _, l := range s.DiffLines {
		switch {
		case strings.HasPrefix(l, "@@"):
			if m := reHunk.FindStringSubmatch(l); m != nil {
				fmt.Sscan(m[2], &newN)
			}
		case strings.HasPrefix(l, "+"), strings.HasPrefix(l, "-"):
			if newN > 0 {
				// A new entity's sub-hunk opens with the blank line above it.
				return max(newN, s.StartLine)
			}
		case strings.HasPrefix(l, `\`): // "\ No newline at end of file"
		default:
			newN++
		}
	}
	if s.StartLine > 0 {
		return s.StartLine
	}
	return 1
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

// NotShownLine names the changed files the review has no snippet for, and
// why, so a binary, a rename or a mode change is never passed over in
// silence.
func NotShownLine(skipped []core.SkippedFile) string {
	parts := make([]string, len(skipped))
	for i, f := range skipped {
		name := f.Path
		if f.OldPath != "" {
			name = f.OldPath + " → " + f.Path
		}
		parts[i] = f.Reason + " " + name
	}
	return "Not shown: " + displaySafe(strings.Join(parts, " · "))
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
	applyMode   bool // stage on accept, unstage on un-accept (off with --no-apply)
	stage       func(s *core.Snippet, reverse bool) error
	keys        map[string]string // review-state key per snippet ID

	// RepoRoot resolves snippet paths for the o (open in editor) key.
	RepoRoot string
	raw      *rawMode

	// Skipped lists the changed files with no snippet: the header counts
	// them, and the hint line names them until the first key.
	Skipped []core.SkippedFile
	// Reviewed reads a file's reviewed version when that is not the
	// working tree (--base, --staged), for the o key; nil otherwise.
	Reviewed func(path string) ([]byte, error)
	// Reload reads the diff again, after o changed a file or when Changed
	// reports a change; nil when the review is not of the working tree.
	Reload func() (*core.ReviewForest, []core.SkippedFile, error)
	// Changed names what changed outside astra since the diff was read,
	// "" when nothing did. y, x and r check it first when they stage.
	Changed func() string
	// Notice is shown on the hint line at start.
	Notice string
	copies string // temp dir of the read-only copies o opened

	tree       *treeState
	cursor     int // index into tree.visible
	diffScroll int
	diffMax    int                 // largest diffScroll that still fills the pane
	diffCache  map[string][]string // rendered diff lines per snippet ID
	cacheCols  int                 // width the cache was rendered at

	search    *searchState
	prompting bool   // the hint line is the / prompt
	input     string // pattern being typed at the prompt
	status    string // one-shot message shown on the hint line
}

// NewRunner creates a Runner for interactive tree review.
// autoAdvance controls whether the cursor jumps to the next pending snippet
// after the user accepts, rejects, or marks a snippet as reviewed.
// applyMode enables git-staging; stage applies one snippet to the index
// (reverse unstages it) and may be nil.
func NewRunner(forest *core.ReviewForest, store *state.Store, autoAdvance, applyMode bool, stage func(s *core.Snippet, reverse bool) error) *Runner {
	return &Runner{
		forest:      forest,
		flat:        forest.Flatten(core.OrderPre),
		store:       store,
		autoAdvance: autoAdvance,
		applyMode:   applyMode,
		stage:       stage,
		tree:        newTreeState(forest),
		diffCache:   make(map[string][]string),
	}
}

// Run starts the interactive loop. Returns when the user quits (q).
func (r *Runner) Run() error {
	raw, err := enableRawMode()
	if err != nil {
		// Fall back to non-interactive view if we can't get raw mode.
		if len(r.Skipped) > 0 {
			fmt.Println(NotShownLine(r.Skipped))
		}
		View(r.flat, r.store)
		return nil
	}
	r.raw = raw
	defer r.removeCopies()
	// The alternate screen keeps frames out of the scrollback and gives the
	// shell its screen back on exit.
	fmt.Print("\033[?1049h")
	leave := func() {
		raw.restore()
		fmt.Print("\r\n\033[?25h\033[?1049l")
	}
	defer leave()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case s := <-sigs:
			leave()
			r.removeCopies()
			os.Exit(128 + int(s.(syscall.Signal)))
		case <-done:
		}
	}()
	notifyResize(resized)
	defer signal.Stop(resized)

	if len(r.Skipped) > 0 {
		r.status = NotShownLine(r.Skipped)
	}
	if r.Notice != "" {
		r.status = strings.TrimSuffix(r.Notice+" · "+r.status, " · ")
	}
	r.render()

	for {
		key := readKey()
		if key == "" {
			break
		}
		if key == keyResize {
			r.render()
			continue
		}
		r.status = ""

		switch key {
		case "q", "Q", "\x03":
			return nil
		case "J", "\033[B":
			r.moveCursor(1)
		case "K", "\033[A":
			r.moveCursor(-1)
		case "l", "\033[C", "\r": // expand / drill in / follow ref
			r.drillIn()
		case "h", "\033[D": // collapse / jump to parent
			r.drillOut()
		case "y", "Y", "a", "A": // y/a = accept (y mirrors git add -p)
			r.setStatus("accepted")
		case "x", "X":
			r.setStatus("rejected")
		case "r", "R":
			r.setStatus("reviewed")
		case "]":
			r.gotoPending(1)
		case "[":
			r.gotoPending(-1)
		case "j":
			r.scrollDiff(1)
		case "k":
			r.scrollDiff(-1)
		case "g":
			if readKey() == "g" {
				r.diffScroll = 0
			}
			r.render()
		case "G":
			r.diffScroll = r.diffMax
			r.render()
		case "\033[H", "\033[1~", "\033[7~": // Home
			r.moveCursor(-r.cursor)
		case "\033[F", "\033[4~", "\033[8~": // End
			r.moveCursor(len(r.tree.visible) - 1 - r.cursor)
		case "/":
			r.promptSearch()
		case "n":
			r.searchJump(1)
		case "N":
			r.searchJump(-1)
		case "E":
			r.tree.expandAllNodes()
			r.refocus()
		case "C":
			r.collapseAll()
		case "\t": // Tab toggles auto-advance
			r.autoAdvance = !r.autoAdvance
			r.render()
		case "o", "O":
			r.openInEditor()
		case "?":
			r.renderHelp()
		}
	}
	return nil
}

// openInEditor suspends the TUI and opens $VISUAL / $EDITOR at the focused
// snippet's first changed line.
func (r *Runner) openInEditor() {
	n := r.currentNode()
	if n == nil {
		return
	}
	path, note, err := r.editorPath(n.Snippet)
	if err != nil {
		r.status = "o: " + err.Error()
		r.render()
		return
	}
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	name, args := editorArgs(editor, path, firstChangedLine(n.Snippet))
	before, _ := os.ReadFile(path)

	// Editors that use the alternate screen leave it on exit, so step out first.
	r.raw.restore()
	fmt.Print("\033[?25h\033[?1049l")
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err = cmd.Run()
	if raw, rerr := enableRawMode(); rerr == nil {
		r.raw = raw
	}
	fmt.Print("\033[?1049h")
	r.status = note
	if err != nil {
		r.status = fmt.Sprintf("%s: %v", editor, err)
	}
	if after, _ := os.ReadFile(path); r.Reload != nil && !bytes.Equal(before, after) {
		r.reload(n.Snippet.FilePath + " changed: reloaded the diff")
		return
	}
	r.render()
}

// reload reads the diff again and shows it with why on the hint line,
// keeping the cursor on the same snippet when it is still there.
func (r *Runner) reload(why string) {
	if r.Reload == nil {
		r.status = why
		r.render()
		return
	}
	forest, skipped, err := r.Reload()
	if err != nil {
		r.status = "could not reload the diff: " + err.Error()
		r.render()
		return
	}
	var id string
	if n := r.currentNode(); n != nil {
		id = n.Snippet.ID
	}
	expanded := r.tree.expanded
	r.forest, r.flat, r.Skipped = forest, forest.Flatten(core.OrderPre), skipped
	r.tree = newTreeState(forest)
	for e := range expanded {
		if forest.ByID[e] != nil {
			r.tree.expanded[e] = true
		}
	}
	r.keys, r.search = nil, nil
	r.diffCache = make(map[string][]string)
	r.cursor = 0
	r.focusNode(id)
	r.status = why
	r.render()
}

// editorPath is the file o opens for s: the working-tree file when it is
// the reviewed version, else a read-only copy of the reviewed version, so
// the editor never shows other code at the snippet's line, and saving
// never creates a file the checked-out branch does not have.
func (r *Runner) editorPath(s *core.Snippet) (path, note string, err error) {
	if s.IsDeleted {
		return "", "", fmt.Errorf("%s is deleted in this review", s.FilePath)
	}
	// A crafted tree can name a path outside the repository.
	if !filepath.IsLocal(s.FilePath) {
		return "", "", fmt.Errorf("%s is outside the repository", s.FilePath)
	}
	path = filepath.Join(r.RepoRoot, s.FilePath)
	fi, statErr := os.Stat(path)
	regular := statErr == nil && fi.Mode().IsRegular()
	if r.Reviewed == nil {
		if !regular {
			return "", "", fmt.Errorf("%s is not a file in the working tree", s.FilePath)
		}
		return path, "", nil
	}
	want, err := r.Reviewed(s.FilePath)
	if err != nil {
		return "", "", fmt.Errorf("no reviewed version of %s to open: %v", s.FilePath, err)
	}
	if regular {
		if have, err := os.ReadFile(path); err == nil && bytes.Equal(have, want) {
			return path, "", nil
		}
	}
	if r.copies == "" {
		if r.copies, err = os.MkdirTemp("", "astra-"); err != nil {
			return "", "", err
		}
	}
	// The copy keeps the file's name, so the editor knows its language.
	path = filepath.Join(r.copies, s.FilePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", "", err
	}
	os.Remove(path) // a copy o opened before is read-only
	if err := os.WriteFile(path, want, 0o444); err != nil {
		return "", "", err
	}
	return path, "opened the reviewed " + s.FilePath + " as a read-only copy", nil
}

// removeCopies deletes the copies o opened.
func (r *Runner) removeCopies() {
	if r.copies != "" {
		os.RemoveAll(r.copies)
		r.copies = ""
	}
}

// editorArgs builds the command that opens path at line, using the syntax
// each common editor expects.
func editorArgs(editor, path string, line int) (string, []string) {
	parts := strings.Fields(editor)
	if len(parts) == 0 {
		parts = []string{"vi"}
	}
	name, args := parts[0], parts[1:]
	at := fmt.Sprintf("%s:%d", path, line)
	switch filepath.Base(name) {
	case "code", "code-insiders", "codium", "cursor", "windsurf":
		return name, append(args, "-g", at)
	case "hx", "helix", "zed", "subl", "idea", "goland", "pycharm":
		return name, append(args, at)
	default: // vi, vim, nvim, nano, emacs, micro, kak, …
		return name, append(args, fmt.Sprintf("+%d", line), path)
	}
}

// keyResize is what readKey returns when the terminal was resized.
const keyResize = "\x00resize"

// resized gets SIGWINCH while the TUI runs.
var resized = make(chan os.Signal, 1)

// readKey reads one keypress, or returns keyResize if the terminal is
// resized first. Reading byte-by-byte (instead of a 3-byte buffer) keeps
// rapid keystrokes from batching into one unrecognized chunk; an ESC byte
// pulls the rest of its escape sequence, if one follows within 50ms, so a
// bare Esc is reported as "\033".
func readKey() string {
	for !stdinReadable(100) {
		select {
		case <-resized:
			return keyResize
		default:
		}
	}
	var b [1]byte
	n, err := os.Stdin.Read(b[:])
	if err != nil || n == 0 {
		return ""
	}
	if b[0] != 0x1b {
		return string(b[:1])
	}
	return readEscape()
}

// readEscape reads the rest of an escape sequence after its ESC. CSI (ESC [)
// and SS3 (ESC O) sequences are read up to their final byte, so no tail byte
// is left over to run as a command.
func readEscape() string {
	next := func() (byte, bool) {
		var b [1]byte
		if !stdinReadable(50) {
			return 0, false
		}
		n, err := os.Stdin.Read(b[:])
		return b[0], n == 1 && err == nil
	}
	c, ok := next()
	for ok && c == 0x1b { // Alt as an ESC prefix: ESC ESC [ A is Alt+↑
		c, ok = next()
	}
	if !ok {
		return "\033"
	}
	seq := []byte{0x1b, c}
	if c == '[' || c == 'O' {
		for len(seq) < 32 {
			if c, ok = next(); !ok {
				break
			}
			seq = append(seq, c)
			// The Linux console sends F1-F5 as ESC [ [ A to E.
			if c >= 0x40 && c <= 0x7e && !(len(seq) == 3 && c == '[') {
				break
			}
		}
	}
	return normalizeKey(string(seq))
}

// normalizeKey maps the forms terminals send for an arrow, Home or End key,
// such as ESC O A in application mode or ESC [ 1 ; 5 A with Ctrl held, to
// the plain ESC [ A form.
func normalizeKey(seq string) string {
	if len(seq) < 3 || seq[1] != '[' && seq[1] != 'O' {
		return seq
	}
	final := seq[len(seq)-1]
	if strings.IndexByte("ABCDHF", final) < 0 || strings.Trim(seq[2:len(seq)-1], "0123456789;") != "" {
		return seq
	}
	return "\033[" + string(final)
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

// scrollDiff scrolls the diff pane by a page step; at the bottom (or top)
// it moves on to the next (or previous) snippet instead.
func (r *Runner) scrollDiff(dir int) {
	scroll, move := scrollStep(r.diffScroll, r.diffMax, dir)
	if move != 0 {
		r.moveCursor(move)
		return
	}
	r.diffScroll = scroll
	r.render()
}

func scrollStep(scroll, max, dir int) (newScroll, move int) {
	if (dir > 0 && scroll >= max) || (dir < 0 && scroll <= 0) {
		return scroll, dir
	}
	scroll += dir * 5
	if scroll > max {
		scroll = max
	}
	if scroll < 0 {
		scroll = 0
	}
	return scroll, 0
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

// key is the review-state key of s: its ID and change.
func (r *Runner) key(s *core.Snippet) string {
	if r.keys == nil {
		r.keys = make(map[string]string)
	}
	k, ok := r.keys[s.ID]
	if !ok {
		k = state.Key(s.ID, s.DiffLines)
		r.keys[s.ID] = k
	}
	return k
}

// statusOf is the review status of s.
func (r *Runner) statusOf(s *core.Snippet) string {
	return r.store.Get(r.key(s)).Status
}

func (r *Runner) setStatus(status string) {
	n := r.currentNode()
	if n == nil {
		return
	}
	// The index and the files must still be what the diff on screen was
	// read from, or staging would write changes nobody reviewed.
	if r.applyMode && r.Changed != nil {
		if why := r.Changed(); why != "" {
			r.reload(why + " outside astra: reloaded the diff, verdict not recorded")
			return
		}
	}
	key := r.key(n.Snippet)
	st := r.store.Get(key)
	if r.applyMode && r.stage != nil && (status == "accepted" || st.Status == "accepted") {
		if err := r.stage(n.Snippet, status != "accepted"); err != nil {
			verb := "stage"
			if status != "accepted" {
				verb = "unstage"
			}
			r.status = "could not " + verb + ": " + err.Error()
			r.render()
			return
		}
	}
	if err := r.store.Set(key, status, st.Comment); err != nil {
		// Lead with the cause; the full error is mostly the file's path.
		for u := errors.Unwrap(err); u != nil; u = errors.Unwrap(u) {
			err = u
		}
		r.status = "review state not saved: " + err.Error()
		r.render()
		return
	}
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
		if s.ID != n.Snippet.ID && r.statusOf(s) == "pending" {
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
	var rows []string
	for _, l := range lines {
		rows = append(rows, ansiWrap(l, width)...)
	}
	lines = rows
	r.diffCache[s.ID] = lines
	return lines
}

// termSize is terminalSize; tests swap it for a fixed size.
var termSize = terminalSize

func (r *Runner) render() {
	rows, cols := termSize()
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
	// The tree takes a third of the rows, at least 8, but leaves the diff 5.
	treeH := min(max(8, rows/3), len(r.tree.visible), max(1, rows-10))
	// Budget: header(1) + rule(1) + tree + focus rule(1) + diff +
	// more-indicator(1) + hint(1, no trailing newline).
	diffH := max(0, rows-treeH-5)

	// Hide cursor during redraw, clear screen, move to top.
	fmt.Print("\033[?25l\033[2J\033[H")

	// Header: progress + toggles.
	done := 0
	for _, s := range r.flat {
		if r.statusOf(s) != "pending" {
			done++
		}
	}
	total := len(r.flat)
	pct := 0
	if total > 0 {
		pct = done * 100 / total
	}
	autoLabel := "auto:on"
	if !r.autoAdvance {
		autoLabel = "auto:off"
	}
	applyLabel := ""
	if r.applyMode {
		applyLabel = "  " + yellow + "[staging]" + reset
	}
	head := fmt.Sprintf("%s Astra%s  %d/%d reviewed ", bold+blue+"⭐", reset, done, total)
	tail := fmt.Sprintf(" %d%%  %s%s%s%s", pct, dim, autoLabel, reset, applyLabel)
	// In a narrow terminal the bar shrinks before the labels are cut.
	barW := min(20, max(0, cols-1-strWidth(stripANSI(head+tail))))
	filled := pct * barW / 100
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barW-filled)
	notShown := ""
	if len(r.Skipped) > 0 {
		notShown = fmt.Sprintf("  %s%d not shown%s", yellow, len(r.Skipped), reset)
	}
	fmt.Println(ansiTruncate(head+dim+bar+reset+tail+notShown, cols-1))
	fmt.Println(dim + strings.Repeat("─", cols) + reset)

	// Tree pane.
	r.tree.scrollTo(r.cursor, treeH)
	for i := r.tree.top; i < r.tree.top+treeH && i < len(r.tree.visible); i++ {
		row := r.tree.visible[i]
		fmt.Println(r.tree.renderTreeRow(row, r.statusOf(row.node.Snippet), i == r.cursor, cols))
	}

	// Focus rule + diff pane.
	n := r.currentNode()
	if n != nil {
		s := n.Snippet
		title := ""
		if s.SymbolPath != "" {
			title = displaySafe(s.SymbolPath) + "  "
		}
		loc := fmt.Sprintf("%s:%d · %s ", displaySafe(s.FilePath), firstChangedLine(s), r.statusOf(s))
		rule := ""
		if pad := cols - strWidth("── "+title+loc); pad > 0 {
			rule = strings.Repeat("─", pad)
		}
		fmt.Println(ansiTruncate(dim+"── "+reset+bold+title+reset+dim+loc+rule+reset, cols))

		var also []string
		for _, id := range n.AlsoCalledBy {
			if c := r.forest.ByID[id]; c != nil && c.Snippet.SymbolPath != "" {
				also = append(also, c.Snippet.SymbolPath)
			}
		}
		if notes := snippetNotes(s, also); len(notes) > 0 && diffH > 0 {
			fmt.Println(dim + ansiTruncate(strings.Join(notes, " · "), cols-1) + reset)
			diffH--
		}

		lines := r.renderedDiff(s, cols)
		r.diffMax = len(lines) - diffH
		if r.diffMax < 0 {
			r.diffMax = 0
		}
		if r.diffScroll > r.diffMax {
			r.diffScroll = r.diffMax
		}
		end := r.diffScroll + diffH
		if end > len(lines) {
			end = len(lines)
		}
		// The current search hit is shown in inverse video so n/N have
		// visible effect even when every hit fits on one screen.
		hitRow := -1
		if h := r.search; h != nil && h.hit.snip >= 0 && r.flat[h.hit.snip].ID == s.ID {
			hitRow = h.hit.row
		}
		for i, l := range lines[r.diffScroll:end] {
			if r.diffScroll+i == hitRow {
				l = "\033[7m" + stripANSI(l) + reset
			}
			fmt.Println(ansiTruncate(l, cols))
		}
		if end < len(lines) {
			fmt.Println(dim + ansiTruncate(fmt.Sprintf("… %d more lines (j/k to scroll)", len(lines)-end), cols-1) + reset)
		}
	}

	// Hint line (truncated so it can never wrap and push the header off).
	switch {
	case r.prompting:
		fmt.Print(ansiTruncate("/"+displaySafe(r.input), cols-1) + "\033[?25h")
	case r.status != "":
		status := strings.Join(strings.Fields(r.status), " ") // git errors span lines
		fmt.Print(yellow + ansiTruncate(displaySafe(status), cols-1) + reset)
	default:
		hints := "? help · q quit · J/K move · l/h drill in/out · y/x/r judge · ]/[ pending · j/k scroll · / search · o open · E/C fold · tab auto"
		fmt.Print(dim + ansiTruncate(hints, cols-1) + reset)
	}
}

func (r *Runner) renderHelp() {
	lines := []string{
		bold + "⭐ Astra keyboard shortcuts" + reset,
		"",
		"  J / ↓       Move down the tree",
		"  K / ↑       Move up the tree",
		"  l / → / ⏎   Expand node; if expanded, move to first callee;",
		"              on a → reference, jump to where it's shown",
		"  h / ←       Collapse node; if collapsed, jump to caller",
		"  y / a       Accept snippet",
		"  x           Reject snippet",
		"  r           Mark as reviewed (no judgement)",
		"  ] / [       Next / previous pending snippet",
		"  j / k       Scroll the diff pane; at its end, next / previous snippet",
		"  gg / G      Scroll the diff pane to its top / bottom",
		"  Home / End  First / last row of the tree",
		"  /           Search path, symbol, and diff text (regexp; case-insensitive",
		"              unless the pattern has an uppercase letter)",
		"  n / N       Next / previous search hit",
		"  o           Open the snippet in $VISUAL / $EDITOR at its first change",
		"  E / C       Expand / collapse the whole tree",
		"  tab         Toggle auto-advance (jump to next pending after judging)",
		"  q / Ctrl-C  Quit",
		"  ?           Show this help",
		"",
		dim + "The tree is the call graph of changed code: children are the" + reset,
		dim + "functions/types the focused snippet calls. Roots are entry points" + reset,
		dim + "nothing else in the diff calls. Tests follow the code they test." + reset,
		"",
		dim + "Note: verdicts are saved under .git/astra/, per change. A named snippet" + reset,
		dim + "      keeps its verdict across commits and rebases; an unnamed one resets" + reset,
		dim + "      when lines above it move. In working tree mode y also stages the" + reset,
		dim + "      snippet and x/r unstages it; pass --no-apply to leave the index alone." + reset,
	}
	if len(r.Skipped) > 0 {
		lines = append(lines, "", dim+NotShownLine(r.Skipped)+reset)
	}
	for {
		rows, cols := termSize()
		if rows <= 0 {
			rows = 40
		}
		if cols <= 0 {
			cols = 80
		}
		fmt.Print("\033[2J\033[H")
		// On a short screen the notes at the end give way so the keys stay.
		for _, l := range lines[:min(len(lines), max(1, rows-2))] {
			fmt.Println(ansiTruncate(l, cols-1))
		}
		fmt.Print("\n" + ansiTruncate("Press any key to continue…", cols-1))
		if readKey() != keyResize {
			break
		}
	}
	r.render()
}
