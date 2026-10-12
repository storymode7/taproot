package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/storymode7/astra/core"
)

// stageFixture is a repository change: each file's committed and working
// text. An empty old text is a new file (git add -N); an empty new text
// is a deleted file. renames maps a new path to the old path it was
// moved from.
type stageFixture struct {
	name    string
	files   map[string][2]string
	renames map[string]string
}

var stageFixtures = []stageFixture{
	{name: "go: a function grows above a new one, its caller changes", files: map[string][2]string{"m.go": {
		"package main\n\nfunc alpha() int {\n\treturn 1\n}\n\nfunc gamma() int {\n\ta := 1\n\tb := 2\n\tc := 3\n\treturn a + b + c\n}\n\nfunc main() {\n\tprintln(gamma())\n}\n",
		"package main\n\nfunc alpha() int {\n\tx := 1\n\ty := 2\n\treturn x + y\n}\n\nfunc helper() int {\n\treturn 4\n}\n\nfunc gamma() int {\n\ta := 1\n\tb := 2\n\tc := 3\n\treturn a + b + c\n}\n\nfunc main() {\n\tprintln(gamma() + helper())\n}\n",
	}}},
	{name: "go: new function after an edited last line", files: map[string][2]string{"m.go": {
		"package main\n\nfunc alpha() int {\n\treturn x + y\n}\n\nfunc gamma() int {\n\treturn 3\n}\n",
		"package main\n\nfunc alpha() int {\n\treturn x * y\n}\n\nfunc helper() int {\n\treturn 42\n}\n\nfunc gamma() int {\n\treturn 3\n}\n",
	}}},
	{name: "go: a function removed, one grown, one added far below", files: map[string][2]string{"m.go": {
		"package main\n\nfunc a() int {\n\treturn 1\n}\n\nfunc legacy() int {\n\treturn 9\n}\n\nfunc b() int {\n\treturn 2\n}\n\nfunc c() int {\n\tx := 3\n\treturn x\n}\n",
		"package main\n\nfunc a() int {\n\tv := 1\n\tw := 2\n\treturn v + w\n}\n\nfunc b() int {\n\treturn 2\n}\n\nfunc d() int {\n\treturn 4\n}\n\nfunc c() int {\n\tx := 3\n\treturn x\n}\n",
	}}},
	{name: "python: a new function ends like the edited one above it", files: map[string][2]string{"m.py": {
		"def f(xs):\n    total = 0\n    for x in xs:\n        total += x\n    return total\n\n\ndef main():\n    print(f([1, 2]))\n",
		"def f(xs):\n    total = 0\n    for x in sorted(xs):\n        total += x\n    return total\n\n\ndef g(ys):\n    total = 1\n    for y in ys:\n        total *= y\n    return total\n\n\ndef main():\n    print(f([1, 2]), g([3]))\n",
	}}},
	{name: "python: identical siblings, one edited", files: map[string][2]string{"q.py": {
		"class LifoQueue:\n    def _init(self, maxsize):\n        self.queue = []\n\n    def _qsize(self):\n        return len(self.queue)\n\n\nclass PriorityQueue:\n    def _init(self, maxsize):\n        self.queue = []\n\n    def _qsize(self):\n        return len(self.queue)\n",
		"class LifoQueue:\n    def _init(self, maxsize):\n        self.queue = []\n\n    def _qsize(self):\n        return len(self.queue)\n\n\nclass PriorityQueue:\n    def _init(self, maxsize):\n        self.queue = [None]\n\n    def _qsize(self):\n        return len(self.queue) - 1\n",
	}}},
	{name: "typescript: a helper inserted below a growing function", files: map[string][2]string{"m.ts": {
		"export function alpha(): number {\n  return 1;\n}\n\nexport function gamma(): number {\n  const a = 1;\n  const b = 2;\n  return a + b;\n}\n\nexport function main() {\n  console.log(gamma());\n}\n",
		"export function alpha(): number {\n  const x = 1;\n  const y = 2;\n  return x + y;\n}\n\nexport function helper(): number {\n  return 4;\n}\n\nexport function gamma(): number {\n  const a = 1;\n  const b = 2;\n  return a + b;\n}\n\nexport function main() {\n  console.log(gamma() + helper());\n}\n",
	}}},
	{name: "rust: a repeated attribute and a new function", files: map[string][2]string{"lib.rs": {
		"#[inline]\nfn a() -> i32 {\n    1\n}\n\nfn b() -> i32 {\n    2\n}\n\n#[inline]\nfn c() -> i32 {\n    3\n}\n",
		"#[inline]\nfn a() -> i32 {\n    10\n}\n\n#[inline]\nfn b() -> i32 {\n    2\n}\n\nfn d() -> i32 {\n    4\n}\n\n#[inline]\nfn c() -> i32 {\n    3\n}\n",
	}}},
	{name: "python: a function removed at the end of the file", files: map[string][2]string{"r.py": {
		"def a():\n    return 1\n\n\ndef b():\n    return 2\n\n\ndef legacy():\n    return 3\n",
		"def a():\n    return 10\n\n\ndef b():\n    return 2\n",
	}}},
	{name: "python: no newline at end of file", files: map[string][2]string{"e.py": {
		"def a():\n    return 1\n\n\ndef b():\n    return 2",
		"def a():\n    return 10\n\n\ndef b():\n    return 20\n\n\ndef c():\n    return 3",
	}}},
	{name: "go: CRLF lines", files: map[string][2]string{"w.go": {
		"package p\r\n\r\nfunc a() int {\r\n\treturn 1\r\n}\r\n\r\nfunc b() int {\r\n\treturn 2\r\n}\r\n",
		"package p\r\n\r\nfunc a() int {\r\n\treturn 10\r\n}\r\n\r\nfunc b() int {\r\n\tx := 2\r\n\treturn x\r\n}\r\n\r\nfunc c() int {\r\n\treturn 3\r\n}\r\n",
	}}},
	{name: "go: an intent-to-add file", files: map[string][2]string{"new.go": {
		"",
		"package main\n\nfunc extra() int {\n\treturn 1\n}\n\nfunc more() int {\n\treturn 2\n}\n",
	}}},
	{name: "two files, one deleted", files: map[string][2]string{
		"gone.py": {"def legacy_auth():\n    return True\n", ""},
		"a.go":    {"package a\n\nfunc A() int {\n\treturn 1\n}\n", "package a\n\nfunc A() int {\n\treturn 2\n}\n\nfunc B() int {\n\treturn 3\n}\n"},
	}},
	{name: "a renamed file, edited", files: map[string][2]string{"b.py": {
		"def a():\n    return 1\n\n\ndef b():\n    return 2\n\n\ndef c():\n    return 3\n",
		"def a():\n    return 1\n\n\ndef b():\n    return 20\n\n\ndef c():\n    return 30\n",
	}}, renames: map[string]string{"b.py": "a.py"}},
}

