package main

import (
	"strings"
	"testing"

	"github.com/astra-review/astra/apps/cli/internal/engine"
)

func snippet(filePath, oldPath string, isNew bool, startLine int, diffLines ...string) *engine.Snippet {
	return &engine.Snippet{
		FilePath:  filePath,
		OldPath:   oldPath,
		IsNew:     isNew,
		StartLine: startLine,
		DiffLines: diffLines,
	}
}

func TestBuildAcceptedPatch_Empty(t *testing.T) {
	if patch := buildAcceptedPatch(nil); patch != "" {
		t.Errorf("expected empty patch, got %q", patch)
	}
	if patch := buildAcceptedPatch([]*engine.Snippet{}); patch != "" {
		t.Errorf("expected empty patch for empty slice, got %q", patch)
	}
}

func TestBuildAcceptedPatch_SingleHunk(t *testing.T) {
	snippets := []*engine.Snippet{
		snippet("foo.go", "foo.go", false, 5,
			"@@ -5,3 +5,4 @@", " ctx", "+// added", " line2", " line3"),
	}
	patch := buildAcceptedPatch(snippets)
	if !strings.Contains(patch, "--- a/foo.go") {
		t.Errorf("missing old header in:\n%s", patch)
	}
	if !strings.Contains(patch, "+++ b/foo.go") {
		t.Errorf("missing new header in:\n%s", patch)
	}
	if !strings.Contains(patch, "@@ -5,3 +5,4 @@") {
		t.Errorf("missing hunk header in:\n%s", patch)
	}
	if !strings.Contains(patch, "+// added") {
		t.Errorf("missing added line in:\n%s", patch)
	}
}

func TestBuildAcceptedPatch_MultiHunkSameFile_SortedByStartLine(t *testing.T) {
	// Provide hunks in reverse order — result must sort them by start line.
	snippets := []*engine.Snippet{
		snippet("foo.go", "foo.go", false, 20, "@@ -20,2 +20,2 @@", "-old2", "+new2"),
		snippet("foo.go", "foo.go", false, 5, "@@ -5,2 +5,2 @@", "-old1", "+new1"),
	}
	patch := buildAcceptedPatch(snippets)

	idx5 := strings.Index(patch, "@@ -5,2")
	idx20 := strings.Index(patch, "@@ -20,2")
	if idx5 == -1 || idx20 == -1 {
		t.Fatalf("expected both hunks in patch:\n%s", patch)
	}
	if idx5 > idx20 {
		t.Errorf("hunk at line 5 must appear before hunk at line 20;\npatch:\n%s", patch)
	}

	// File header must appear exactly once.
	if count := strings.Count(patch, "--- a/foo.go"); count != 1 {
		t.Errorf("expected 1 file header, got %d:\n%s", count, patch)
	}
}

func TestBuildAcceptedPatch_MultiFile(t *testing.T) {
	snippets := []*engine.Snippet{
		snippet("a.go", "a.go", false, 1, "@@ -1,1 +1,1 @@", "-x", "+y"),
		snippet("b.go", "b.go", false, 1, "@@ -1,1 +1,1 @@", "-p", "+q"),
	}
	patch := buildAcceptedPatch(snippets)
	if !strings.Contains(patch, "--- a/a.go") {
		t.Errorf("missing a.go header:\n%s", patch)
	}
	if !strings.Contains(patch, "--- a/b.go") {
		t.Errorf("missing b.go header:\n%s", patch)
	}
	// Each file header appears exactly once.
	for _, hdr := range []string{"--- a/a.go", "--- a/b.go", "+++ b/a.go", "+++ b/b.go"} {
		if strings.Count(patch, hdr) != 1 {
			t.Errorf("expected exactly 1 %q in patch:\n%s", hdr, patch)
		}
	}
}

func TestBuildAcceptedPatch_Rename(t *testing.T) {
	snippets := []*engine.Snippet{
		snippet("new_name.go", "old_name.go", false, 1, "@@ -1,1 +1,1 @@", "-old", "+new"),
	}
	patch := buildAcceptedPatch(snippets)
	if !strings.Contains(patch, "--- a/old_name.go") {
		t.Errorf("expected old path in old header, got:\n%s", patch)
	}
	if !strings.Contains(patch, "+++ b/new_name.go") {
		t.Errorf("expected new path in new header, got:\n%s", patch)
	}
}

func TestBuildAcceptedPatch_NewFile_UsesDevNull(t *testing.T) {
	// New files: IsNew=true, OldPath matches FilePath (from git diff header).
	snippets := []*engine.Snippet{
		snippet("brand_new.go", "brand_new.go", true, 1,
			"@@ -0,0 +1,3 @@", "+package main", "+", "+func init() {}"),
	}
	patch := buildAcceptedPatch(snippets)
	if !strings.Contains(patch, "--- /dev/null") {
		t.Errorf("expected /dev/null for new file, got:\n%s", patch)
	}
	if !strings.Contains(patch, "+++ b/brand_new.go") {
		t.Errorf("expected new file path in new header, got:\n%s", patch)
	}
}

func TestBuildAcceptedPatch_OldPathEmpty_FallsBackToFilePath(t *testing.T) {
	// If OldPath is empty for some reason, FilePath is used.
	snippets := []*engine.Snippet{
		{FilePath: "foo.go", OldPath: "", IsNew: false, StartLine: 1,
			DiffLines: []string{"@@ -1,1 +1,1 @@", "-a", "+b"}},
	}
	patch := buildAcceptedPatch(snippets)
	if !strings.Contains(patch, "--- a/foo.go") {
		t.Errorf("expected foo.go as fallback old path, got:\n%s", patch)
	}
}
