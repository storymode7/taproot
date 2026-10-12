package core

import (
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"testing"
)

// The property behind staging: after the entity split, every changed line
// belongs to exactly one snippet and every fragment carries exact old-side
// positions. Applying the fragments of any subset of snippets to the old
// text, in old-position order, then gives a well-formed file, and applying
// all of them gives the new text exactly.
//
// Files are generated as functions of a fixed shape, at the top level or as
// methods of a class, edited at random, and diffed with every alignment git
// may choose for an added or deleted block (a block can slide over equal
// lines: a shared "}", a shared last statement, a shared decorator). Old and
// new files may end without a newline.

// genLine is one line of a generated file and whether a newline ends it.
type genLine struct {
	text string
	nl   bool
}

type genFunc struct {
	name string
	deco bool     // Python: @cache above the def
	body []string // statements, the last one returns
}

type genFile struct {
	lang  string
	class bool // the functions are methods of one class
	funcs []genFunc
	gaps  []int // blank lines before each function
	eol   bool
}

// genLayout is how a generated file writes its functions.
type genLayout struct {
	open, close     string // the class header and footer, "" outside a class
	decl            string // a declaration, with %s for the name
	declRe          *regexp.Regexp
	deco, body, end string // decorator line, body indent, closing line
}

func (f genFile) layout() genLayout {
	l := genLayout{deco: "@cache", body: "  ", end: "}"}
	switch {
	case f.lang == "go":
		l.decl, l.body = "func %s() int {", "\t"
	case f.lang == "python" && f.class:
		l.open, l.decl, l.deco, l.body, l.end = "class C:", "    def %s(self):", "    @cache", "        ", ""
	case f.lang == "python":
		l.decl, l.body, l.end = "def %s():", "    ", ""
	case f.class:
		l.open, l.close, l.decl, l.body, l.end = "class C {", "}", "  %s() {", "    ", "  }"
	default:
		l.decl = "function %s() {"
	}
	l.declRe = regexp.MustCompile("^" + strings.Replace(regexp.QuoteMeta(l.decl), "%s", `\w+`, 1) + "$")
	return l
}

var genHeads = map[string]string{"go": "package p", "python": "import os", "javascript": `"use strict";`}

func (f genFile) clone() genFile {
	g := f
	g.funcs = make([]genFunc, len(f.funcs))
	for i, fn := range f.funcs {
		fn.body = append([]string(nil), fn.body...)
		g.funcs[i] = fn
	}
	g.gaps = append([]int(nil), f.gaps...)
	return g
}

func (f genFile) lines() []genLine {
	lay := f.layout()
	out := []string{genHeads[f.lang]}
	if lay.open != "" {
		out = append(out, "", lay.open)
	}
	for i, fn := range f.funcs {
		for k := 0; k < f.gaps[i]; k++ {
			out = append(out, "")
		}
		if fn.deco {
			out = append(out, lay.deco)
		}
		out = append(out, fmt.Sprintf(lay.decl, fn.name))
		for _, st := range fn.body {
			out = append(out, lay.body+st)
		}
		if lay.end != "" {
			out = append(out, lay.end)
		}
	}
	if lay.close != "" {
		out = append(out, lay.close)
	}
	lines := make([]genLine, len(out))
	for i, t := range out {
		lines[i] = genLine{t, true}
	}
	lines[len(lines)-1].nl = f.eol
	return lines
}

func genAssign(lang, v string, n int) string {
	switch lang {
	case "go":
		return fmt.Sprintf("%s := %d", v, n)
	case "javascript":
		return fmt.Sprintf("const %s = %d;", v, n)
	}
	return fmt.Sprintf("%s = %d", v, n)
}

// genReturn returns v; most functions end with the same statement, so an
// added function can take the last line of the one above it.
func genReturn(lang string, plus int) string {
	s := "return v"
	if plus > 0 {
		s += fmt.Sprintf(" + %d", plus)
	}
	if lang == "javascript" {
		s += ";"
	}
	return s
}

