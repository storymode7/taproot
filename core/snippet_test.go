package core

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestBuildSnippets_Nil(t *testing.T) {
	snippets := BuildSnippets(nil)
	if len(snippets) != 0 {
		t.Fatalf("expected 0 snippets, got %d", len(snippets))
	}
}

func TestBuildSnippets_DeletedFileIsOneSnippet(t *testing.T) {
	// A deleted file is reviewed like any change: one snippet holding its
	// hunks, numbered from the old side since it has no new one.
	lines := []string{"@@ -1,3 +0,0 @@", "-package auth", "-", "-func Check() bool { return true }"}
	files := []*FileDiff{
		{Path: "auth/check.go", OldPath: "auth/check.go", Language: "go", IsDeleted: true, OldMode: "100644",
			Hunks: []Hunk{{OldStart: 1, OldLines: 3, NewStart: 0, NewLines: 0, Lines: lines}}},
		{Path: "logo.png", OldPath: "logo.png", IsBinary: true},
		{Path: "empty.go", OldPath: "empty.go", Hunks: []Hunk{}},
	}
	snippets := BuildSnippets(files)
	if len(snippets) != 1 {
		t.Fatalf("expected the deleted file's snippet only, got %v", symbolPaths(snippets))
	}
	s := snippets[0]
	if s.SymbolPath != "(deleted)" || s.NodeType != "file" || !s.IsDeleted || s.StartLine != 1 || s.EndLine != 3 {
		t.Errorf("deleted snippet = %q %q deleted=%v lines %d-%d", s.SymbolPath, s.NodeType, s.IsDeleted, s.StartLine, s.EndLine)
	}
	if s.ID != snippetID("auth/check.go", "(deleted)", 0) {
		t.Errorf("ID = %s", s.ID)
	}
	if strings.Join(s.DiffLines, "\n") != strings.Join(lines, "\n") {
		t.Errorf("DiffLines = %q", s.DiffLines)
	}
}

func TestSkippedFiles(t *testing.T) {
	files := []FileDiff{
		{Path: "src/a.go", OldPath: "src/a.go", Hunks: []Hunk{{Lines: []string{"@@ -1 +1 @@", "-a", "+b"}}}},
		{Path: "z/logo.png", OldPath: "z/logo.png", IsBinary: true, IsDeleted: true},
		{Path: "new_name.py", OldPath: "old_name.py"},
		{Path: "run.sh", OldPath: "run.sh", OldMode: "100644", NewMode: "100755"},
		{Path: "empty.py", OldPath: "empty.py", IsNew: true, NewMode: "100644"},
		{Path: "gone.txt", OldPath: "gone.txt", IsDeleted: true, OldMode: "100644"},
		{Path: "m.py", OldPath: "m.py", IsConflict: true},
		{Path: "vendor/big.js", OldPath: "vendor/big.js"},
	}
	want := []SkippedFile{
		{Path: "empty.py", Reason: "empty"},
		{Path: "gone.txt", Reason: "empty"},
		{Path: "m.py", Reason: "conflict"},
		{Path: "new_name.py", OldPath: "old_name.py", Reason: "renamed"},
		{Path: "run.sh", Reason: "mode"},
		{Path: "vendor/big.js", Reason: "no-diff"},
		{Path: "z/logo.png", Reason: "binary"},
	}
	if got := SkippedFiles(files); !reflect.DeepEqual(got, want) {
		t.Errorf("SkippedFiles =\n%+v\nwant\n%+v", got, want)
	}
	ptrs := make([]*FileDiff, len(files))
	for i := range files {
		ptrs[i] = &files[i]
	}
	if got := symbolPaths(BuildSnippets(ptrs)); len(got) != 1 {
		t.Errorf("a skipped file made snippets: %v", got)
	}
}

