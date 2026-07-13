package tui

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Highlighter is the active syntax-highlighting backend, detected at startup.
type Highlighter int

const (
	HighlightBuiltin Highlighter = iota // simple +/-/@ coloring (always available)
	HighlightBat                        // https://github.com/sharkdp/bat
	HighlightDelta                      // https://github.com/dandavison/delta
)

// UseDelta is kept for backwards compat but no longer changes behaviour —
// delta is now the default when installed.  Set NoDelta=true (--no-delta flag)
// to skip delta and use bat/builtin instead.
var UseDelta bool

// NoDelta forces bat/builtin even when delta is installed.
var NoDelta bool

// NoDeltaConfig passes --no-gitconfig to delta (--no-delta-config flag).
var NoDeltaConfig bool

var activeHighlighter Highlighter

func init() {
	if os.Getenv("ASTRA_HIGHLIGHTER") == "delta" {
		UseDelta = true
	}
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
	switch activeHighlighter {
	case HighlightBat, HighlightDelta:
		// bat is used for line-by-line capture; delta uses direct-write (see printDiffLines)
		if out := runBat(filePath, diffLines); out != nil {
			return out
		}
		return builtinHighlight(diffLines)
	default:
		return builtinHighlight(diffLines)
	}
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
		theme := os.Getenv("ASTRA_DELTA_THEME")
		if theme == "" {
			theme = "GitHub"
		}
		args = append([]string{"--no-gitconfig", "--light", "--syntax-theme=" + theme}, args...)
	}
	return args
}

func deltaInput(filePath string, diffLines []string) string {
	header := fmt.Sprintf("diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n",
		filePath, filePath, filePath, filePath)
	return header + strings.Join(diffLines, "\n") + "\n"
}

// runDeltaDirect pipes diff lines through delta writing directly to w (usually
// os.Stdout).  Because delta has a real TTY on its stdout it auto-detects
// terminal width, honours the user's ~/.gitconfig theme, and emits correctly
// scoped \033[K / background-fill sequences — the same output as screenshot 2.
func runDeltaDirect(w io.Writer, filePath string, diffLines []string) error {
	cmd := exec.Command("delta", deltaArgs()...)
	cmd.Stdin = strings.NewReader(deltaInput(filePath, diffLines))
	cmd.Stdout = w  // direct write — delta sees the real TTY
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
		"--language="+batLang(filePath),
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

func batLang(filePath string) string {
	ext := filePath
	if i := strings.LastIndexByte(filePath, '.'); i >= 0 {
		ext = filePath[i+1:]
	}
	switch strings.ToLower(ext) {
	case "js", "mjs", "cjs", "jsx":
		return "JavaScript"
	case "ts", "tsx", "mts":
		return "TypeScript"
	case "py":
		return "Python"
	case "go":
		return "Go"
	case "java":
		return "Java"
	case "rs":
		return "Rust"
	default:
		return "text"
	}
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