func genFunction(rng *rand.Rand, lang, name string) genFunc {
	fn := genFunc{name: name, deco: lang == "python" && rng.Intn(3) == 0}
	for k := rng.Intn(2); k >= 0; k-- {
		fn.body = append(fn.body, genAssign(lang, "v", rng.Intn(9)))
	}
	plus := 0
	if rng.Intn(4) == 0 {
		plus = 1 + rng.Intn(3)
	}
	fn.body = append(fn.body, genReturn(lang, plus))
	return fn
}

func genGap(rng *rand.Rand, lang string) int {
	if lang == "python" {
		return 1 + rng.Intn(2)
	}
	return 1 + rng.Intn(4)/3
}

// genEdit applies one random edit that keeps every function well formed.
func genEdit(rng *rand.Rand, f *genFile, next *int) {
	i := rng.Intn(len(f.funcs))
	fn := &f.funcs[i]
	last := len(fn.body) - 1
	switch rng.Intn(9) {
	case 0: // change a statement
		k := rng.Intn(len(fn.body))
		if k == last {
			fn.body[k] = genReturn(f.lang, 4+rng.Intn(5))
		} else {
			fn.body[k] = genAssign(f.lang, "v", 10+rng.Intn(90))
		}
	case 1: // change the last statement
		fn.body[last] = genReturn(f.lang, 4+rng.Intn(5))
	case 2: // add a statement
		k := rng.Intn(len(fn.body))
		fn.body = append(fn.body[:k], append([]string{genAssign(f.lang, "w", rng.Intn(9))}, fn.body[k:]...)...)
	case 3: // drop a statement
		if last > 0 {
			k := rng.Intn(last)
			fn.body = append(fn.body[:k], fn.body[k+1:]...)
		}
	case 4, 5: // add a function, often right after one whose end changed
		*next++
		nf := genFunction(rng, f.lang, fmt.Sprintf("n%d", *next))
		k := rng.Intn(len(f.funcs) + 1)
		f.funcs = append(f.funcs[:k], append([]genFunc{nf}, f.funcs[k:]...)...)
		f.gaps = append(f.gaps[:k], append([]int{genGap(rng, f.lang)}, f.gaps[k:]...)...)
	case 6: // drop a function
		if len(f.funcs) > 1 {
			f.funcs = append(f.funcs[:i], f.funcs[i+1:]...)
			f.gaps = append(f.gaps[:i], f.gaps[i+1:]...)
		}
	case 7: // change the blank lines above a function
		if f.gaps[i] > 1 && rng.Intn(2) == 0 {
			f.gaps[i]--
		} else {
			f.gaps[i]++
		}
	case 8: // rename a function, or toggle its decorator
		if f.lang == "python" && rng.Intn(2) == 0 {
			fn.deco = !fn.deco
		} else {
			*next++
			fn.name = fmt.Sprintf("r%d", *next)
		}
	}
}

// genDiff diffs a against b by longest common subsequence, then puts each
// pure block of added or deleted lines at a random position in the range it
// can slide over, as git's alignment heuristics may.
func genDiff(rng *rand.Rand, a, b []genLine) []diffLine {
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	type op struct {
		kind byte
		l    genLine
	}
	var ops []op
	for i, j := 0, 0; i < n || j < m; {
		switch {
		case i < n && j < m && a[i] == b[j]:
			ops = append(ops, op{' ', a[i]})
			i, j = i+1, j+1
		case j == m || i < n && lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, op{'-', a[i]})
			i++
		default:
			ops = append(ops, op{'+', b[j]})
			j++
		}
	}
	block := func(s int) int {
		e := s
		for e < len(ops) && ops[e].kind != ' ' {
			e++
		}
		return e
	}
	for s := 0; s < len(ops); {
		if ops[s].kind == ' ' {
			s++
			continue
		}
		e := block(s)
		pure := true
		for _, o := range ops[s:e] {
			pure = pure && o.kind == ops[s].kind
		}
		if pure {
			up := 0
			for s-up-1 >= 0 && ops[s-up-1].kind == ' ' && ops[s-up-1].l == ops[e-up-1].l {
				up++
			}
			down := 0
			for e+down < len(ops) && ops[e+down].kind == ' ' && ops[e+down].l == ops[s+down].l {
				down++
			}
			k := rng.Intn(up+down+1) - up
			for ; k < 0; k++ { // the context line above joins the block, its last line leaves
				ops[s-1].kind, ops[e-1].kind = ops[e-1].kind, ' '
				s, e = s-1, e-1
			}
			for ; k > 0; k-- {
				ops[e].kind, ops[s].kind = ops[s].kind, ' '
				s, e = s+1, e+1
			}
		}
		s = e
	}
	// Within a block of changes git lists deletions before additions.
	for s := 0; s < len(ops); {
		if ops[s].kind == ' ' {
			s++
			continue
		}
		e := block(s)
		var del, add []op
		for _, o := range ops[s:e] {
			if o.kind == '-' {
				del = append(del, o)
			} else {
				add = append(add, o)
			}
		}
		copy(ops[s:], append(del, add...))
		s = e
	}
	out := make([]diffLine, 0, len(ops))
	for _, o := range ops {
		out = append(out, diffLine{text: string(o.kind) + o.l.text, kind: o.kind})
		if !o.l.nl {
			out = append(out, diffLine{text: `\ No newline at end of file`, kind: '\\'})
		}
	}
	return out
}

