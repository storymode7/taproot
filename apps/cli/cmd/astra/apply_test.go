package main

import (
	"strings"
	"testing"

	"github.com/storymode7/astra/core"
)

// apply puts the fragments of snippets, given by their diff lines, into base.
func apply(t *testing.T, base string, snippets ...[]string) (string, error) {
	t.Helper()
	var all []fragment
	for _, lines := range snippets {
		frags, err := fragments(&core.Snippet{FilePath: "f", DiffLines: lines})
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, frags...)
	}
	out, err := applyFragments([]byte(base), all)
	return string(out), err
}

func TestApplyFragments_ExactPlaces(t *testing.T) {
	base := "a\nb\nc\nd\n"
	for _, c := range []struct {
		name     string
		snippets [][]string
		want     string
	}{
		{"nothing", nil, base},
		{"a change", [][]string{{"@@ -2,1 +2,1 @@", "-b", "+B"}}, "a\nB\nc\nd\n"},
		// An insertion names the line it goes after, 0 for the top.
		{"insert at the top", [][]string{{"@@ -0,0 +1,1 @@", "+top"}}, "top\na\nb\nc\nd\n"},
		{"insert after line 2", [][]string{{"@@ -2,0 +3,1 @@", "+new"}}, "a\nb\nnew\nc\nd\n"},
		{"insert at the end", [][]string{{"@@ -4,0 +5,1 @@", "+end"}}, base + "end\n"},
		// A pure insertion at a line goes before a change starting there,
		// in either order of the snippets.
		{"insert before a change", [][]string{{"@@ -3,1 +4,1 @@", "-c", "+C"}, {"@@ -2,0 +3,1 @@", "+new"}}, "a\nb\nnew\nC\nd\n"},
		{"insert before a deletion", [][]string{{"@@ -2,1 +2,0 @@", "-b"}, {"@@ -1,0 +2,1 @@", "+new"}}, "a\nnew\nc\nd\n"},
		{"two insertions at one line", [][]string{{"@@ -2,0 +4,1 @@", "+second"}, {"@@ -2,0 +3,1 @@", "+first"}}, "a\nb\nfirst\nsecond\nc\nd\n"},
		{"one snippet, two fragments", [][]string{{"@@ -1,1 +1,1 @@", "-a", "+A", "@@ -4,1 +4,1 @@", "-d", "+D"}}, "A\nb\nc\nD\n"},
		{"a deletion", [][]string{{"@@ -2,2 +1,0 @@", "-b", "-c"}}, "a\nd\n"},
		{"context is kept", [][]string{{"@@ -1,3 +1,3 @@", " a", "-b", "+B", " c"}}, "a\nB\nc\nd\n"},
	} {
		got, err := apply(t, base, c.snippets...)
		if err != nil || got != c.want {
			t.Errorf("%s: %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

func TestApplyFragments_LineEndings(t *testing.T) {
	// "\ No newline at end of file" marks the line before it; a CRLF
	// line keeps its "\r".
	got, err := apply(t, "a\nb", []string{"@@ -2,1 +2,2 @@", "-b", `\ No newline at end of file`, "+b", "+c"})
	if err != nil || got != "a\nb\nc\n" {
		t.Errorf("gaining a newline: %q, %v", got, err)
	}
	got, err = apply(t, "a\nb\n", []string{"@@ -2,1 +2,1 @@", "-b", "+B", `\ No newline at end of file`})
	if err != nil || got != "a\nB" {
		t.Errorf("losing a newline: %q, %v", got, err)
	}
	got, err = apply(t, "a\r\nb\r\n", []string{"@@ -2,1 +2,1 @@", "-b\r", "+B\r"})
	if err != nil || got != "a\r\nB\r\n" {
		t.Errorf("CRLF: %q, %v", got, err)
	}
}

func TestApplyFragments_RefusesWhatDoesNotMatch(t *testing.T) {
	for name, snippets := range map[string][][]string{
		"a removed line that is not there":      {{"@@ -2,1 +2,1 @@", "-x", "+B"}},
		"context that is not there":             {{"@@ -1,2 +1,3 @@", " a", "+new", " x"}},
		"a CR the base does not have":           {{"@@ -2,1 +2,1 @@", "-b\r", "+B"}},
		"two changes to one line":               {{"@@ -2,1 +2,1 @@", "-b", "+B"}, {"@@ -2,1 +2,1 @@", "-b", "+X"}},
		"past the end of the file":              {{"@@ -5,1 +5,1 @@", "-e", "+E"}},
		"a newline the last line lacks":         {{"@@ -4,1 +4,1 @@", "-d", `\ No newline at end of file`, "+D"}},
		"identical lines elsewhere in the file": {{"@@ -3,1 +3,1 @@", "-b", "+B"}},
	} {
		if got, err := apply(t, "a\nb\nc\nd\n", snippets...); err == nil {
			t.Errorf("%s: applied as %q, want an error", name, got)
		}
	}
	if _, err := fragments(&core.Snippet{FilePath: "f", DiffLines: []string{"@@ -1,2 +1,1 @@", "-a"}}); err == nil {
		t.Error("a header whose counts do not match its lines must be an error")
	}
}

// TestStage_ClassAndMethodSplit: one git hunk spanning class-level lines
// and a method body splits into interleaved snippets. Accepting only the
// method must not stage the class-level change.
func TestStage_ClassAndMethodSplit(t *testing.T) {
	oldSrc := "class Store:\n    version = 1\n\n    def put(self, x):\n        self.items.append(x)\n\n    limit = 10\n"
	newSrc := "class Store:\n    version = 2\n\n    def put(self, x):\n        self.items.append(x)\n        self.log(x)\n\n    limit = 20\n"
	r := newRepo(t, "")
	r.write("store.py", oldSrc)
	r.commit("base")
	r.write("store.py", newSrc)

	d := r.load(t, true)
	var put *core.Snippet
	for _, s := range d.snippets {
		if s.SymbolPath == "Store.put" {
			put = s
		}
	}
	if put == nil {
		t.Fatalf("expected a Store.put snippet, got %v", symbols(d.snippets))
	}
	if err := d.stager.stage(put, true); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(oldSrc, "append(x)\n", "append(x)\n        self.log(x)\n", 1)
	if got := r.git("show", ":store.py"); got != want {
		t.Errorf("accepting only Store.put staged\n%q\nwant\n%q", got, want)
	}
	stageEach(t, d.stager, d.snippets, orders(len(d.snippets))[1])
	if got := r.git("show", ":store.py"); got != newSrc {
		t.Errorf("staging every snippet != full change; staged:\n%s", got)
	}
	if err := d.stager.stage(put, false); err != nil {
		t.Fatalf("unstaging Store.put: %v", err)
	}
	if got := r.git("show", ":store.py"); strings.Contains(got, "self.log(x)") || !strings.Contains(got, "version = 2") || !strings.Contains(got, "limit = 20") {
		t.Errorf("unstaging Store.put should remove only its change; staged:\n%s", got)
	}
	if err := d.stager.stage(put, false); err != nil {
		t.Fatalf("unstaging a snippet that isn't staged must be a no-op, got: %v", err)
	}
}

// TestStage_SplitHunkWithoutTrailingContext: a sub-hunk cut at an entity
// boundary ends on its last changed line, with no trailing context.
func TestStage_SplitHunkWithoutTrailingContext(t *testing.T) {
	oldSrc := "\"\"\"doc\"\"\"\n\nimport os\nimport sys\n\nfrom app.client import Client\n\n\ndef f():\n    pass\n"
	newSrc := "\"\"\"doc\"\"\"\n\nimport os\nimport sys\nfrom app.errors import RequestError\n\nfrom app.client import Client\n\n\ndef f():\n    pass\n"
	r := newRepo(t, "")
	r.write("p.py", oldSrc)
	r.commit("base")
	r.write("p.py", newSrc)

	d := r.load(t, true)
	if len(d.snippets) != 1 || d.snippets[0].NodeType != "imports" {
		t.Fatalf("expected one imports snippet, got %v", symbols(d.snippets))
	}
	if err := d.stager.stage(d.snippets[0], true); err != nil {
		t.Fatal(err)
	}
	if got := r.git("show", ":p.py"); got != newSrc {
		t.Errorf("staged content != new source; staged:\n%s", got)
	}
	if err := d.stager.stage(d.snippets[0], false); err != nil {
		t.Fatal(err)
	}
	if got := r.git("show", ":p.py"); got != oldSrc {
		t.Errorf("unstaging the imports left\n%s", got)
	}
}

func symbols(snippets []*core.Snippet) []string {
	out := make([]string, len(snippets))
	for i, s := range snippets {
		out[i] = s.SymbolPath
	}
	return out
}
