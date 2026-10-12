package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runIn runs astra in dir with args and returns what it printed to stdout
// and stderr.
func runIn(t *testing.T, dir string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	t.Chdir(dir)
	capture := func(f **os.File) func() string {
		rd, w, perr := os.Pipe()
		if perr != nil {
			t.Fatal(perr)
		}
		out := make(chan string)
		go func() {
			b, _ := io.ReadAll(rd)
			rd.Close()
			out <- string(b)
		}()
		old := *f
		*f = w
		return func() string {
			*f = old
			w.Close()
			return <-out
		}
	}
	endOut, endErr := capture(&os.Stdout), capture(&os.Stderr)
	err = run(args)
	return endOut(), endErr(), err
}

func TestRun_NamesFilesItCannotShow(t *testing.T) {
	r := newRepo(t, "")
	r.write("a.py", "def a():\n    return 1\n")
	r.write("auth.py", "def legacy_auth():\n    return True\n")
	r.write("run.sh", "run\n")
	r.write("old_name.py", "def old():\n    return 1\n")
	r.write("logo.png", "\x89PNG\x00\x01")
	r.commit("base")
	r.git("checkout", "-qb", "feature")
	r.write("a.py", "def a():\n    return 2\n")
	r.git("rm", "-q", "auth.py")
	if err := os.Chmod(filepath.Join(r.dir, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	r.git("mv", "old_name.py", "new_name.py")
	r.write("logo.png", "\x89PNG\x00\x02")
	r.write("empty.py", "")
	r.commit("feature")

	out, _, err := runIn(t, r.dir, "diff", "--base", "main", "--no-tty")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out, "\n")
	want := "Not shown: empty empty.py · binary logo.png · renamed old_name.py → new_name.py · mode run.sh"
	if lines[0] != want {
		t.Errorf("first line = %q, want %q", lines[0], want)
	}
	for _, s := range []string{"a.py:2 :: a", "auth.py:1 :: (deleted)"} {
		if !strings.Contains(out, s) {
			t.Errorf("output lacks the snippet %q:\n%s", s, out)
		}
	}

	// A diff of only such files names them instead of showing nothing.
	r.git("checkout", "-q", "main")
	r.write("logo.png", "\x89PNG\x00\x03")
	out, _, err = runIn(t, r.dir, "diff", "--no-tty")
	if err != nil || out != "Not shown: binary logo.png\n" {
		t.Errorf("binary-only diff printed %q, %v", out, err)
	}
}

func TestRun_NamesUnmergedFiles(t *testing.T) {
	r := newRepo(t, "")
	r.write("m.py", "def m():\n    return 1\n")
	r.write("o.py", "def o():\n    return 1\n")
	r.commit("base")
	r.git("checkout", "-qb", "other")
	r.write("m.py", "def m():\n    return 2\n")
	r.commit("other")
	r.git("checkout", "-q", "main")
	r.write("m.py", "def m():\n    return 3\n")
	r.commit("main")
	cmd := exec.Command("git", "merge", "other")
	cmd.Dir = r.dir
	if cmd.Run() == nil {
		t.Fatal("the merge did not conflict")
	}
	r.write("o.py", "def o():\n    return 9\n")

	out, _, err := runIn(t, r.dir, "diff", "--no-tty")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "Not shown: conflict m.py\n") || strings.Contains(out, "<<<<<<<") || !strings.Contains(out, "o.py:2 :: o") {
		t.Errorf("output:\n%s", out)
	}
}
