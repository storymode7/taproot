package diff

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestBaseMode_IgnoresChangesOnBase reproduces PR review after the base
// branch moved on: only the branch's own edits may appear, and post-images
// come from the head commit, not the (dirty) working tree.
func TestBaseMode_IgnoresChangesOnBase(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "m.py"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run("init", "-q", "-b", "main")
	write("def a():\n    return 1\n\ndef b():\n    return 2\n")
	run("add", ".")
	run("commit", "-qm", "init")
	run("checkout", "-qb", "feat")
	write("def a():\n    return 9\n\ndef b():\n    return 2\n")
	run("commit", "-qam", "feat")
	run("checkout", "-q", "main")
	write("def a():\n    return 1\n\ndef b():\n    return 3\n")
	run("commit", "-qam", "main moved")
	run("checkout", "-q", "feat")
	write("uncommitted\n")

	a := &GitDiffAdapter{RepoRoot: dir, Base: "main"}
	files, err := a.Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || len(files[0].Hunks) != 1 {
		t.Fatalf("want one file with one hunk, got %+v", files)
	}
	diff := strings.Join(files[0].Hunks[0].Lines, "\n")
	if !strings.Contains(diff, "+    return 9") || strings.Contains(diff, "return 3") {
		t.Errorf("diff must hold only the branch's change:\n%s", diff)
	}

	src, err := a.FileBlob(BlobRequest{Path: "m.py"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "return 9") {
		t.Errorf("post-image must come from HEAD, got %q", src)
	}
}