// genHunks cuts a diff into hunks with three lines of context, as git does.
func genHunks(d []diffLine) []Hunk {
	const ctx = 3
	var changes []int
	for i, l := range d {
		if l.kind == '+' || l.kind == '-' {
			changes = append(changes, i)
		}
	}
	var hunks []Hunk
	for c := 0; c < len(changes); {
		first, last := changes[c], changes[c]
		for c++; c < len(changes) && changes[c]-last-1 <= 2*ctx; c++ {
			last = changes[c]
		}
		s := first
		for k := 0; k < ctx && s > 0; s-- {
			if d[s-1].kind == ' ' {
				k++
			}
		}
		e := last + 1
		for k := 0; e < len(d) && (k < ctx || d[e].kind == '\\'); e++ {
			if d[e].kind == ' ' {
				k++
			}
		}
		oldBefore, newBefore := 0, 0
		for _, l := range d[:s] {
			if l.kind != '+' && l.kind != '\\' {
				oldBefore++
			}
			if l.kind != '-' && l.kind != '\\' {
				newBefore++
			}
		}
		h := Hunk{OldStart: oldBefore + 1, NewStart: newBefore + 1}
		var body []string
		for _, l := range d[s:e] {
			body = append(body, l.text)
			switch l.kind {
			case ' ':
				h.OldLines++
				h.NewLines++
			case '-':
				h.OldLines++
			case '+':
				h.NewLines++
			}
		}
		if h.OldLines == 0 {
			h.OldStart--
		}
		if h.NewLines == 0 {
			h.NewStart--
		}
		h.Lines = append([]string{fmt.Sprintf("@@ -%d,%d +%d,%d @@", h.OldStart, h.OldLines, h.NewStart, h.NewLines)}, body...)
		hunks = append(hunks, h)
	}
	return hunks
}

