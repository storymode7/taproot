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
		{"jsx tags are not regexes", "typescript",
			[]string{
				`{items.map((i) => <Row key={i.id} />)}{helper(a)}`,
				`{open && <Modal onClose={close} />}`,
				`        />))}`,
				`<span>{a} / {ratio(b)}</span>`,
				`<p className="error">{a}</p>{tail(a)}`,
			},
			[]string{"helper(a)", "/>}", "/>))}", "ratio(b)", "tail(a)"}, nil},
		{"js regex opening with > at line start", "javascript",
			[]string{"s.replace(", `  />/g, "&gt;")`},
			nil, []string{"/>/"}},
		{"jsx text apostrophes and inch marks are not strings", "typescript",
			[]string{
				`{a && <p className="x">Can't load: {helper(a)}</p>}`,
				`<p>Don't panic. {calm(a)}</p>`,
				`<p>Don't {first(a)} - it's {second(a)}</p>`,
				`<p>Don't panic: {third(a, 'cart')}</p>`,
				`<p>Fits 12" laptops: {fourth(e)}</p>`,
			},
			[]string{"helper(a)", "</p>}", "calm(a)", "first(a)", "second(a)", "third(a,", "fourth(e)"},
			[]string{"cart"}},
		{"jsx text url is not a comment", "typescript",
			[]string{
				`<p>See https://example.com for {helper(a)}</p>`,
				`{a && <a href={a}>http://localhost:3000</a>}{tail(a)}`,
				`x(); // see https://example.com and y()`,
			},
			[]string{"helper(a)", "</a>}", "tail(a)", "x()"}, []string{"see", "y()"}},
		{"js string literals still mask", "javascript",
			[]string{`x = 'a(b)'; keep()`, `y = 'it\'s z()' + f()`, `return"q()"`, `case'k()': g()`, `s = 'abc \`},
			[]string{"keep()", "f()", "g()"}, []string{"a(b)", "z()", "q()", "k()", "abc"}},
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
