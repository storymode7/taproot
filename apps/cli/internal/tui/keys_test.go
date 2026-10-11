package tui

import (
	"reflect"
	"testing"

	"github.com/storymode7/astra/core"
)

// keysOf returns the keys readKey reports for input.
func keysOf(t *testing.T, input string) []string {
	t.Helper()
	feedStdin(t, input).Close()
	var keys []string
	for k := readKey(); k != ""; k = readKey() {
		keys = append(keys, k)
	}
	return keys
}

func TestReadKey_WholeEscapeSequences(t *testing.T) {
	for in, want := range map[string][]string{
		"\x1b[A":        {"\x1b[A"},
		"\x1b[1;5A":     {"\x1b[A"}, // Ctrl+↑: the tail "5A" must not accept
		"\x1b[1;2A":     {"\x1b[A"}, // Shift+↑
		"\x1b[1;3A":     {"\x1b[A"}, // Alt+↑
		"\x1b[1;2C":     {"\x1b[C"}, // Shift+→: the tail "C" must not collapse
		"\x1b\x1b[Ay":   {"\x1b[A", "y"},
		"\x1bOA":        {"\x1b[A"},
		"\x1bOH":        {"\x1b[H"},
		"\x1b[1;5H":     {"\x1b[H"},
		"\x1b[1~":       {"\x1b[1~"},
		"\x1b[3;5~":     {"\x1b[3;5~"},
		"\x1b[[A":       {"\x1b[[A"}, // F1 on the Linux console
		"\x1by":         {"\x1by"},   // Alt+y must not accept
		"J\x1b[1;5BK":   {"J", "\x1b[B", "K"},
		"\x1b[1;5A\x1b": {"\x1b[A", "\x1b"},
	} {
		if got := keysOf(t, in); !reflect.DeepEqual(got, want) {
			t.Errorf("keys for %q = %q, want %q", in, got, want)
		}
	}
}

func TestReadKey_BareEscape(t *testing.T) {
	w := feedStdin(t, "\x1b")
	if k := readKey(); k != "\x1b" {
		t.Errorf("bare Esc = %q, want \\x1b", k)
	}
	w.Close()
}

func helpRunner(t *testing.T) *Runner {
	return testRunner(t, &core.Snippet{
		ID: "a", FilePath: "a.go", SymbolPath: "A", NodeType: "function",
		DiffLines: []string{"@@ -1 +1 @@", "+x"},
	})
}

func TestRenderHelp_ReadsTheWholeKey(t *testing.T) {
	r := helpRunner(t)
	fixedSize(t, 24, 80)
	feedStdin(t, "\x1b[Ay").Close()
	captureStdout(t, r.renderHelp)
	if k := readKey(); k != "y" {
		t.Errorf("key after closing help with ↑ = %q, want y: the arrow's tail must not run", k)
	}
}

func TestPromptSearch_IgnoresEscapeSequences(t *testing.T) {
	r := helpRunner(t)
	fixedSize(t, 24, 80)
	feedStdin(t, "\x1b[1;5Afoo\r").Close()
	captureStdout(t, r.promptSearch)
	if r.input != "foo" {
		t.Errorf("search input = %q, want foo", r.input)
	}
}