// setUp makes the fixture's repository: the old texts committed, the new
// ones in the working tree, new files intent-to-add.
func (fx stageFixture) setUp(t *testing.T) *testRepo {
	t.Helper()
	r := newRepo(t, "")
	for p, c := range fx.files {
		if old := fx.renames[p]; old != "" {
			r.write(old, c[0])
		} else if c[0] != "" {
			r.write(p, c[0])
		}
	}
	r.write(".keep", "")
	r.commit("base")
	for p, c := range fx.files {
		if old := fx.renames[p]; old != "" {
			if err := os.Remove(filepath.Join(r.dir, old)); err != nil {
				t.Fatal(err)
			}
		}
		if c[1] == "" {
			if err := os.Remove(filepath.Join(r.dir, p)); err != nil {
				t.Fatal(err)
			}
			continue
		}
		r.write(p, c[1])
		if c[0] == "" || fx.renames[p] != "" {
			r.git("add", "-N", p)
		}
	}
	return r
}

// stageSession is one review of a fixture's working tree: its snippets
// and the function y and x call.
type stageSession struct {
	snippets []*core.Snippet
	stage    func(s *core.Snippet, accept bool) error
}

func openStageSession(t *testing.T, r *testRepo) *stageSession {
	t.Helper()
	d := r.load(t, true)
	return &stageSession{snippets: d.snippets, stage: d.stager.stage}
}

// frag is one @@ fragment of a snippet's diff lines.
type frag struct {
	oldStart, oldCount, newStart int
	pre, post                    []string
}

var reFrag = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