func TestProcess_SkippedAndDeletedOnTheWire(t *testing.T) {
	out, err := ProcessJSON(`{"files": [
		{"path": "auth.go", "oldPath": "auth.go", "language": "go", "isDeleted": true,
		 "hunks": [{"oldStart": 1, "oldLines": 1, "newStart": 0, "newLines": 0, "lines": ["@@ -1,1 +0,0 @@", "-package auth"]}]},
		{"path": "run.sh", "oldPath": "run.sh", "oldMode": "100644", "newMode": "100755"},
		{"path": "m.py", "oldPath": "m.py", "isConflict": true}
	]}`)
	if err != nil {
		t.Fatal(err)
	}
	var resp ProcessResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Snippets) != 1 || !resp.Snippets[0].IsDeleted || resp.Snippets[0].SymbolPath != "(deleted)" {
		t.Errorf("snippets = %+v", resp.Snippets)
	}
	want := []SkippedFile{{Path: "m.py", Reason: "conflict"}, {Path: "run.sh", Reason: "mode"}}
	if !reflect.DeepEqual(resp.Skipped, want) {
		t.Errorf("skipped = %+v, want %+v", resp.Skipped, want)
	}
	if !strings.Contains(out, `"isDeleted":true`) || strings.Count(out, "isDeleted") != 1 {
		t.Errorf("isDeleted must be on the deleted snippet only: %s", out)
	}
}

func TestBuildSnippets_OneSnippetPerHunk(t *testing.T) {
	files := []*FileDiff{
		{
			Path:     "foo.go",
			OldPath:  "foo.go",
			Language: "go",
			Hunks: []Hunk{
				{OldStart: 1, OldLines: 3, NewStart: 1, NewLines: 4, Lines: []string{"@@ -1,3 +1,4 @@", "+// new", " existing"}},
				{OldStart: 10, OldLines: 2, NewStart: 11, NewLines: 2, Lines: []string{"@@ -10,2 +11,2 @@", "-old", "+new"}},
			},
		},
	}
	snippets := BuildSnippets(files)
	if len(snippets) != 2 {
		t.Fatalf("expected 2 snippets (one per hunk), got %d", len(snippets))
	}
	for _, s := range snippets {
		if s.FilePath != "foo.go" {
			t.Errorf("FilePath = %q, want foo.go", s.FilePath)
		}
		if s.OldPath != "foo.go" {
			t.Errorf("OldPath = %q, want foo.go", s.OldPath)
		}
		if s.Language != "go" {
			t.Errorf("Language = %q, want go", s.Language)
		}
		if s.ID == "" {
			t.Error("ID must not be empty")
		}
		if s.NodeType != "hunk" {
			t.Errorf("NodeType = %q, want hunk", s.NodeType)
		}
	}
	// First snippet: StartLine == NewStart of first hunk
	if snippets[0].StartLine != 1 {
		t.Errorf("snippets[0].StartLine = %d, want 1", snippets[0].StartLine)
	}
	if snippets[1].StartLine != 11 {
		t.Errorf("snippets[1].StartLine = %d, want 11", snippets[1].StartLine)
	}
}

func TestBuildSnippets_MultiFile(t *testing.T) {
	files := []*FileDiff{
		{Path: "a.go", OldPath: "a.go", Hunks: []Hunk{{Lines: []string{"@@ -1,1 +1,1 @@", "-x", "+y"}}}},
		{Path: "b.go", OldPath: "b.go", Hunks: []Hunk{{Lines: []string{"@@ -1,1 +1,1 @@", "-p", "+q"}}}},
	}
	snippets := BuildSnippets(files)
	if len(snippets) != 2 {
		t.Fatalf("expected 2 snippets, got %d", len(snippets))
	}
	if snippets[0].FilePath != "a.go" {
		t.Errorf("snippets[0].FilePath = %q, want a.go", snippets[0].FilePath)
	}
	if snippets[1].FilePath != "b.go" {
		t.Errorf("snippets[1].FilePath = %q, want b.go", snippets[1].FilePath)
	}
}

