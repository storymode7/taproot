package tui

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/storymode7/astra/core"
)

// reSGR matches the colour escapes the highlighters emit themselves.
var reSGR = regexp.MustCompile("\x1b\\[[0-9;]*m")

// hostile holds diff lines that hide or fake code when printed raw: a CR
// that returns to column 0 so a decoy is drawn over the real line, and
// escapes that erase the row and move the cursor up.
var hostile = []string{
	"@@ -1,2 +1,3 @@",
	" def handler(req):",
	"+    os.system(req.args[\"cmd\"])  \r       2 │ +    return ok(req)",
	"+    log(req)  # \x1b[2K\x1b[1A\x1b[2K",
	"-    return ok(req)",
}

// assertShown fails when out holds a control character other than colour
// escapes and line breaks, or lacks the visible stand-ins for them.
func assertShown(t *testing.T, where, out string) {
	t.Helper()
	for _, c := range reSGR.ReplaceAllString(out, "") {
		if c < 0x20 && c != '\n' && c != '\t' || c >= 0x7f && c <= 0x9f {
			t.Errorf("%s: raw control character %U reaches the terminal: %q", where, c, out)
			return
		}
	}
	if !strings.Contains(out, "␍") || !strings.Contains(out, "␛[2K") {
		t.Errorf("%s: control characters are not shown as ␍ and ␛: %q", where, out)
	}
}

func hostileSnippet() *core.Snippet {
	return &core.Snippet{
		ID: "a", FilePath: "app\x1b[2K.py", SymbolPath: "handler\r", NodeType: "function",
		DiffLines: hostile, UnchangedCallers: []string{"cli.py::main\x1b[1A"},
	}
}

func TestHighlightLines_ShowsControlCharacters(t *testing.T) {
	useHighlighter(t, HighlightBuiltin)
	assertShown(t, "builtin", strings.Join(highlightLines("app.py", hostile), "\n"))
	if _, err := exec.LookPath("bat"); err == nil {
		useHighlighter(t, HighlightBat)
		assertShown(t, "bat", strings.Join(highlightLines("app.py", hostile), "\n"))
	}
}

func TestDisplaySafe(t *testing.T) {
	for in, want := range map[string]string{
		"a\rb\x1b[2K\x07\x7f":       "a␍b␛[2K␇␡",
		"\u009b1A \u202eevil\u2069": "�1A �evil�",
		"bad \xff utf8":             "bad � utf8",
		"\tkept\r":                  "\tkept",
		"crlf\r\r":                  "crlf␍",
		"plain ✓ 日本":                "plain ✓ 日本",
	} {
		if got := displaySafe(in); got != want {
			t.Errorf("displaySafe(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDeltaInput_ShowsControlCharacters(t *testing.T) {
	assertShown(t, "delta input", deltaInput("app\r.py", hostile))
	if _, err := exec.LookPath("delta"); err != nil {
		return
	}
	NoDeltaConfig = true
	defer func() { NoDeltaConfig = false }()
	out, err := runDeltaCapture("app.py", hostile, 100)
	if err != nil {
		t.Fatal(err)
	}
	// delta fills line backgrounds with an erase to the end of the line.
	assertShown(t, "delta", strings.ReplaceAll(strings.Join(out, "\n"), "\x1b[0K", ""))
}

func TestView_ShowsControlCharacters(t *testing.T) {
	useHighlighter(t, HighlightBuiltin)
	r := testRunner(t, hostileSnippet())
	for _, plain := range []bool{true, false} {
		Plain = plain
		out := captureStdout(t, func() { View(r.flat, r.store) })
		Plain = false
		assertShown(t, "View", out)
	}
}

func TestRender_ShowsControlCharacters(t *testing.T) {
	useHighlighter(t, HighlightBuiltin)
	r := testRunner(t, hostileSnippet())
	r.status = "git apply: \x1b[2K\x1b]0;title\x07"
	assertShown(t, "frame", strings.Join(frame(t, r, 24, 100), "\n"))

	r.status, r.prompting, r.input = "", true, "x\r\x1b[2K"
	lines := frame(t, r, 24, 100)
	prompt := strings.TrimSuffix(lines[len(lines)-1], "\033[?25h")
	assertShown(t, "search prompt", prompt)
}
