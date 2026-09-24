package core

import (
	"strings"
	"testing"
)

func TestMaskCode(t *testing.T) {
	cases := []struct {
		name, lang string
		in         []string
		keep, drop []string // substrings that must survive / be masked
	}{
		{"go strings and comments", "go",
			[]string{`a("x(") // b()`, "c := `", "d()", "` + e() /* f() */ + g()"},
			[]string{"a(", "e()", "g()"}, []string{"x(", "b()", "d()", "f()"}},
		{"go rune literals", "go",
			[]string{`if r == '"' { h() }`, `s := '\'' ; k()`},
			[]string{"h()", "k()"}, nil},
		{"js template interpolation", "typescript",
			[]string{"x = `n ${count(a)} parse()` + `${`in ${deep()}`}`"},
			[]string{"count(", "deep("}, []string{"parse("}},
		{"js regex literal", "javascript",
			[]string{`s.replace(/\/*$/, "") ; keep()`, "const r = /`/; after()", "after2()"},
			[]string{"keep()", "after()", "after2()"}, nil},
		{"js division is not a regex", "javascript",
			[]string{`x = a / b / c(); y = "s" / d()`},
			[]string{"c()", "d()"}, nil},
		{"python f-string", "python",
			[]string{`return f"total: {total(items)} {{skip()}} none()"`},
			[]string{"total("}, []string{"skip(", "none("}},
		{"python triple quotes", "python",
			[]string{`s = """`, "run()", `"""`, "real()"},
			[]string{"real()"}, []string{"run()"}},
		{"rust raw and multi-line strings", "rust",
			[]string{`let a = r#"`, "fn run() {}", `"#; one();`, `let b = "`, "two()", `"; three();`},
			[]string{"one()", "three()"}, []string{"run()", "two()"}},
		{"rust lifetimes", "rust",
			[]string{"fn get<'a>(s: &'a str) -> u8 { decode(s) }"},
			[]string{"decode("}, nil},
		{"java text block", "java",
			[]string{`String s = """`, "  run();", `  """; real();`},
			[]string{"real()"}, []string{"run()"}},
	}
	for _, tc := range cases {
		got := strings.Join(maskCode(tc.in, tc.lang), "\n")
		for _, k := range tc.keep {
			if !strings.Contains(got, k) {
				t.Errorf("%s: %q was masked:\n%s", tc.name, k, got)
			}
		}
		for _, d := range tc.drop {
			if strings.Contains(got, d) {
				t.Errorf("%s: %q survived:\n%s", tc.name, d, got)
			}
		}
	}
}
