package main

import (
	"os"
	"os/exec"
	"path/filepath"
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

// snippets reads the diff the way run does: files, post-images, snippets.
func (r *testRepo) snippets(a *diff.GitDiffAdapter) []*core.Snippet {
	r.t.Helper()
	files, err := a.Files()
	if err != nil {
		r.t.Fatal(err)
	}
	for _, f := range files {
		if f.Language == "" || f.IsDeleted || f.IsBinary {
			continue
		}
		if src, err := a.FileBlob(diff.BlobRequest{Path: f.Path}); err == nil {
			f.Source = src
		}
	}
	return core.BuildSnippets(files)
}

// stageEach stages snippets one at a time, as y does, in the given order.
func stageEach(t *testing.T, a *diff.GitDiffAdapter, snippets []*core.Snippet, order []int) {
	t.Helper()
	for _, i := range order {
		s := snippets[i]
		if err := a.ApplyCached(buildAcceptedPatch([]*core.Snippet{s}), false); err != nil {
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

	a := &diff.GitDiffAdapter{RepoRoot: r.dir}
	snippets := r.snippets(a)
	if len(snippets) != 2 {
		t.Fatalf("want snippets for a and c, got %v", symbols(snippets))
	}
	for _, order := range orders(len(snippets)) {
		stageEach(t, a, snippets, order)
		if got := r.git("show", ":w.py"); got != newSrc {
			t.Errorf("order %v: staged %q, want the working tree %q", order, got, newSrc)
		}
		if err := a.ApplyCached(buildAcceptedPatch(snippets[:1]), true); err != nil {
			t.Fatalf("unstaging %s: %v", snippets[0].SymbolPath, err)
		}
		if got, want := r.git("show", ":w.py"), strings.Replace(newSrc, "return 10", "return 1", 1); got != want {
			t.Errorf("order %v: after unstaging a, index %q, want %q", order, got, want)
		}
		r.git("reset", "-q")
	}
}
