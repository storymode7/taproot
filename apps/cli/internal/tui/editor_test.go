package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/storymode7/astra/core"
)

func TestEditorPath(t *testing.T) {
	repo := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("same.py", "reviewed\n")
	write("pkg/differs.py", "checked out\n")
	if err := os.Symlink(os.DevNull, filepath.Join(repo, "dev.py")); err != nil {
		t.Fatal(err)
	}
	reviewed := map[string]string{"same.py": "reviewed\n", "pkg/differs.py": "reviewed\n", "new.py": "reviewed\n", "dev.py": os.DevNull, "a/../../x.py": "escape\n"}

	r := testRunner(t)
	r.RepoRoot = repo
	t.Cleanup(r.removeCopies)
	open := func(path string) (string, error) {
		p, _, err := r.editorPath(&core.Snippet{FilePath: path})
		return p, err
	}

	// The working tree is the reviewed version.
	if p, err := open("same.py"); err != nil || p != filepath.Join(repo, "same.py") {
		t.Errorf("worktree mode: %q, %v", p, err)
	}
	for _, path := range []string{"missing.py", "dev.py", "../x.py"} {
		if p, err := open(path); err == nil {
			t.Errorf("worktree mode opens %s as %q", path, p)
		}
	}

	// --base or --staged: the reviewed version comes from git.
	r.Reviewed = func(path string) ([]byte, error) {
		if s, ok := reviewed[path]; ok {
			return []byte(s), nil
		}
		return nil, errors.New("not in the reviewed version")
	}
	if p, err := open("same.py"); err != nil || p != filepath.Join(repo, "same.py") {
		t.Errorf("an unchanged working-tree file must open itself: %q, %v", p, err)
	}
	for _, path := range []string{"pkg/differs.py", "new.py", "dev.py", "pkg/differs.py"} {
		p, err := open(path)
		if err != nil || !strings.HasPrefix(p, r.copies+string(filepath.Separator)) || !strings.HasSuffix(p, filepath.FromSlash(path)) {
			t.Errorf("%s: opened %q, %v; want a copy named like it", path, p, err)
			continue
		}
		fi, _ := os.Stat(p)
		if got, _ := os.ReadFile(p); string(got) != reviewed[path] || fi.Mode().Perm()&0o222 != 0 {
			t.Errorf("%s: the copy holds %q with mode %v, want the reviewed text, read-only", path, got, fi.Mode())
		}
	}
	for _, path := range []string{"missing.py", "a/../../x.py"} {
		if p, err := open(path); err == nil {
			t.Errorf("opens %s as %q", path, p)
		}
	}
	if _, _, err := r.editorPath(&core.Snippet{FilePath: "same.py", IsDeleted: true}); err == nil {
		t.Error("a deleted file must not open")
	}

	copies := r.copies
	r.removeCopies()
	if _, err := os.Stat(copies); !os.IsNotExist(err) {
		t.Errorf("copies left behind: %v", err)
	}
}
