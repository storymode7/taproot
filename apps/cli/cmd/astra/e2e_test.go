package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/storymode7/astra/apps/cli/internal/diff"
	"github.com/storymode7/astra/core"
)

// testRepo is a throwaway git repository whose git sees no user or system
// config beyond what the test writes to globalConfig.
type testRepo struct {
	t   *testing.T
	dir string
}

// newRepo makes a repository on branch main. globalConfig is the content
// of the only git config file outside the repository.
func newRepo(t *testing.T, globalConfig string) *testRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	cfg := filepath.Join(home, "gitconfig")
	if err := os.WriteFile(cfg, []byte(globalConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	r := &testRepo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.git("config", "user.email", "t@t")
	r.git("config", "user.name", "t")
	return r
}

// git runs git in the repository and returns its stdout.
func (r *testRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		r.t.Fatalf("git %v: %v\n%s", args, err, stderr)
	}
	return string(out)
}

func (r *testRepo) write(name, content string) {
	r.t.Helper()
	path := filepath.Join(r.dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// commit stages everything and commits it.
func (r *testRepo) commit(msg string) {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-qm", msg)
}

// load reads the working-tree review as run does.
func (r *testRepo) load(t *testing.T, applyMode bool) *loaded {
	t.Helper()
	d, err := (&review{adapter: &diff.GitDiffAdapter{RepoRoot: r.dir}, applyMode: applyMode}).load()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// stageEach stages snippets one at a time, as y does, in the given order.
func stageEach(t *testing.T, st *stager, snippets []*core.Snippet, order []int) {
	t.Helper()
	for _, i := range order {
		s := snippets[i]
		if err := st.stage(s, true); err != nil {
			t.Fatalf("staging %s:%s alone: %v\n%q", s.FilePath, s.SymbolPath, err, s.DiffLines)
		}
	}
}

// orders lists the snippet indices front to back and back to front.
func orders(n int) [][]int {
	fwd, back := make([]int, n), make([]int, n)
	for i := range n {
		fwd[i], back[i] = i, n-1-i
	}
	return [][]int{fwd, back}
}

func TestStage_CRLFFile(t *testing.T) {
	r := newRepo(t, "")
	oldSrc := "def a():\r\n    return 1\r\n\r\n\r\ndef b():\r\n    return 2\r\n"
	newSrc := "def a():\r\n    return 10\r\n\r\n\r\ndef b():\r\n    return 2\r\n\r\n\r\ndef c():\r\n    return 3\r\n"
	r.write("w.py", oldSrc)
	r.commit("base")
	r.write("w.py", newSrc)

	for _, order := range orders(2) {
		d := r.load(t, true)
		if len(d.snippets) != 2 {
			t.Fatalf("want snippets for a and c, got %v", symbols(d.snippets))
		}
		stageEach(t, d.stager, d.snippets, order)
		if got := r.git("show", ":w.py"); got != newSrc {
			t.Errorf("order %v: staged %q, want the working tree %q", order, got, newSrc)
		}
		if err := d.stager.stage(d.snippets[0], false); err != nil {
			t.Fatalf("unstaging %s: %v", d.snippets[0].SymbolPath, err)
		}
		if got, want := r.git("show", ":w.py"), strings.Replace(newSrc, "return 10", "return 1", 1); got != want {
			t.Errorf("order %v: after unstaging a, index %q, want %q", order, got, want)
		}
		r.git("reset", "-q")
	}
}

func TestStage_OddPaths(t *testing.T) {
	r := newRepo(t, "[core]\n\tquotePath = false\n")
	names := []string{"My Docs/foo.py", "foo.py", "café.py", "sp ace.py"}
	for i, n := range names {
		r.write(n, fmt.Sprintf("def f%d():\n    return 1\n", i))
	}
	r.commit("base")
	edited := map[string]string{}
	for i, n := range names {
		if n != "foo.py" {
			r.write(n, fmt.Sprintf("def f%d():\n    return 2\n", i))
			edited[n] = fmt.Sprintf("f%d", i)
		}
	}

	d := r.load(t, true)
	got := map[string]string{}
	for _, s := range d.snippets {
		got[s.FilePath] = s.SymbolPath
	}
	if !reflect.DeepEqual(got, edited) {
		t.Fatalf("snippets (path: symbol) = %q, want %q", got, edited)
	}
	stageEach(t, d.stager, d.snippets, orders(len(d.snippets))[0])
	staged := strings.Split(strings.TrimSuffix(r.git("diff", "--cached", "--name-only", "-z"), "\x00"), "\x00")
	want := []string{"My Docs/foo.py", "café.py", "sp ace.py"}
	if !reflect.DeepEqual(staged, want) {
		t.Errorf("staged %q, want %q", staged, want)
	}
}

// TestStage_NoNewlineAtEndOfFile stages the snippets of files whose old
// or new text lacks a final newline, so git marks a line with "\ No
// newline at end of file". Staging each alone, in either order, must give
// the working tree, and a second y must leave the index alone.
func TestStage_NoNewlineAtEndOfFile(t *testing.T) {
	for _, c := range []struct{ name, old, new string }{
		{"edit.py", "def a():\n    return 1\n\n\ndef b():\n    return 2", "def a():\n    return 10\n\n\ndef b():\n    return 20"},
		{"append.py", "def a():\n    return 1", "def a():\n    return 1\n\n\ndef f():\n    return 2\n"},
		{"append-noeol.py", "def a():\n    return 1", "def a():\n    return 1\n\n\ndef f():\n    return 2"},
		{"append-edit.py", "def a():\n    return 1", "def a():\n    return 10\n\n\ndef f():\n    return 2\n"},
		{"append.go", "package p\n\nfunc a() {\n\tx()\n}", "package p\n\nfunc a() {\n\tx()\n}\n\nfunc b() {\n\ty()\n}\n"},
		{"inner.js", "function a() {\n  return 1;\n}\n\nfunction b() {\n  x();\n  return 2;\n}", "function a() {\n  return 1;\n}\n\nfunction b() {\n  y();\n  return 2;\n}"},
		{"gains-eol.py", "def a():\n    return 1\n\n\ndef b():\n    return 2", "def a():\n    return 1\n\n\ndef b():\n    return 2\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRepo(t, "")
			r.write(c.name, c.old)
			r.commit("base")
			r.write(c.name, c.new)
			n := len(r.load(t, true).snippets)
			if n == 0 {
				t.Fatal("no snippets")
			}
			for _, order := range orders(n) {
				d := r.load(t, true)
				stageEach(t, d.stager, d.snippets, order)
				stageEach(t, d.stager, d.snippets, order)
				if got := r.git("show", ":"+c.name); got != c.new {
					t.Errorf("order %v: staged %q, want %q", order, got, c.new)
				}
				r.git("reset", "-q")
			}
		})
	}
}
