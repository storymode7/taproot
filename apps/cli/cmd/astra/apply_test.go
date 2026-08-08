package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/astra-review/astra/apps/cli/internal/diff"
	"github.com/astra-review/astra/core"
)

func snippet(filePath, oldPath string, isNew bool, startLine int, diffLines ...string) *core.Snippet {
	return &core.Snippet{
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
	if patch := buildAcceptedPatch([]*core.Snippet{}); patch != "" {
		t.Errorf("expected empty patch for empty slice, got %q", patch)
	}
}

func TestBuildAcceptedPatch_SingleHunk(t *testing.T) {
	snippets := []*core.Snippet{
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
	snippets := []*core.Snippet{
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

func TestBuildAcceptedPatch_InterleavedFragmentsSorted(t *testing.T) {
	// A class-container snippet can hold multiple non-contiguous fragments
	// that interleave with its methods' snippets (one git hunk split at
	// entity boundaries). git apply needs hunks in ascending order, so
	// fragments must be sorted individually — not per snippet.
	class := snippet("store.py", "store.py", false, 1,
		"@@ -1,3 +1,3 @@", " class Store:", "-    version = 1", "+    version = 2", " ",
		"@@ -8,1 +9,1 @@", "-    limit = 10", "+    limit = 20")
	method := snippet("store.py", "store.py", false, 4,
		"@@ -4,3 +4,4 @@", "     def put(self, x):", "         self.items.append(x)", "+        self.log(x)", " ")

	patch := buildAcceptedPatch([]*core.Snippet{class, method})

	idx1 := strings.Index(patch, "@@ -1,3")
	idx4 := strings.Index(patch, "@@ -4,3")
	idx8 := strings.Index(patch, "@@ -8,1")
	if idx1 == -1 || idx4 == -1 || idx8 == -1 {
		t.Fatalf("expected all three fragments in patch:\n%s", patch)
	}
	if !(idx1 < idx4 && idx4 < idx8) {
		t.Errorf("fragments out of ascending order (git apply would fail):\n%s", patch)
	}
}

func TestBuildAcceptedPatch_GitApply_ClassAndMethodSplit(t *testing.T) {
	// End-to-end regression: one git hunk spanning class-level lines and a
	// method body splits into interleaved snippets. The assembled patch must
	// still be a valid git patch, and accepting only the method must not
	// stage the class-level deletion.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	oldSrc := `class Store:
    version = 1

    def put(self, x):
        self.items.append(x)

    limit = 10
`
	newSrc := `class Store:
    version = 2

    def put(self, x):
        self.items.append(x)
        self.log(x)

    limit = 20
`

	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "t@t")
	git("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "store.py"), []byte(oldSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "store.py")
	git("commit", "-q", "-m", "base")
	if err := os.WriteFile(filepath.Join(dir, "store.py"), []byte(newSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	adapter := &diff.GitDiffAdapter{RepoRoot: dir}
	files, err := adapter.Files()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		f.Source = []byte(newSrc)
	}
	snippets := core.BuildSnippets(files)

	// Accepting everything must produce a patch git can apply.
	patch := buildAcceptedPatch(snippets)
	if err := adapter.ApplyCached(patch); err != nil {
		t.Fatalf("full patch rejected by git apply: %v\npatch:\n%s", err, patch)
	}
	git("reset", "-q")

	// Accepting only Store.put must leave the class-level limit line alone.
	var put []*core.Snippet
	for _, s := range snippets {
		if s.SymbolPath == "Store.put" {
			put = append(put, s)
		}
	}
	if len(put) != 1 {
		t.Fatalf("expected a Store.put snippet, got %v", symbols(snippets))
	}
	if err := adapter.ApplyCached(buildAcceptedPatch(put)); err != nil {
		t.Fatalf("method-only patch rejected by git apply: %v", err)
	}
	cmd := exec.Command("git", "show", ":store.py")
	cmd.Dir = dir
	staged, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(staged), "limit = 10") {
		t.Errorf("accepting only Store.put lost the unaccepted limit line; staged:\n%s", staged)
	}
}

func symbols(snippets []*core.Snippet) []string {
	out := make([]string, len(snippets))
	for i, s := range snippets {
		out[i] = s.SymbolPath
	}
	return out
}

func TestBuildAcceptedPatch_MultiFile(t *testing.T) {
	snippets := []*core.Snippet{
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
	snippets := []*core.Snippet{
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
	snippets := []*core.Snippet{
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
	snippets := []*core.Snippet{
		{FilePath: "foo.go", OldPath: "", IsNew: false, StartLine: 1,
			DiffLines: []string{"@@ -1,1 +1,1 @@", "-a", "+b"}},
	}
	patch := buildAcceptedPatch(snippets)
	if !strings.Contains(patch, "--- a/foo.go") {
		t.Errorf("expected foo.go as fallback old path, got:\n%s", patch)
	}
}
