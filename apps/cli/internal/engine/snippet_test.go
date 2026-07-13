package engine

import (
	"testing"

	"github.com/astra-review/astra/apps/cli/internal/diff"
)

func TestBuildSnippets_Nil(t *testing.T) {
	snippets := BuildSnippets(nil, nil)
	if len(snippets) != 0 {
		t.Fatalf("expected 0 snippets, got %d", len(snippets))
	}
}

func TestBuildSnippets_SkipsDeletedBinaryEmpty(t *testing.T) {
	files := []*diff.FileDiff{
		{Path: "deleted.go", OldPath: "deleted.go", IsDeleted: true, Hunks: []diff.Hunk{{Lines: []string{"@@ -1,1 +0,0 @@", "-line"}}}},
		{Path: "binary.png", OldPath: "binary.png", IsBinary: true},
		{Path: "empty.go", OldPath: "empty.go", Hunks: []diff.Hunk{}},
	}
	snippets := BuildSnippets(files, nil)
	if len(snippets) != 0 {
		t.Errorf("expected 0 snippets, got %d", len(snippets))
	}
}

func TestBuildSnippets_OneSnippetPerHunk(t *testing.T) {
	files := []*diff.FileDiff{
		{
			Path:     "foo.go",
			OldPath:  "foo.go",
			Language: "go",
			Hunks: []diff.Hunk{
				{OldStart: 1, OldLines: 3, NewStart: 1, NewLines: 4, Lines: []string{"@@ -1,3 +1,4 @@", "+// new", " existing"}},
				{OldStart: 10, OldLines: 2, NewStart: 11, NewLines: 2, Lines: []string{"@@ -10,2 +11,2 @@", "-old", "+new"}},
			},
		},
	}
	snippets := BuildSnippets(files, nil)
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
	files := []*diff.FileDiff{
		{Path: "a.go", OldPath: "a.go", Hunks: []diff.Hunk{{Lines: []string{"@@ -1,1 +1,1 @@", "-x", "+y"}}}},
		{Path: "b.go", OldPath: "b.go", Hunks: []diff.Hunk{{Lines: []string{"@@ -1,1 +1,1 @@", "-p", "+q"}}}},
	}
	snippets := BuildSnippets(files, nil)
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
	files := []*diff.FileDiff{
		{
			Path:    "new_name.go",
			OldPath: "old_name.go",
			Hunks:   []diff.Hunk{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1, Lines: []string{"@@ -1,1 +1,1 @@", "-old", "+new"}}},
		},
	}
	snippets := BuildSnippets(files, nil)
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
	files := []*diff.FileDiff{
		{
			Path:    "brand_new.go",
			OldPath: "brand_new.go",
			IsNew:   true,
			Hunks:   []diff.Hunk{{OldStart: 0, OldLines: 0, NewStart: 1, NewLines: 2, Lines: []string{"@@ -0,0 +1,2 @@", "+package main", "+func init() {}"}}},
		},
	}
	snippets := BuildSnippets(files, nil)
	if len(snippets) != 1 {
		t.Fatalf("expected 1 snippet, got %d", len(snippets))
	}
	if !snippets[0].IsNew {
		t.Error("expected IsNew = true on snippet from new file")
	}
}

func TestSnippetID_Stable(t *testing.T) {
	id1 := snippetID("foo.go", "MyFunc", 10)
	id2 := snippetID("foo.go", "MyFunc", 10)
	if id1 != id2 {
		t.Errorf("snippetID not stable: %q != %q", id1, id2)
	}
}

func TestSnippetID_DifferentInputs(t *testing.T) {
	base := snippetID("foo.go", "MyFunc", 10)
	cases := []struct {
		name string
		id   string
	}{
		{"different file", snippetID("bar.go", "MyFunc", 10)},
		{"different symbol", snippetID("foo.go", "OtherFunc", 10)},
	}
	for _, tc := range cases {
		if tc.id == base {
			t.Errorf("%s: expected different ID but got same as base %q", tc.name, base)
		}
	}
}

func TestSnippetID_NamedEntitySurvivesLineShift(t *testing.T) {
	// Review state must survive lines shifting above a named entity.
	if snippetID("foo.go", "MyFunc", 10) != snippetID("foo.go", "MyFunc", 42) {
		t.Error("named entity ID changed when start line shifted")
	}
}

func TestSnippetID_NamelessKeyedByLine(t *testing.T) {
	// Preamble/fallback-hunk snippets have no symbol; line keeps them distinct.
	if snippetID("foo.go", "", 10) == snippetID("foo.go", "", 42) {
		t.Error("nameless snippets at different lines must get different IDs")
	}
}
