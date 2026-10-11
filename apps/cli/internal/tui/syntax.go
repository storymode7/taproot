package tui

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Highlighter is the active syntax-highlighting backend, detected at startup.
type Highlighter int

const (
	HighlightBuiltin Highlighter = iota // simple +/-/@ coloring (always available)
	HighlightBat                        // https://github.com/sharkdp/bat
	HighlightDelta                      // https://github.com/dandavison/delta
)

// NoDelta forces bat/builtin even when delta is installed.
var NoDelta bool

// NoDeltaConfig passes --no-gitconfig to delta (--no-delta-config flag).
var NoDeltaConfig bool

var activeHighlighter Highlighter

// DetectHighlighter picks delta, bat or the built-in colouring, whichever is
// installed first. Call it after NoDelta is set.
func DetectHighlighter() {
	activeHighlighter = detectHighlighter()
}

func detectHighlighter() Highlighter {
	if !NoDelta {
		if _, err := exec.LookPath("delta"); err == nil {
			return HighlightDelta
		}
	}
	if _, err := exec.LookPath("bat"); err == nil {
		return HighlightBat
	}
	return HighlightBuiltin
}

func terminalSize() (rows, cols int) {
	// Quick read from stty; used by callers that need explicit dimensions.
	cmd := exec.Command("stty", "size")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return 0, 0
	}
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d %d", &rows, &cols)
	return rows, cols
}

func terminalWidth() int {
	_, cols := terminalSize()
	return cols
}

// highlightLines applies syntax highlighting to a snippet's diff lines.
//
// filePath is used to help bat detect the language.
// Returns replacement lines (may include ANSI codes); never errors — falls
// back to the next tier if the external tool fails.
//
// Note: delta is NOT handled here — it writes directly to stdout via
// printDiffLines to preserve TTY semantics.
func highlightLines(filePath string, diffLines []string) []string {
	diffLines = displaySafeLines(diffLines)
	var out []string
	switch activeHighlighter {
	case HighlightBat, HighlightDelta:
		// bat is used for line-by-line capture; delta uses direct-write (see printDiffLines)
		out = runBat(filePath, diffLines)
	}
	if out == nil {
		out = builtinHighlight(diffLines)
	}
	return withLineNumbers(diffLines, out)
}

// withLineNumbers prefixes each rendered line with GitHub-style old/new
// line-number columns, counted from the hunk headers. Hunk headers
// themselves get a blank gutter.
func withLineNumbers(diffLines, rendered []string) []string {
	numStyle := hexFg(palette().zeroNum)
	oldN, newN := 0, 0
	out := make([]string, len(rendered))
	for i, r := range rendered {
		l := ""
		if i < len(diffLines) {
			l = diffLines[i]
		}
		var o, n string
		switch {
		case strings.HasPrefix(l, "@@"):
			if m := reHunk.FindStringSubmatch(l); m != nil {
				fmt.Sscan(m[1], &oldN)
				fmt.Sscan(m[2], &newN)
			}
		case strings.HasPrefix(l, "+"):
			n = fmt.Sprint(newN)
			newN++
		case strings.HasPrefix(l, "-"):
			o = fmt.Sprint(oldN)
			oldN++
		case strings.HasPrefix(l, `\`): // "\ No newline at end of file"
		default:
			o, n = fmt.Sprint(oldN), fmt.Sprint(newN)
			oldN++
			newN++
		}
		out[i] = fmt.Sprintf("%s%4s %4s │%s %s", numStyle, o, n, reset, r)
	}
	return out
}

var reHunk = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)`)

// displaySafe turns control characters in reviewed text into glyphs (␍, ␛,
// ␡, and � for C1 and bidi controls), so a diff line cannot move the cursor
// or restyle the terminal. A trailing CR is a CRLF line end and is dropped.
func displaySafe(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return r
		case r < 0x20:
			return 0x2400 + r
		case r == 0x7f:
			return '␡'
		case r >= 0x80 && r <= 0x9f, r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
			return utf8.RuneError
		}
		return r
	}, strings.TrimSuffix(s, "\r"))
}

func displaySafeLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = displaySafe(l)
	}
	return out
}

// hexFg turns "#rrggbb" into a truecolor foreground escape.
func hexFg(hex string) string {
	var r, g, b int
	fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b)
	return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
}

// ---------------------------------------------------------------------------
// delta backend
// ---------------------------------------------------------------------------

func deltaArgs() []string {
	args := []string{
		"--paging=never",
		"--file-style=omit",
		"--hunk-header-style=omit",
	}
	if NoDeltaConfig {
		args = append([]string{"--no-gitconfig"}, args...)
	}
	if NoDeltaConfig || !userHasDeltaStyle() {
		args = append(args, githubDeltaStyle()...)
	}
	return args
}

// userHasDeltaStyle reports whether ~/.gitconfig sets delta's look. When it
// does, those settings win; otherwise Astra applies its GitHub-style theme.
func userHasDeltaStyle() bool {
	out, err := exec.Command("git", "config", "--get-regexp", `^delta\.`).Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		key, _, _ := strings.Cut(line, " ")
		switch strings.TrimPrefix(key, "delta.") {
		case "features", "syntax-theme", "plus-style", "minus-style",
			"line-numbers", "side-by-side", "dark", "light":
			return true
		}
	}
	return false
}

// lightTheme picks GitHub's light palette when ASTRA_THEME=light, or when
// ASTRA_THEME is unset and COLORFGBG reports a light background.
func lightTheme() bool {
	switch os.Getenv("ASTRA_THEME") {
	case "light":
		return true
	case "dark":
		return false
	}
	fgbg := os.Getenv("COLORFGBG")
	if i := strings.LastIndexByte(fgbg, ';'); i >= 0 {
		bg := fgbg[i+1:]
		return bg == "7" || bg == "15"
	}
	return false
}