func TestBuildSnippets_OldPath_Rename(t *testing.T) {
	files := []*FileDiff{
		{
			Path:    "new_name.go",
			OldPath: "old_name.go",
			Hunks:   []Hunk{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1, Lines: []string{"@@ -1,1 +1,1 @@", "-old", "+new"}}},
		},
	}
	snippets := BuildSnippets(files)
	if len(snippets) != 1 {
		t.Fatalf("expected 1 snippet, got %d", len(snippets))
	}
	if snippets[0].OldPath != "old_name.go" {
		t.Errorf("OldPath = %q, want old_name.go", snippets[0].OldPath)
	}
	if snippets[0].FilePath != "new_name.go" {
		t.Errorf("FilePath = %q, want new_name.go", snippets[0].FilePath)
	}
}

func TestBuildSnippets_IsNew(t *testing.T) {
	files := []*FileDiff{
		{
			Path:    "brand_new.go",
			OldPath: "brand_new.go",
			IsNew:   true,
			Hunks:   []Hunk{{OldStart: 0, OldLines: 0, NewStart: 1, NewLines: 2, Lines: []string{"@@ -0,0 +1,2 @@", "+package main", "+func init() {}"}}},
		},
	}
	snippets := BuildSnippets(files)
	if len(snippets) != 1 {
		t.Fatalf("expected 1 snippet, got %d", len(snippets))
	}
	if !snippets[0].IsNew {
		t.Error("expected IsNew = true on snippet from new file")
	}
}

func TestSnippetID_Stable(t *testing.T) {
	id1 := entitySnippetID("foo.go", "function", "MyFunc", 1)
	id2 := entitySnippetID("foo.go", "function", "MyFunc", 1)
	if id1 != id2 {
		t.Errorf("entitySnippetID not stable: %q != %q", id1, id2)
	}
}

func TestSnippetID_DifferentInputs(t *testing.T) {
	base := entitySnippetID("foo.go", "function", "MyFunc", 1)
	cases := []struct {
		name string
		id   string
	}{
		{"different file", entitySnippetID("bar.go", "function", "MyFunc", 1)},
		{"different kind", entitySnippetID("foo.go", "type", "MyFunc", 1)},
		{"different symbol", entitySnippetID("foo.go", "function", "OtherFunc", 1)},
		{"second same-named entity", entitySnippetID("foo.go", "function", "MyFunc", 2)},
		{"nameless", snippetID("foo.go", "", 10)},
	}
	for _, tc := range cases {
		if tc.id == base {
			t.Errorf("%s: expected different ID but got same as base %q", tc.name, base)
		}
	}
}

func TestSnippetID_NamedEntitySurvivesLineShift(t *testing.T) {
	// Review state must survive lines shifting above a named entity.
	id := func(src string, line int) string {
		files := []*FileDiff{{
			Path: "foo.go", OldPath: "foo.go", Language: "go", Source: []byte(src),
			Hunks: []Hunk{{OldStart: line, OldLines: 1, NewStart: line, NewLines: 1,
				Lines: []string{"-\tx()", "+\ty()"}}},
		}}
		snippets := BuildSnippets(files)
		if len(snippets) != 1 || snippets[0].SymbolPath != "MyFunc" {
			t.Fatalf("expected one MyFunc snippet, got %v", symbolPaths(snippets))
		}
		return snippets[0].ID
	}
	a := id("package p\n\nfunc MyFunc() {\n\ty()\n}\n", 4)
	b := id("package p\n\nvar v = 1\n\nfunc MyFunc() {\n\ty()\n}\n", 6)
	if a != b {
		t.Error("named entity ID changed when start line shifted")
	}
}

func TestSnippetID_NamelessKeyedByLine(t *testing.T) {
	// Preamble/fallback-hunk snippets have no symbol; line keeps them distinct.
	if snippetID("foo.go", "", 10) == snippetID("foo.go", "", 42) {
		t.Error("nameless snippets at different lines must get different IDs")
	}
}
