package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/storymode7/taproot/apps/cli/internal/tui"
)

// TestNoDeltaFlag runs `taproot diff --no-tty` with a fake delta first on PATH
// and checks that --no-delta keeps it from running on a terminal, and that
// it never runs for /dev/null, a character device that is no terminal.
func TestNoDeltaFlag(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	emptyConfig := filepath.Join(home, "gitconfig")
	if err := os.WriteFile(emptyConfig, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", emptyConfig)
	t.Setenv("NO_COLOR", "")

	bin := t.TempDir()
	calls := filepath.Join(bin, "calls")
	fake := "#!/bin/sh\necho called >> '" + calls + "'\ncat >/dev/null\n"
	if err := os.WriteFile(filepath.Join(bin, "delta"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "t@t")
	git("config", "user.name", "t")
	src := filepath.Join(repo, "main.go")
	if err := os.WriteFile(src, []byte("package main\n\nfunc main() {\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "main.go")
	git("commit", "-q", "-m", "base")
	if err := os.WriteFile(src, []byte("package main\n\nfunc main() {\n\tprintln(1)\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	t.Cleanup(func() {
		os.Stdout = stdout
		devNull.Close()
		tui.NoDelta = false
	})
	check := func(out *os.File, wantDelta bool, args ...string) {
		t.Helper()
		os.Remove(calls)
		os.Stdout = out
		tui.NoDelta = false
		if err := run(args); err != nil {
			t.Fatalf("run %v: %v", args, err)
		}
		_, err := os.Stat(calls)
		if called := err == nil; called != wantDelta {
			t.Errorf("taproot %v to %s: delta ran = %v, want %v", args, out.Name(), called, wantDelta)
		}
	}

	check(devNull, false, "diff", "--no-tty")
	// A terminal as stdout makes --no-tty print highlighted output.
	term := openTerminal(t)
	check(term, true, "diff", "--no-tty")
	check(term, false, "diff", "--no-tty", "--no-delta")
}