// githubPalette holds GitHub's PR-diff colours, pre-blended onto the page
// background so terminals without alpha get the same result.
type githubPalette struct {
	syntax, addLine, addWord, delLine, delWord, addNum, delNum, zeroNum string
}

var (
	githubDark = githubPalette{
		syntax: "Visual Studio Dark+", addLine: "#12261e", addWord: "#1a4a29",
		delLine: "#301a1f", delWord: "#6b2b2b",
		addNum: "#3fb950", delNum: "#f85149", zeroNum: "#6e7681",
	}
	githubLight = githubPalette{
		syntax: "GitHub", addLine: "#dafbe1", addWord: "#aceebb",
		delLine: "#ffebe9", delWord: "#ffcecb",
		addNum: "#1a7f37", delNum: "#cf222e", zeroNum: "#8c959f",
	}
)

func palette() githubPalette {
	if lightTheme() {
		return githubLight
	}
	return githubDark
}

// githubDeltaStyle renders like GitHub's PR diff: old/new line numbers,
// kept +/- markers, soft line tints and stronger word highlights.
func githubDeltaStyle() []string {
	p := palette()
	mode := "--dark"
	if lightTheme() {
		mode = "--light"
	}
	if theme := os.Getenv("ASTRA_DELTA_THEME"); theme != "" {
		p.syntax = theme
	}
	return []string{
		mode,
		"--syntax-theme=" + p.syntax,
		"--plus-style=syntax " + p.addLine,
		"--plus-emph-style=syntax " + p.addWord,
		"--minus-style=syntax " + p.delLine,
		"--minus-emph-style=syntax " + p.delWord,
		"--zero-style=syntax",
		"--keep-plus-minus-markers",
		"--line-numbers",
		"--line-numbers-left-format={nm:>4} ",
		"--line-numbers-right-format={np:>4} │ ",
		"--line-numbers-minus-style=" + p.delNum,
		"--line-numbers-plus-style=" + p.addNum,
		"--line-numbers-zero-style=" + p.zeroNum,
		"--line-numbers-left-style=" + p.zeroNum,
		"--line-numbers-right-style=" + p.zeroNum,
	}
}

func deltaInput(filePath string, diffLines []string) string {
	filePath = displaySafe(filePath)
	header := fmt.Sprintf("diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n",
		filePath, filePath, filePath, filePath)
	return header + strings.Join(displaySafeLines(diffLines), "\n") + "\n"
}

// runDeltaDirect pipes diff lines through delta writing directly to w (usually
// os.Stdout).  Because delta has a real TTY on its stdout it auto-detects
// terminal width, honours the user's ~/.gitconfig theme, and emits correctly
// scoped \033[K / background-fill sequences — the same output as screenshot 2.
func runDeltaDirect(w io.Writer, filePath string, diffLines []string) error {
	cmd := exec.Command("delta", deltaArgs()...)
	cmd.Stdin = strings.NewReader(deltaInput(filePath, diffLines))
	cmd.Stdout = w // direct write — delta sees the real TTY
	cmd.Stderr = io.Discard
	return cmd.Run()
}

// runDeltaCapture runs delta with an explicit --width and captures the output
// line by line so the caller can window/scroll it (the tree view needs this).
// The explicit width replaces the TTY auto-detection that direct-write relies
// on; colors and background fills survive capture (delta always emits ANSI).
func runDeltaCapture(filePath string, diffLines []string, width int) ([]string, error) {
	args := append(deltaArgs(), fmt.Sprintf("--width=%d", width))
	cmd := exec.Command("delta", args...)
	cmd.Stdin = strings.NewReader(deltaInput(filePath, diffLines))
	cmd.Stderr = io.Discard
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimRight(string(out), "\n"), "\n"), nil
}

// ---------------------------------------------------------------------------
// bat backend
// ---------------------------------------------------------------------------

func runBat(filePath string, diffLines []string) []string {
	// bat can't natively highlight unified diffs in a line-by-line way, but
	// it can colorize the code content.  We extract the code body, pipe it
	// through bat, then re-attach the +/-/@ prefix colors.
	var codeLines []string
	var prefixes []string
	for _, l := range diffLines {
		if len(l) == 0 {
			prefixes = append(prefixes, " ")
			codeLines = append(codeLines, "")
			continue
		}
		prefixes = append(prefixes, string(l[0]))
		codeLines = append(codeLines, l[1:])
	}

	cmd := exec.Command("bat",
		"--paging=never",
		"--style=plain",
		"--color=always",
		"--file-name="+filePath,
		"-",
	)
	cmd.Stdin = strings.NewReader(strings.Join(codeLines, "\n"))
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	highlighted := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	result := make([]string, 0, len(prefixes))
	for i, pfx := range prefixes {
		code := ""
		if i < len(highlighted) {
			code = highlighted[i]
		}
		switch pfx {
		case "+":
			result = append(result, green+"+"+reset+code)
		case "-":
			result = append(result, red+"-"+reset+code)
		case "@":
			result = append(result, cyan+diffLines[i]+reset)
		default:
			result = append(result, " "+code)
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// Built-in fallback — same simple +/-/@ coloring as before
// ---------------------------------------------------------------------------

func builtinHighlight(diffLines []string) []string {
	result := make([]string, len(diffLines))
	for i, line := range diffLines {
		switch {
		case strings.HasPrefix(line, "+"):
			result[i] = green + line + reset
		case strings.HasPrefix(line, "-"):
			result[i] = red + line + reset
		case strings.HasPrefix(line, "@"):
			result[i] = cyan + line + reset
		default:
			result[i] = line
		}
	}
	return result
}
