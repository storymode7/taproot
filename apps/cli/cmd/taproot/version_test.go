package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestVersion_SetAtLinkTime builds taproot the way a release does and asks
// it for its version: -X silently does nothing unless main.version is a
// string variable that the program reads.
func TestVersion_SetAtLinkTime(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}
	bin := filepath.Join(t.TempDir(), "taproot")
	build := exec.Command("go", "build", "-o", bin, "-ldflags", "-X main.version=v1.2.3", ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	for _, arg := range []string{"--version", "-V", "version"} {
		out, err := exec.Command(bin, arg).Output()
		if err != nil || string(out) != "taproot v1.2.3\n" {
			t.Errorf("taproot %s printed %q, %v; want \"taproot v1.2.3\\n\"", arg, out, err)
		}
	}
}

// TestUsage_MatchesTheTUI pins the --help lines that drifted from the
// TUI: n searches (x rejects), y stages in the default review, and only
// named snippets keep their verdict when lines shift.
func TestUsage_MatchesTheTUI(t *testing.T) {
	for _, s := range []string{"x       Reject", "n / N   Next/prev hit", "/       Search", "y also stages", "-h, --help", "-V, --version", "keyed by its first line"} {
		if !strings.Contains(usage, s) {
			t.Errorf("usage lacks %q", s)
		}
	}
	if !strings.HasPrefix(usage, "taproot — call-tree diff reviewer\n") {
		t.Errorf("usage opens with %q", strings.SplitN(usage, "\n", 2)[0])
	}
	for _, s := range []string{"n / x", "does not stage", "they survive commit", "AST-aware", "call-graph depth"} {
		if strings.Contains(usage, s) {
			t.Errorf("usage still says %q", s)
		}
	}
}