func genText(lines []genLine) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.text)
		if l.nl {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// genShapeErr reports the first place where text is not a header followed
// by whole functions (in their class, for methods), each ending in its own
// return and, outside Python, its own closing brace.
func genShapeErr(f genFile, text string) error {
	lay := f.layout()
	ls := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if ls[0] != genHeads[f.lang] {
		return fmt.Errorf("line 1 is %q", ls[0])
	}
	i := 1
	for i < len(ls) && ls[i] == "" {
		i++
	}
	if lay.open != "" {
		if i == len(ls) || ls[i] != lay.open {
			return fmt.Errorf("no class header")
		}
		i++
	}
	closed := lay.close == ""
	for i < len(ls) {
		if ls[i] == "" {
			i++
			continue
		}
		if closed && lay.close != "" {
			return fmt.Errorf("line %d after the class: %q", i+1, ls[i])
		}
		if lay.close != "" && ls[i] == lay.close {
			closed = true
			i++
			continue
		}
		if f.lang == "python" && ls[i] == lay.deco {
			i++
		}
		if i == len(ls) || !lay.declRe.MatchString(ls[i]) {
			return fmt.Errorf("line %d does not start a function: %q", i+1, ls[min(i, len(ls)-1)])
		}
		start := i
		var body []string
		for i++; i < len(ls) && strings.HasPrefix(ls[i], lay.body); i++ {
			body = append(body, ls[i])
		}
		for k, st := range body {
			if strings.HasPrefix(st, lay.body+"return") != (k == len(body)-1) {
				return fmt.Errorf("function at line %d: body %q does not end in its one return", start+1, body)
			}
		}
		if len(body) == 0 {
			return fmt.Errorf("function at line %d has no body", start+1)
		}
		if lay.end != "" {
			if i == len(ls) || ls[i] != lay.end {
				return fmt.Errorf("function at line %d is not closed", start+1)
			}
			i++
		}
	}
	if !closed {
		return fmt.Errorf("class not closed")
	}
	return nil
}

func TestSplitStaging_AnySubsetOfSnippetsApplies(t *testing.T) {
	if cases := splitStagingProperty(t, 7, 3000); cases < 2500 {
		t.Fatalf("only %d cases generated", cases)
	}
}

// splitStagingProperty checks the property on iterations random files and
// returns how many differed from their edited version.
func splitStagingProperty(t *testing.T, seed int64, iterations int) int {
	t.Helper()
	ext := map[string]string{"go": ".go", "python": ".py", "javascript": ".js"}
	rng := rand.New(rand.NewSource(seed))
	cases := 0
	for iter := 0; iter < iterations; iter++ {
		lang := []string{"go", "python", "javascript"}[iter%3]
		old := genFile{lang: lang, class: lang != "go" && rng.Intn(3) == 0, eol: rng.Intn(4) > 0}
		next := 0
		for k := 1 + rng.Intn(4); k > 0; k-- {
			next++
			old.funcs = append(old.funcs, genFunction(rng, lang, fmt.Sprintf("f%d", next)))
			old.gaps = append(old.gaps, genGap(rng, lang))
		}
		nw := old.clone()
		for k := 1 + rng.Intn(3); k > 0; k-- {
			genEdit(rng, &nw, &next)
		}
		if rng.Intn(5) == 0 {
			nw.eol = !nw.eol
		}
		oldLines, newLines := old.lines(), nw.lines()
		oldText, newText := genText(oldLines), genText(newLines)
		if oldText == newText {
			continue
		}
		hunks := genHunks(genDiff(rng, oldLines, newLines))
		cases++

		name := "f" + ext[lang]
		snippets := BuildSnippets([]*FileDiff{{Path: name, OldPath: name, Language: lang,
			Source: []byte(newText), Hunks: hunks}})
		fail := func(format string, args ...any) {
			t.Helper()
			var hunkText []string
			for _, h := range hunks {
				hunkText = append(hunkText, h.Lines...)
			}
			t.Fatalf("case %d (%s): %s\nold:\n%s\nnew:\n%s\ndiff:\n%s", iter, lang, fmt.Sprintf(format, args...),
				oldText, newText, strings.Join(hunkText, "\n"))
		}
		var allLines []string
		for _, h := range hunks {
			allLines = append(allLines, h.Lines...)
		}
		checkNamedContext(t, lang, oldText, newText, allLines, snippets)
		frags := make([][]fragment, len(snippets))
		for k, s := range snippets {
			f, err := fragments(s.DiffLines)
			if err != nil {
				fail("snippet %q: %v", s.SymbolPath, err)
			}
			frags[k] = f
		}
		var all []fragment
		for _, f := range frags {
			all = append(all, f...)
		}
		got, err := applyFragments(oldText, all, true)
		if err != nil {
			fail("applying every snippet: %v", err)
		}
		if got != newText {
			fail("applying every snippet gives\n%s", got)
		}
		subsets := 1 << len(snippets)
		for k := 0; k < min(subsets, 64); k++ {
			mask := k
			if subsets > 64 {
				mask = rng.Intn(subsets)
			}
			var picked []fragment
			var names []string
			for j := range snippets {
				if mask&(1<<j) != 0 {
					picked = append(picked, frags[j]...)
					names = append(names, snippets[j].SymbolPath)
				}
			}
			res, err := applyFragments(oldText, picked, false)
			if err != nil {
				fail("applying %q: %v", names, err)
			}
			if err := genShapeErr(old, res); err != nil {
				fail("applying %q gives a broken file: %v\n%s", names, err, res)
			}
		}
	}
	return cases
}
