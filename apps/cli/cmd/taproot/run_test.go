package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runIn runs taproot in dir with args and returns what it printed to stdout
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

func TestRun_RejectsArgumentsItDoesNotKnow(t *testing.T) {
	dir := t.TempDir() // not a repository: a bad command line must fail before git runs
	for _, args := range [][]string{
		{"diff", "main"},
		{"diff", "HEAD~1"},
		{"main"},
		{"foo"},
		{"-v"},
		{"-s"},
		{"diff", "-staged"},
		{"diff", "--staged", "extra"},
		{"diff", "diff"},
		{"diff", "--no-tty=1"},
		{"diff", "--base="},
		{"diff", "--ref"},
		{"diff", "--ref", "--staged"},
		{"diff", "--order", "sideways"},
		{"diff", "--order", "top-down"},
		{"diff", "--preset", "top-down"},
		{"diff", "--staged", "--ref", "main"},
		{"diff", "--head", "feature"},
		{"--version", "extra"},
		{"--help", "extra"},
		{"help", "me"},
		{"diff", "--staged", "--base", "main", "--help"},
		{"diff", "--order", "sideways", "--version"},
	} {
		_, _, err := runIn(t, dir, args...)
		var ue usageError
		if !errors.As(err, &ue) {
			t.Errorf("taproot %q: err = %v, want a usage error", args, err)
		}
	}
	_, _, err := runIn(t, dir, "diff", "main")
	if err == nil || !strings.Contains(err.Error(), `"main"`) || !strings.Contains(err.Error(), "--base") || !strings.Contains(err.Error(), "--ref") {
		t.Errorf("taproot diff main: %v, want a hint naming --base and --ref", err)
	}
}

func TestRun_OrderPicksTheReadingOrder(t *testing.T) {
	r := newRepo(t, "")
	r.write("a.py", "def main():\n    return parse()\n\n\ndef parse():\n    return tokenize()\n\n\ndef tokenize():\n    return 1\n")
	r.commit("base")
	r.write("a.py", "def main():\n    return parse() + 1\n\n\ndef parse():\n    return tokenize() + 1\n\n\ndef tokenize():\n    return 2\n")

	symbols := func(out string) []string {
		var got []string
		for _, l := range strings.Split(out, "\n") {
			if _, sym, ok := strings.Cut(l, " :: "); ok && strings.Contains(l, "--- ") {
				got = append(got, strings.Fields(sym)[0])
			}
		}
		return got
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, "main parse tokenize"},
		{[]string{"--order", "callers-first"}, "main parse tokenize"},
		{[]string{"--order=callees-first"}, "tokenize parse main"},
	} {
		out, _, err := runIn(t, r.dir, append([]string{"diff", "--no-tty"}, c.args...)...)
		if got := strings.Join(symbols(out), " "); err != nil || got != c.want {
			t.Errorf("taproot diff %v: order %q, %v; want %q", c.args, got, err, c.want)
		}
	}
}

func TestRun_HelpAndVersionNeedNoRepository(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"--help"}, {"-h"}, {"diff", "--help"}, {"diff", "--staged", "--help"}} {
		out, _, err := runIn(t, dir, args...)
		if err != nil || out != usage {
			t.Errorf("taproot %q printed %q, %v; want the usage", args, out, err)
		}
	}
	for _, args := range [][]string{{"--version"}, {"-V"}, {"version"}} {
		out, _, err := runIn(t, dir, args...)
		if err != nil || out != "taproot "+buildVersion()+"\n" {
			t.Errorf("taproot %q printed %q, %v; want the version", args, out, err)
		}
	}
}

func TestRun_FlagValueAfterEquals(t *testing.T) {
	r := newRepo(t, "")
	r.write("a.py", "def a():\n    return 1\n")
	r.commit("base")
	r.git("checkout", "-qb", "feature")
	r.write("a.py", "def a():\n    return 2\n")
	r.commit("feature")

	spaced, _, err := runIn(t, r.dir, "diff", "--base", "main", "--no-tty")
	if err != nil {
		t.Fatal(err)
	}
	joined, _, err := runIn(t, r.dir, "diff", "--base=main", "--no-tty")
	if err != nil || joined != spaced || !strings.Contains(joined, "a.py:2 :: a") {
		t.Errorf("--base=main printed %q, %v; want what --base main prints, %q", joined, err, spaced)
	}
	help, _, err := runIn(t, r.dir, "help")
	if err != nil || !strings.HasPrefix(help, "taproot — ") {
		t.Errorf("taproot help printed %q, %v; want the usage", help, err)
	}
}

func TestRun_GitErrorsNameTheProblem(t *testing.T) {
	r := newRepo(t, "")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"diff", "--base", "main"}, "--base reviews commits, and this branch has none yet (--staged reviews what is staged)"},
		{[]string{"diff", "--ref", "HEAD"}, "--ref HEAD: no such branch, tag or commit; this branch has no commits yet"},
	} {
		if _, _, err := runIn(t, r.dir, c.args...); err == nil || err.Error() != c.want {
			t.Errorf("taproot %q in a repository with no commits: %v, want %q", c.args, err, c.want)
		}
	}

	r.write("a.py", "def a():\n    return 1\n")
	r.commit("base")
	r.write("main/x.py", "x = 1\n") // a directory named like the branch
	r.commit("dir")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"diff", "--base", "nope"}, "--base nope: no such branch, tag or commit"},
		{[]string{"diff", "--base", "main", "--head", "nope"}, "--head nope: no such branch, tag or commit"},
		{[]string{"diff", "--ref", "nope"}, "--ref nope: no such branch, tag or commit"},
	} {
		if _, _, err := runIn(t, r.dir, c.args...); err == nil || err.Error() != c.want {
			t.Errorf("taproot %q: %v, want %q", c.args, err, c.want)
		}
	}
	if _, stderr, err := runIn(t, r.dir, "diff", "--ref", "main", "--no-tty"); err != nil {
		t.Errorf("--ref main with a main/ directory: %v %s", err, stderr)
	}

	t.Setenv("PATH", t.TempDir())
	if _, _, err := runIn(t, r.dir, "diff"); err == nil || err.Error() != "git is not installed or not on PATH" {
		t.Errorf("without git: %v", err)
	}
}

func TestRun_NoChangesSaysWhatIsLeftOut(t *testing.T) {
	r := newRepo(t, "")
	r.write("a.py", "def a():\n    return 1\n")
	r.commit("base")
	check := func(want string, args ...string) {
		t.Helper()
		out, errOut, err := runIn(t, r.dir, args...)
		if err != nil || out != "" || errOut != want {
			t.Errorf("taproot %q: stdout %q, stderr %q, %v; want only stderr %q", args, out, errOut, err, want)
		}
	}
	check("No changes found.\n", "diff", "--no-tty")

	r.write("a.py", "def a():\n    return 2\n")
	r.git("add", "a.py")
	r.write("new.py", "x = 1\n")
	check("No changes found.\n"+
		"Staged changes are not shown: taproot diff --staged reviews them.\n"+
		"Untracked files are not reviewed: git add them, then run taproot diff --staged.\n", "diff", "--no-tty")

	r.git("commit", "-qm", "a")
	check("No changes found.\n", "diff", "--staged", "--no-tty")
}