func frags(t *testing.T, s *core.Snippet) []frag {
	t.Helper()
	var out []frag
	for i, l := range s.DiffLines {
		if m := reFrag.FindStringSubmatch(l); m != nil {
			f := frag{oldCount: 1}
			f.oldStart, _ = strconv.Atoi(m[1])
			if m[2] != "" {
				f.oldCount, _ = strconv.Atoi(m[2])
			}
			f.newStart, _ = strconv.Atoi(m[3])
			out = append(out, f)
			continue
		}
		if len(out) == 0 || strings.HasPrefix(l, `\`) {
			continue
		}
		f := &out[len(out)-1]
		text := l[1:] + "\n"
		if i+1 < len(s.DiffLines) && strings.HasPrefix(s.DiffLines[i+1], `\`) {
			text = l[1:]
		}
		switch l[0] {
		case '-':
			f.pre = append(f.pre, text)
		case '+':
			f.post = append(f.post, text)
		default:
			f.pre = append(f.pre, text)
			f.post = append(f.post, text)
		}
	}
	return out
}

// expectIndex is the oracle: the file's base text with the fragments of
// the accepted snippets put in, each at its exact old line, from the last
// one up so no fragment moves another.
func expectIndex(t *testing.T, base string, accepted []*core.Snippet) string {
	t.Helper()
	var all []frag
	for _, s := range accepted {
		all = append(all, frags(t, s)...)
	}
	begin := func(f frag) int {
		if f.oldCount == 0 {
			return f.oldStart
		}
		return f.oldStart - 1
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if begin(a) != begin(b) {
			return begin(a) > begin(b)
		}
		if (a.oldCount == 0) != (b.oldCount == 0) {
			return a.oldCount != 0
		}
		return a.newStart > b.newStart
	})
	var lines []string
	for rest := base; rest != ""; {
		i := strings.IndexByte(rest, '\n')
		if i < 0 {
			lines = append(lines, rest)
			break
		}
		lines = append(lines, rest[:i+1])
		rest = rest[i+1:]
	}
	for _, f := range all {
		b := begin(f)
		if got := strings.Join(lines[b:b+f.oldCount], ""); got != strings.Join(f.pre, "") {
			t.Fatalf("oracle: @@ -%d,%d @@ expects %q, base has %q", f.oldStart, f.oldCount, f.pre, got)
		}
		lines = append(lines[:b], append(append([]string(nil), f.post...), lines[b+f.oldCount:]...)...)
	}
	return strings.Join(lines, "")
}

// permutations lists every order of 0..n-1.
func permutations(n int) [][]int {
	if n == 0 {
		return [][]int{{}}
	}
	var out [][]int
	for _, p := range permutations(n - 1) {
		for i := 0; i <= len(p); i++ {
			out = append(out, append(append(append([]int{}, p[:i]...), n-1), p[i:]...))
		}
	}
	return out
}

// indexText is the file's staged text, "" when the index has no entry.
func indexText(r *testRepo, path string) string {
	r.t.Helper()
	if strings.TrimSpace(r.git("ls-files", "-s", "--", path)) == "" {
		return ""
	}
	return r.git("cat-file", "blob", ":"+path)
}

// TestStage_EveryOrderGivesBasePlusAccepted presses y on every snippet of
// each fixture in every order, then x in the same order. After each key
// the index holds exactly the base text plus the fragments of the
// snippets accepted so far; accepting all gives the working tree, and
// rejecting all gives back the index astra started from.
func TestStage_EveryOrderGivesBasePlusAccepted(t *testing.T) {
	for _, fx := range stageFixtures {
		t.Run(fx.name, func(t *testing.T) {
			r := fx.setUp(t)
			start := r.git("status", "--porcelain=v1", "-z")
			index, err := os.ReadFile(filepath.Join(r.dir, ".git", "index"))
			if err != nil {
				t.Fatal(err)
			}
			n := len(openStageSession(t, r).snippets)
			if n == 0 || n > 5 {
				t.Fatalf("fixture makes %d snippets", n)
			}
			for _, order := range permutations(n) {
				if err := os.WriteFile(filepath.Join(r.dir, ".git", "index"), index, 0o644); err != nil {
					t.Fatal(err)
				}
				sess := openStageSession(t, r)
				accepted := map[int]bool{}
				check := func(step string) bool {
					t.Helper()
					for p := range fx.files {
						var acc []*core.Snippet
						for i, s := range sess.snippets {
							if accepted[i] && s.FilePath == p {
								acc = append(acc, s)
							}
						}
						want := expectIndex(t, fx.files[p][0], acc)
						switch {
						case fx.files[p][1] == "" && len(acc) > 0:
							want = "" // the deletion is staged
						case fx.renames[p] != "" && len(acc) == 0:
							want = "" // the new path is intent-to-add again
						}
						if got := indexText(r, p); got != want {
							t.Errorf("order %v, %s: index of %s =\n%q\nwant\n%q", order, step, p, got, want)
							return false
						}
						if old := fx.renames[p]; old != "" {
							want := fx.files[p][0]
							if len(acc) > 0 {
								want = "" // the rename is staged
							}
							if got := indexText(r, old); got != want {
								t.Errorf("order %v, %s: index of %s = %q, want %q", order, step, old, got, want)
								return false
							}
						}
					}
					return true
				}
				ok := true
				for _, i := range order {
					s := sess.snippets[i]
					if err := sess.stage(s, true); err != nil {
						t.Errorf("order %v: y on %s %q: %v", order, s.FilePath, s.SymbolPath, err)
						ok = false
						break
					}
					accepted[i] = true
					if ok = check(fmt.Sprintf("after y on %q", s.SymbolPath)); !ok {
						break
					}
				}
				if !ok {
					continue
				}
				for p, c := range fx.files {
					if got := indexText(r, p); got != c[1] {
						t.Errorf("order %v: all accepted, index of %s = %q, want the working tree %q", order, p, got, c[1])
					}
				}
				for _, i := range order {
					s := sess.snippets[i]
					if err := sess.stage(s, false); err != nil {
						t.Errorf("order %v: x on %s %q: %v", order, s.FilePath, s.SymbolPath, err)
						ok = false
						break
					}
					delete(accepted, i)
					if ok = check(fmt.Sprintf("after x on %q", s.SymbolPath)); !ok {
						break
					}
				}
				if got := r.git("status", "--porcelain=v1", "-z"); ok && got != start {
					t.Errorf("order %v: all rejected, git status %q, want %q as at start", order, got, start)
				}
			}
		})
	}
}

// TestStage_EachSnippetAloneParses stages one snippet at a time: a Go file
// must still parse, and pressing y again must change nothing. A new file's
// package clause is a snippet of its own, so only files with a base count.
func TestStage_EachSnippetAloneParses(t *testing.T) {
	for _, fx := range stageFixtures {
		r := fx.setUp(t)
		index, err := os.ReadFile(filepath.Join(r.dir, ".git", "index"))
		if err != nil {
			t.Fatal(err)
		}
		n := len(openStageSession(t, r).snippets)
		for i := range n {
			if err := os.WriteFile(filepath.Join(r.dir, ".git", "index"), index, 0o644); err != nil {
				t.Fatal(err)
			}
			sess := openStageSession(t, r)
			s := sess.snippets[i]
			for range 2 {
				if err := sess.stage(s, true); err != nil {
					t.Fatalf("%s: y on %s %q: %v", fx.name, s.FilePath, s.SymbolPath, err)
				}
			}
			staged := indexText(r, s.FilePath)
			if want := expectIndex(t, fx.files[s.FilePath][0], []*core.Snippet{s}); fx.files[s.FilePath][1] != "" && staged != want {
				t.Errorf("%s: y y on %q staged\n%q\nwant\n%q", fx.name, s.SymbolPath, staged, want)
			}
			if strings.HasSuffix(s.FilePath, ".go") && fx.files[s.FilePath][0] != "" {
				if _, err := parser.ParseFile(token.NewFileSet(), s.FilePath, staged, 0); err != nil {
					t.Errorf("%s: staging only %q leaves an index that does not parse: %v\n%s", fx.name, s.SymbolPath, err, staged)
				}
			}
		}
	}
}

// staleRepo is a.go with alpha and beta edited, not staged.
func staleRepo(t *testing.T) *testRepo {
	t.Helper()
	r := newRepo(t, "")
	r.write("a.go", "package a\n\nfunc alpha() int {\n\treturn 1\n}\n\nfunc beta() int {\n\treturn 2\n}\n")
	r.commit("base")
	r.write("a.go", "package a\n\nfunc alpha() int {\n\treturn 10\n}\n\nfunc beta() int {\n\treturn 20\n}\n")
	return r
}

func snippetNamed(t *testing.T, d *loaded, symbol string) *core.Snippet {
	t.Helper()
	for _, s := range d.snippets {
		if s.SymbolPath == symbol {
			return s
		}
	}
	t.Fatalf("no snippet %q in %v", symbol, symbols(d.snippets))
	return nil
}

// TestStage_SeesChangesOutsideAstra: whatever changes the diff on screen
// outside astra is reported before y, x or r stage anything: an editor,
// git stash, checkout, add or commit in another shell, or a second astra.
// astra's own writes are not.
func TestStage_SeesChangesOutsideAstra(t *testing.T) {
	for _, c := range []struct {
		name  string
		act   func(r *testRepo)
		want  string
		stage bool // y on alpha first
	}{
		{"nothing", func(*testRepo) {}, "", true},
		{"an edit", func(r *testRepo) {
			r.write("a.go", strings.Replace(r.git("show", ":a.go"), "return 1\n", "return 11\n", 1))
		}, "a.go changed on disk", false},
		{"git checkout", func(r *testRepo) { r.git("checkout", "--", "a.go") }, "a.go changed on disk", false},
		{"git stash", func(r *testRepo) { r.git("stash", "-q") }, "the index entry of a.go changed", true},
		{"git add", func(r *testRepo) { r.git("add", "a.go") }, "the index entry of a.go changed", false},
		{"git commit", func(r *testRepo) { r.git("commit", "-qm", "alpha") }, "HEAD moved", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := staleRepo(t)
			d := r.load(t, true)
			if c.stage {
				if err := d.stager.stage(snippetNamed(t, d, "alpha"), true); err != nil {
					t.Fatal(err)
				}
			}
			c.act(r)
			if got := d.stager.changed(); got != c.want {
				t.Errorf("changed() = %q, want %q", got, c.want)
			}
		})
	}
}

// TestStage_RefusesAnIndexItDidNotWrite proves the guard: when a file's
// index entry is not the one astra found or last wrote, y and x change
// nothing and say to restart.
func TestStage_RefusesAnIndexItDidNotWrite(t *testing.T) {
	for _, act := range [][]string{{"add", "a.go"}, {"stash", "-q"}, {"reset", "-q"}} {
		t.Run(act[0], func(t *testing.T) {
			r := staleRepo(t)
			d := r.load(t, true)
			alpha, beta := snippetNamed(t, d, "alpha"), snippetNamed(t, d, "beta")
			if err := d.stager.stage(alpha, true); err != nil {
				t.Fatal(err)
			}
			r.git(act...)
			before := r.git("ls-files", "-s")
			for _, k := range []struct {
				s      *core.Snippet
				accept bool
			}{{beta, true}, {alpha, false}} {
				err := d.stager.stage(k.s, k.accept)
				if err == nil || err.Error() != "a.go changed in the index outside astra; restart astra to review it again" {
					t.Errorf("%s %q: err = %v, want a refusal that says to restart", map[bool]string{true: "y", false: "x"}[k.accept], k.s.SymbolPath, err)
				}
				if after := r.git("ls-files", "-s"); after != before {
					t.Errorf("the refusal changed the index:\n%s\nwas\n%s", after, before)
				}
			}
		})
	}
}

// TestStage_TwoSessions: a second astra on the same working tree sees the
// first one's y as a change outside it and stages nothing over it.
func TestStage_TwoSessions(t *testing.T) {
	r := staleRepo(t)
	first, second := r.load(t, true), r.load(t, true)
	if err := first.stager.stage(snippetNamed(t, first, "alpha"), true); err != nil {
		t.Fatal(err)
	}
	if got := second.stager.changed(); got != "the index entry of a.go changed" {
		t.Errorf("second session: changed() = %q", got)
	}
	before := r.git("ls-files", "-s")
	if err := second.stager.stage(snippetNamed(t, second, "beta"), true); err == nil {
		t.Error("the second session staged over the first one's y")
	}
	if after := r.git("ls-files", "-s"); after != before {
		t.Errorf("the refusal changed the index:\n%s\nwas\n%s", after, before)
	}
}

// TestStage_IntentToAddKeepsItsMode: a new executable file is staged with
// its mode, and x gives it back its intent-to-add entry even when it is
// gitignored (it was added with git add -N -f).
func TestStage_IntentToAddKeepsItsMode(t *testing.T) {
	r := newRepo(t, "")
	r.write(".gitignore", "run.py\n")
	r.commit("base")
	r.write("run.py", "def main():\n    return 1\n\n\ndef helper():\n    return 2\n")
	if err := os.Chmod(filepath.Join(r.dir, "run.py"), 0o755); err != nil {
		t.Fatal(err)
	}
	r.git("add", "-N", "-f", "run.py")
	start := r.git("status", "--porcelain=v1", "-z", "--ignored")
	d := r.load(t, true)
	for _, s := range d.snippets {
		if err := d.stager.stage(s, true); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Fields(r.git("ls-files", "-s", "run.py")); len(got) < 1 || got[0] != "100755" {
		t.Errorf("staged entry %q, want mode 100755", got)
	}
	for _, s := range d.snippets {
		if err := d.stager.stage(s, false); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.git("status", "--porcelain=v1", "-z", "--ignored"); got != start {
		t.Errorf("after x on every snippet, git status %q, want %q", got, start)
	}
}
