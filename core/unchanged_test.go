package core

import (
	"reflect"
	"testing"
)

// ucFile builds a FileDiff with source for unchanged-caller tests.
func ucFile(path, lang, src string, hunks ...Hunk) *FileDiff {
	return &FileDiff{Path: path, OldPath: path, Language: lang, Source: []byte(src), Hunks: hunks}
}

// ucSnippet finds the snippet with the given file path and symbol path.
func ucSnippet(t *testing.T, snippets []*Snippet, path, symbol string) *Snippet {
	t.Helper()
	for _, s := range snippets {
		if s.FilePath == path && s.SymbolPath == symbol {
			return s
		}
	}
	t.Fatalf("no snippet %s::%s in %v", path, symbol, snippets)
	return nil
}

func TestAnnotateUnchangedCallers_SameFileCaller(t *testing.T) {
	src := `package p

func handle() {
	parse()
}

func parse() int {
	return 2
}
`
	f := ucFile("a.go", "go", src,
		Hunk{OldStart: 8, OldLines: 1, NewStart: 8, NewLines: 1,
			Lines: []string{"-\treturn 1", "+\treturn 2"}})

	files := []*FileDiff{f}
	snippets := BuildSnippets(files)
	AnnotateUnchangedCallers(files, snippets)

	parse := ucSnippet(t, snippets, "a.go", "parse")
	want := []string{"a.go::handle"}
	if !reflect.DeepEqual(parse.UnchangedCallers, want) {
		t.Errorf("UnchangedCallers = %v, want %v", parse.UnchangedCallers, want)
	}
}

func TestAnnotateUnchangedCallers_ChangedCallerNotListed(t *testing.T) {
	src := `package p

func handle() {
	parse()
}

func parse() int {
	return 2
}
`
	f := ucFile("a.go", "go", src,
		Hunk{OldStart: 4, OldLines: 0, NewStart: 4, NewLines: 1,
			Lines: []string{"+\tparse()"}},
		Hunk{OldStart: 8, OldLines: 1, NewStart: 8, NewLines: 1,
			Lines: []string{"-\treturn 1", "+\treturn 2"}})

	files := []*FileDiff{f}
	snippets := BuildSnippets(files)
	AnnotateUnchangedCallers(files, snippets)

	parse := ucSnippet(t, snippets, "a.go", "parse")
	if len(parse.UnchangedCallers) != 0 {
		t.Errorf("changed caller listed as unchanged: %v", parse.UnchangedCallers)
	}
}

func TestAnnotateUnchangedCallers_CrossFile(t *testing.T) {
	srcA := `package p

func parse() int {
	return 2
}
`
	srcB := `package p

func boot() {
	parse()
}

func other() {
	x := 1
	_ = x
}
`
	fa := ucFile("a.go", "go", srcA,
		Hunk{OldStart: 4, OldLines: 1, NewStart: 4, NewLines: 1,
			Lines: []string{"-\treturn 1", "+\treturn 2"}})
	fb := ucFile("b.go", "go", srcB,
		Hunk{OldStart: 8, OldLines: 0, NewStart: 8, NewLines: 1,
			Lines: []string{"+\tx := 1"}})

	files := []*FileDiff{fa, fb}
	snippets := BuildSnippets(files)
	AnnotateUnchangedCallers(files, snippets)

	parse := ucSnippet(t, snippets, "a.go", "parse")
	want := []string{"b.go::boot"}
	if !reflect.DeepEqual(parse.UnchangedCallers, want) {
		t.Errorf("UnchangedCallers = %v, want %v", parse.UnchangedCallers, want)
	}
	other := ucSnippet(t, snippets, "b.go", "other")
	if len(other.UnchangedCallers) != 0 {
		t.Errorf("other has unexpected callers: %v", other.UnchangedCallers)
	}
}

func TestAnnotateUnchangedCallers_MethodResolution(t *testing.T) {
	src := `class Spider:
    def start(self):
        self.parse(None)

    def parse(self, response):
        return 2
`
	f := ucFile("spider.py", "python", src,
		Hunk{OldStart: 6, OldLines: 1, NewStart: 6, NewLines: 1,
			Lines: []string{"-        return 1", "+        return 2"}})

	files := []*FileDiff{f}
	snippets := BuildSnippets(files)
	AnnotateUnchangedCallers(files, snippets)

	parse := ucSnippet(t, snippets, "spider.py", "Spider.parse")
	want := []string{"spider.py::Spider.start"}
	if !reflect.DeepEqual(parse.UnchangedCallers, want) {
		t.Errorf("UnchangedCallers = %v, want %v", parse.UnchangedCallers, want)
	}
}

func TestAnnotateUnchangedCallers_UnchangedSameNamedSibling(t *testing.T) {
	// Circle.String changed; Square.String did not but calls describe. A
	// changed entity's name must not hide its unchanged namesake.
	src := `package shapes

func (c Circle) String() string {
	return "circle"
}

func (s Square) String() string {
	return describe(s.Side)
}

func describe(x float64) string {
	return "side"
}
`
	f := ucFile("shapes.go", "go", src,
		Hunk{OldStart: 4, OldLines: 1, NewStart: 4, NewLines: 1,
			Lines: []string{`-	return "c"`, `+	return "circle"`}},
		Hunk{OldStart: 12, OldLines: 1, NewStart: 12, NewLines: 1,
			Lines: []string{`-	return "s"`, `+	return "side"`}})

	files := []*FileDiff{f}
	snippets := BuildSnippets(files)
	AnnotateUnchangedCallers(files, snippets)

	describe := ucSnippet(t, snippets, "shapes.go", "describe")
	want := []string{"shapes.go::String"}
	if !reflect.DeepEqual(describe.UnchangedCallers, want) {
		t.Errorf("UnchangedCallers = %v, want %v", describe.UnchangedCallers, want)
	}
}
