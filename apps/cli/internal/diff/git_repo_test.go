package diff

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepo is a throwaway repository whose git reads no config but the
// repository's own and the global file the test writes.
type gitRepo struct {
	t   *testing.T
	dir string
}

func newGitRepo(t *testing.T, globalConfig string) *gitRepo {
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
	r := &gitRepo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.git("config", "user.email", "t@t")
	r.git("config", "user.name", "t")
	return r
}

func (r *gitRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (r *gitRepo) write(name, content string) {
	r.t.Helper()
	path := filepath.Join(r.dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// hostileConfig is a global git config that changes what git diff prints
// in every way a user's config can.
func hostileConfig(t *testing.T) string {
	ext := filepath.Join(t.TempDir(), "extdiff")
	if err := os.WriteFile(ext, []byte("#!/bin/sh\necho \"EXTDIFF $1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_EXTERNAL_DIFF", ext)
	return `[diff]
	noprefix = true
	mnemonicPrefix = true
	srcPrefix = SRC/
	dstPrefix = DST/
	external = ` + ext + `
	submodule = log
	renames = copies
[diff "upper"]
	textconv = tr a-z A-Z
[color]
	ui = always
	diff = always
[core]
	quotePath = false
[apply]
	whitespace = error
	ignoreWhitespace = change
`
}

// fileSummary lists each file's path and changed lines, one per line.
func fileSummary(files []*FileDiff) string {
	var b strings.Builder
	for _, f := range files {
		b.WriteString(f.Path + ":")
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				if strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-") {
					b.WriteString(" " + l)
				}
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func TestFiles_IgnoresUserDiffConfig(t *testing.T) {
	r := newGitRepo(t, hostileConfig(t))
	if err := os.WriteFile(filepath.Join(r.dir, ".git", "info", "attributes"), []byte("*.py diff=upper\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.write("top.py", "def a():\n    return 1\n")
	r.write("pkg/mod.py", "def b():\n    return 1\n")
	r.git("add", "-A")
	r.git("commit", "-qm", "base")
	r.git("branch", "pkg") // a branch named like the pkg directory
	r.write("top.py", "def a():\n    return 2\n")
	r.write("pkg/mod.py", "def b():\n    return 2\n")

	want := "pkg/mod.py: -    return 1 +    return 2\ntop.py: -    return 1 +    return 2\n"
	for _, a := range []*GitDiffAdapter{{RepoRoot: r.dir}, {RepoRoot: r.dir, Ref: "pkg"}} {
		files, err := a.Files()
		if err != nil {
			t.Fatalf("Ref %q: %v", a.Ref, err)
		}
		if got := fileSummary(files); got != want {
			t.Errorf("Ref %q: files =\n%s\nwant\n%s", a.Ref, got, want)
		}
	}

	// A submodule bump next to an edit: diff.submodule=log must not glue
	// "Submodule" lines onto the edit's hunk.
	r.git("add", "-A")
	r.git("commit", "-qm", "edit")
	r.git("update-index", "--add", "--cacheinfo", "160000,1111111111111111111111111111111111111111,sub")
	r.git("commit", "-qm", "sub")
	r.git("update-index", "--cacheinfo", "160000,2222222222222222222222222222222222222222,sub")
	r.write("top.py", "def a():\n    return 3\n")
	r.git("add", "top.py")
	files, err := (&GitDiffAdapter{RepoRoot: r.dir, Staged: true}).Files()
	if err != nil {
		t.Fatal(err)
	}
	want = "sub: -Subproject commit 1111111111111111111111111111111111111111 +Subproject commit 2222222222222222222222222222222222222222\n" +
		"top.py: -    return 2 +    return 3\n"
	if got := fileSummary(files); got != want {
		t.Errorf("staged files =\n%s\nwant\n%s", got, want)
	}
}

func TestApplyCached_IgnoresUserApplyConfig(t *testing.T) {
	r := newGitRepo(t, hostileConfig(t))
	r.write("w.py", "def a():\n    return 1\n\n\ndef b():\n  return 2\n")
	r.git("add", "-A")
	r.git("commit", "-qm", "base")
	// A trailing space (apply.whitespace=error refuses it) and a
	// whitespace-only change (apply.ignoreWhitespace=change takes it as
	// already applied).
	newSrc := "def a():\n    return 1 \n\n\ndef b():\n    return 2\n"
	r.write("w.py", newSrc)

	a := &GitDiffAdapter{RepoRoot: r.dir}
	files, err := a.Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("want one file, got %d", len(files))
	}
	var patch strings.Builder
	patch.WriteString("--- a/w.py\n+++ b/w.py\n")
	for _, h := range files[0].Hunks {
		patch.WriteString(strings.Join(h.Lines, "\n") + "\n")
	}
	if err := a.ApplyCached(patch.String(), false); err != nil {
		t.Fatal(err)
	}
	if got := r.git("show", ":w.py"); got != newSrc {
		t.Errorf("staged %q, want %q", got, newSrc)
	}
}

func TestGit_ErrorIsGitsMessage(t *testing.T) {
	r := newGitRepo(t, "")
	_, err := (&GitDiffAdapter{RepoRoot: r.dir}).RunGit("rev-parse", "--verify", "nope")
	if err == nil || strings.HasSuffix(err.Error(), "\n") || !strings.HasSuffix(err.Error(), "fatal: Needed a single revision") {
		t.Errorf("err = %q, want git's message without the trailing newline", err)
	}
}
