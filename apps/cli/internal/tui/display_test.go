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

// cells counts terminal columns in the test strings below, independently of
// the code under test: CJK punctuation, kana, ideographs, ⭐ and emoji take
// two.
func cells(s string) int {
	n := 0
	for _, r := range stripANSI(s) {
		switch {
		case r >= 0x3000 && r <= 0x30ff, r >= 0x4e00 && r <= 0x9fff, r == 0x2b50, r >= 0x1f300 && r <= 0x1faff:
			n += 2
		default:
			n++
		}
	}
	return n
}

func TestRuneWidth(t *testing.T) {
	for r, want := range map[rune]int{
		'a': 1, 'é': 1, '\u00ad': 1, '─': 1, '│': 1, '…': 1, '●': 1, '▼': 1, '✓': 1, '✗': 1, '␍': 1, '�': 1,
		'日': 2, '。': 2, 'カ': 2, '한': 2, 'Ａ': 2, '⭐': 2, '✅': 2, '⚡': 2, '😀': 2, '🇺': 2,
		'\u0301': 0, '\u200b': 0, '\u200d': 0, '\ufe0f': 1,
	} {
		if got := runeWidth(r); got != want {
			t.Errorf("runeWidth(%q %U) = %d, want %d", r, r, got, want)
		}
	}
}

const wideLine = "  18   18 │ " + green + "+        // 日本語のコメント — wide chars 😀 emoji 🎉 and a very long line follows" + reset

func TestAnsiWrap_CountsWideCharacters(t *testing.T) {
	for _, width := range []int{80, 41, 24} {
		rows := ansiWrap(wideLine, width)
		var joined strings.Builder
		for i, row := range rows {
			if w := cells(row); w > width {
				t.Errorf("width %d: row %d is %d columns: %q", width, i, w, stripANSI(row))
			}
			plain := stripANSI(row)
			if i > 0 {
				plain = strings.TrimPrefix(plain, "          │ ")
			}
			joined.WriteString(plain)
		}
		if want := stripANSI(wideLine); joined.String() != want {
			t.Errorf("width %d: wrapped text = %q, want %q", width, joined.String(), want)
		}
	}
	if got := ansiWrap("abc日", 4); len(got) != 2 || stripANSI(got[0]) != "abc" || got[1] != "日" {
		t.Errorf("a wide character that does not fit must move to the next row: %q", got)
	}
}

func TestAnsiTruncate_CountsWideCharacters(t *testing.T) {
	if w := cells(ansiTruncate(strings.Repeat("😀", 50), 9)); w > 9 {
		t.Errorf("50 emoji cut to 9 columns take %d", w)
	}
	if got := stripANSI(ansiTruncate("ab日本", 3)); got != "ab" {
		t.Errorf("ansiTruncate(ab日本, 3) = %q, want ab", got)
	}
	if got := ansiTruncate("é", 1); got != "é" {
		t.Errorf("a combining mark takes no column: %q", got)
	}
}

func TestRenderSegments_CountsWideCharacters(t *testing.T) {
	out := renderSegments([]segment{{"処理する関数の名前がとても長い", bold}, {"  main.go", dim}}, 20, false)
	if w := cells(out); w > 19 {
		t.Errorf("tree row is %d columns, want at most 19: %q", w, stripANSI(out))
	}
}

func TestRender_WideCharactersFit(t *testing.T) {
	diff := []string{"@@ -1,0 +1,30 @@"}
	for i := 0; i < 30; i++ {
		diff = append(diff, "+日本語の段落です。これはとても長い行で、端末の幅を超えて折り返されます 😀🎉")
	}
	r := testRunner(t, &core.Snippet{
		ID: "a", FilePath: "docs/快速开始.md", SymbolPath: "设置", NodeType: "function", DiffLines: diff,
	})
	for _, size := range [][2]int{{24, 80}, {16, 60}} {
		lines := frame(t, r, size[0], size[1])
		if len(lines) > size[0] {
			t.Errorf("%dx%d: frame has %d rows", size[0], size[1], len(lines))
		}
		for i, l := range lines {
			if w := cells(l); w > size[1] {
				t.Errorf("%dx%d: row %d is %d columns: %q", size[0], size[1], i, w, stripANSI(l))
			}
		}
	}
}
