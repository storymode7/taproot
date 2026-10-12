//go:build unix

package diff

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestFileBlob_DoesNotFollowLinks: git's post-image of a symlink is the
// path it holds, and reading what it points to could hang on a pipe or
// exhaust memory on /dev/zero.
func TestFileBlob_DoesNotFollowLinks(t *testing.T) {
	r := newGitRepo(t, "")
	r.write("a.py", "def a():\n    return 1\n")
	r.git("add", "-A")
	r.git("commit", "-qm", "base")
	if err := os.Symlink("a.py", filepath.Join(r.dir, "link.py")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(r.dir, "pipe.py"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, a := range []*GitDiffAdapter{{RepoRoot: r.dir}, {RepoRoot: r.dir, Ref: "HEAD"}} {
		if got, err := a.FileBlob(BlobRequest{Path: "link.py"}); err != nil || string(got) != "a.py" {
			t.Errorf("Ref %q: FileBlob(link.py) = %q, %v; want the link's target, a.py", a.Ref, got, err)
		}
		done := make(chan error, 1)
		go func() {
			_, err := a.FileBlob(BlobRequest{Path: "pipe.py"})
			done <- err
		}()
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("Ref %q: FileBlob(pipe.py) read a pipe", a.Ref)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("Ref %q: FileBlob(pipe.py) blocked on a pipe", a.Ref)
		}
	}
}
