package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/storymode7/taproot/apps/cli/internal/diff"
	"github.com/storymode7/taproot/apps/cli/internal/state"
)

// judge gives the snippet of symbol a verdict the way y, x and r do: in a
// working-tree review that stages, y stages it before the verdict is saved.
func (r *testRepo) judge(t *testing.T, a *diff.GitDiffAdapter, applyMode bool, symbol, status string) {
	t.Helper()
	store, err := state.Open(filepath.Join(r.dir, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	a.RepoRoot = r.dir
	d, err := (&review{adapter: a, store: store, applyMode: applyMode}).load()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range d.snippets {
		if s.SymbolPath != symbol {
			continue
		}
		if applyMode && status == "accepted" {
			if err := d.stager.stage(s, true); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.Set(state.Key(s.ID, s.DiffLines), status, ""); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("no snippet %q in %v", symbol, symbols(d.snippets))
}

var reStatusLine = regexp.MustCompile(`^\s*--- \d+/\d+  .* :: (\S+)  \[(\w+)\]$`)

// statuses runs taproot --no-tty with args and returns each symbol's status.
func (r *testRepo) statuses(t *testing.T, args ...string) map[string]string {
	t.Helper()
	out, _, err := runIn(t, r.dir, append([]string{"diff", "--no-tty"}, args...)...)
	if err != nil {
		t.Fatalf("taproot %q: %v", args, err)
	}
	got := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if m := reStatusLine.FindStringSubmatch(l); m != nil {
			got[m[1]] = m[2]
		}
	}
	return got
}

const threeFuncs = "def alpha():\n    return %s\n\n\ndef beta():\n    return %s\n\n\ndef gamma():\n    return %s\n"

func funcs(a, b, c string) string {
	return fmt.Sprintf(threeFuncs, a, b, c)
}

// TestVerdicts_FollowTheChange: a verdict belongs to a snippet's change.
// It survives a commit and an amend, --staged shows what y staged as
// accepted, an edit to the change makes it pending again, and all of it
// lives in one file.
func TestVerdicts_FollowTheChange(t *testing.T) {
	r := newRepo(t, "")
	r.write("a.py", funcs("1", "2", "3"))
	r.commit("base")
	r.write("a.py", funcs("10", "20", "30"))

	worktree := &diff.GitDiffAdapter{}
	r.judge(t, worktree, true, "beta", "rejected")
	r.judge(t, worktree, true, "gamma", "reviewed")
	r.judge(t, worktree, true, "alpha", "accepted")
	want := map[string]string{"beta": "rejected", "gamma": "reviewed"}
	if got := r.statuses(t); !reflect.DeepEqual(got, want) {
		t.Errorf("after y on alpha: %v, want %v (alpha is staged)", got, want)
	}
	if got := r.statuses(t, "--staged"); !reflect.DeepEqual(got, map[string]string{"alpha": "accepted"}) {
		t.Errorf("--staged: %v, want alpha accepted", got)
	}

	r.git("commit", "-qm", "alpha")
	if got := r.statuses(t); !reflect.DeepEqual(got, want) {
		t.Errorf("after the commit: %v, want %v", got, want)
	}
	r.write("notes.txt", "n\n")
	r.git("add", "notes.txt")
	r.git("commit", "-q", "--amend", "--no-edit")
	if got := r.statuses(t); !reflect.DeepEqual(got, want) {
		t.Errorf("after an amend: %v, want %v", got, want)
	}

	r.write("a.py", funcs("10", "21", "30"))
	if got, want := r.statuses(t), map[string]string{"beta": "pending", "gamma": "reviewed"}; !reflect.DeepEqual(got, want) {
		t.Errorf("after beta is edited again: %v, want %v", got, want)
	}
	files, err := os.ReadDir(filepath.Join(r.dir, ".git", "taproot"))
	if err != nil || len(files) != 1 || files[0].Name() != "verdicts.json" {
		t.Errorf(".git/taproot holds %v, %v; want only verdicts.json", files, err)
	}
}

// TestVerdicts_AcceptedButNotStagedIsPending: in the review that stages,
// ✓ means staged. A snippet accepted with --no-apply, or unstaged with git
// reset, is still in the unstaged diff, so it shows as pending there.
func TestVerdicts_AcceptedButNotStagedIsPending(t *testing.T) {
	r := newRepo(t, "")
	r.write("a.py", funcs("1", "2", "3"))
	r.commit("base")
	r.write("a.py", funcs("10", "2", "3"))

	r.judge(t, &diff.GitDiffAdapter{}, false, "alpha", "accepted")
	if got := r.statuses(t); got["alpha"] != "pending" {
		t.Errorf("accepted with --no-apply, then the review that stages: alpha %q, want pending", got["alpha"])
	}
	if got := r.statuses(t, "--no-apply"); got["alpha"] != "accepted" {
		t.Errorf("--no-apply: alpha %q, want accepted", got["alpha"])
	}

	r.judge(t, &diff.GitDiffAdapter{}, true, "alpha", "accepted")
	if got := r.git("diff", "--cached", "--name-only"); got != "a.py\n" {
		t.Fatalf("y did not stage alpha: %q", got)
	}
	r.git("reset", "-q")
	if got := r.statuses(t); got["alpha"] != "pending" {
		t.Errorf("y, then git reset: alpha %q, want pending", got["alpha"])
	}
}

// TestVerdicts_SurviveARebase: a --base review keeps its verdicts when the
// branch is rebased and its diff does not change.
func TestVerdicts_SurviveARebase(t *testing.T) {
	r := newRepo(t, "")
	r.write("a.py", funcs("1", "2", "3"))
	r.commit("base")
	r.git("checkout", "-qb", "feature")
	r.write("a.py", funcs("10", "20", "3"))
	r.commit("feature")

	base := &diff.GitDiffAdapter{Base: "main"}
	r.judge(t, base, false, "alpha", "reviewed")
	r.judge(t, base, false, "beta", "rejected")
	want := map[string]string{"alpha": "reviewed", "beta": "rejected"}

	r.git("checkout", "-q", "main")
	r.write("README", "readme\n")
	r.commit("readme")
	r.git("checkout", "-q", "feature")
	r.git("rebase", "-q", "main")
	if got := r.statuses(t, "--base", "main"); !reflect.DeepEqual(got, want) {
		t.Errorf("after a rebase: %v, want %v", got, want)
	}

	r.write("a.py", funcs("10", "200", "3"))
	r.commit("fixup")
	if got, want := r.statuses(t, "--base", "main"), map[string]string{"alpha": "reviewed", "beta": "pending"}; !reflect.DeepEqual(got, want) {
		t.Errorf("after a fixup to beta: %v, want %v", got, want)
	}
}

// TestVerdicts_UnreadableStateDoesNotBlockReview: a corrupt verdicts file
// or a read-only .git leaves the review usable.
func TestVerdicts_UnreadableStateDoesNotBlockReview(t *testing.T) {
	r := newRepo(t, "")
	r.write("a.py", funcs("1", "2", "3"))
	r.commit("base")
	r.git("checkout", "-qb", "feature")
	r.write("a.py", funcs("10", "2", "3"))
	r.commit("feature")

	path := filepath.Join(r.dir, ".git", "taproot", "verdicts.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"verdicts": [`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := runIn(t, r.dir, "diff", "--base", "main", "--no-tty")
	if err != nil || !strings.Contains(out, ":: alpha  [pending]") || !strings.Contains(errOut, "verdicts are not saved this session") {
		t.Errorf("corrupt state: err %v, stdout %q, stderr %q", err, out, errOut)
	}

	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	if err := os.RemoveAll(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(r.dir, ".git")
	if err := os.Chmod(gitDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(gitDir, 0o755) })
	out, errOut, err = runIn(t, r.dir, "diff", "--base", "main", "--no-tty")
	if err != nil || !strings.Contains(out, ":: alpha  [pending]") || errOut != "" {
		t.Errorf("read-only .git: err %v, stdout %q, stderr %q", err, out, errOut)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Errorf("a review that marks nothing created %s", filepath.Dir(path))
	}
}
