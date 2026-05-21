package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Highlighter is the active syntax-highlighting backend, detected at startup.
type Highlighter int

const (
	HighlightBuiltin Highlighter = iota // simple +/-/@ coloring (always available)
	HighlightDelta                      // https://github.com/dandavison/delta
	HighlightBat                        // https://github.com/sharkdp/bat
)

var activeHighlighter Highlighter
var termWidth = 120

func init() {
	activeHighlighter = detectHighlighter()
	if w := terminalWidth(); w > 0 {
		termWidth = w
	}
}

func detectHighlighter() Highlighter {
	if _, err := exec.LookPath("delta"); err == nil {
		return HighlightDelta
	}
	if _, err := exec.LookPath("bat"); err == nil {
		return HighlightBat
	}
	return HighlightBuiltin
}

func terminalWidth() int {
	// Quick read from stty; ignore errors (best-effort).
	cmd := exec.Command("stty", "size")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	var rows, cols int
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d %d", &rows, &cols)
	return cols
}

// highlightLines applies syntax highlighting to a snippet's diff lines.
//
// filePath is used to help delta/bat detect the language.
// Returns replacement lines (may include ANSI codes); never errors — falls
// back to the next tier if the external tool fails.
func highlightLines(filePath string, diffLines []string) []string {
	switch activeHighlighter {
	case HighlightDelta:
		if out := runDelta(filePath, diffLines); out != nil {
			return out
		}
		fallthrough
	case HighlightBat:
		if out := runBat(filePath, diffLines); out != nil {
			return out
		}
		fallthrough
	default:
		return builtinHighlight(diffLines)
	}
}

// ---------------------------------------------------------------------------
// delta backend
// ---------------------------------------------------------------------------

func runDelta(filePath string, diffLines []string) []string {
	// Construct a minimal git unified diff header so delta detects the language.
	header := fmt.Sprintf("diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n",
		filePath, filePath, filePath, filePath)
	input := header + strings.Join(diffLines, "\n") + "\n"

	cmd := exec.Command("delta",
		"--paging=never",
		"--file-style=omit",
		"--hunk-header-style=omit",
		fmt.Sprintf("--width=%d", termWidth),
	)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	return stripDeltaArtifacts(lines)
}

// stripDeltaArtifacts removes the blank separator line delta adds at the end
// and any accidental file-header lines that slipped through.
func stripDeltaArtifacts(lines []string) []string {
	result := make([]string, 0, len(lines))
	for _, l := range lines {
		// Skip delta's "──── filename ────" file header lines (in case the
		// user's delta config doesn't honour --file-style=omit).
		if strings.HasPrefix(l, "\033") && strings.Contains(l, "─") {
			continue
		}
		result = append(result, l)
	}
	return result
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
