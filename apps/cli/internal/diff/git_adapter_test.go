package diff

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseDiff_Empty(t *testing.T) {
	files := parseDiff("")
	if len(files) != 0 {
		t.Fatalf("expected 0 files, got %d", len(files))
	}
}

func TestParseDiff_SingleFileHunk(t *testing.T) {
	raw := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,4 @@
 package main

+import "fmt"
 func main() {}`

	files := parseDiff(raw)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	f := files[0]
	if f.Path != "foo.go" {
		t.Errorf("path = %q, want foo.go", f.Path)
	}
	if f.OldPath != "foo.go" {
		t.Errorf("OldPath = %q, want foo.go", f.OldPath)
	}
	if len(f.Hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(f.Hunks))
	}
	h := f.Hunks[0]
	if h.OldStart != 1 || h.OldLines != 3 {
		t.Errorf("old range = %d,%d, want 1,3", h.OldStart, h.OldLines)
	}
	if h.NewStart != 1 || h.NewLines != 4 {
		t.Errorf("new range = %d,%d, want 1,4", h.NewStart, h.NewLines)
	}
}

func TestParseDiff_MultiHunk(t *testing.T) {
	raw := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,4 @@
 package main
+// comment
 
 func a() {}
@@ -10,3 +11,3 @@
 func b() {
-	old()
+	newCall()
 }`

	files := parseDiff(raw)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if len(files[0].Hunks) != 2 {
		t.Fatalf("expected 2 hunks, got %d", len(files[0].Hunks))
	}
	if files[0].Hunks[1].OldStart != 10 {
		t.Errorf("second hunk OldStart = %d, want 10", files[0].Hunks[1].OldStart)
	}
}

func TestParseDiff_MultiFile(t *testing.T) {
	raw := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,2 +1,3 @@
 package main
+// comment
 func foo() {}
diff --git a/bar.go b/bar.go
--- a/bar.go
+++ b/bar.go
@@ -5,3 +5,4 @@
 func bar() {
-	return
+	return nil
 }`

	files := parseDiff(raw)
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if files[0].Path != "foo.go" {
		t.Errorf("files[0].Path = %q, want foo.go", files[0].Path)
	}
	if files[1].Path != "bar.go" {
		t.Errorf("files[1].Path = %q, want bar.go", files[1].Path)
	}
}

func TestParseDiff_NewFile(t *testing.T) {
	raw := `diff --git a/new.go b/new.go
new file mode 100644
--- /dev/null
+++ b/new.go
@@ -0,0 +1,3 @@
+package main
+
+func init() {}`

	files := parseDiff(raw)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if !files[0].IsNew {
		t.Error("expected IsNew = true")
	}
	if files[0].Path != "new.go" {
		t.Errorf("path = %q, want new.go", files[0].Path)
	}
}

func TestParseDiff_DeletedFile(t *testing.T) {
	raw := `diff --git a/old.go b/old.go
deleted file mode 100644
--- a/old.go
+++ /dev/null
@@ -1,2 +0,0 @@
-package main
-func old() {}`

	files := parseDiff(raw)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if !files[0].IsDeleted {
		t.Error("expected IsDeleted = true")
	}
}

func TestParseDiff_BinaryFile(t *testing.T) {
	raw := `diff --git a/img.png b/img.png
Binary files a/img.png and b/img.png differ`

	files := parseDiff(raw)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if !files[0].IsBinary {
		t.Error("expected IsBinary = true")
	}
}

func TestParseDiff_Rename(t *testing.T) {
	raw := `diff --git a/old_name.go b/new_name.go
--- a/old_name.go
+++ b/new_name.go
@@ -1,2 +1,2 @@
 package main
-func oldFunc() {}
+func newFunc() {}`

	files := parseDiff(raw)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	f := files[0]
	if f.Path != "new_name.go" {
		t.Errorf("Path = %q, want new_name.go", f.Path)
	}
	if f.OldPath != "old_name.go" {
		t.Errorf("OldPath = %q, want old_name.go", f.OldPath)
	}
}

func TestParseDiff_HunkLineCount_Default(t *testing.T) {
	// @@ -1 +1 @@ (no count suffix → default 1)
	raw := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1 +1 @@
-old
+new`

	files := parseDiff(raw)
	if len(files) != 1 || len(files[0].Hunks) != 1 {
		t.Fatal("expected 1 file with 1 hunk")
	}
	h := files[0].Hunks[0]
	if h.OldLines != 1 || h.NewLines != 1 {
		t.Errorf("expected OldLines=1, NewLines=1, got %d, %d", h.OldLines, h.NewLines)
	}
}

func TestParseDiff_LongLineAndCRLF(t *testing.T) {
	long := "+var a=" + strings.Repeat("1+", 40000) + "1;"
	raw := "diff --git a/a.min.js b/a.min.js\n--- a/a.min.js\n+++ b/a.min.js\n@@ -1 +1 @@\n-x\n" + long + "\n" +
		"diff --git a/w.py b/w.py\n--- a/w.py\n+++ b/w.py\n@@ -1,2 +1,2 @@\n def f():\r\n-    return 1\r\n+    return 2\r\n"

	files := parseDiff(raw)
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2: a line over 64 KB must not end the diff", len(files))
	}
	if got := files[0].Hunks[0].Lines; len(got) != 3 || got[2] != long {
		t.Errorf("a.min.js lost its long line: %d lines", len(got))
	}
	want := []string{"@@ -1,2 +1,2 @@", " def f():\r", "-    return 1\r", "+    return 2\r"}
	if got := files[1].Hunks[0].Lines; !reflect.DeepEqual(got, want) {
		t.Errorf("CRLF lines = %q, want %q (git apply needs the \\r)", got, want)
	}
}
