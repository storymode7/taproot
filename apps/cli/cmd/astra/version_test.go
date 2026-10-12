package main

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestVersion_SetAtLinkTime builds astra the way a release does and asks
// it for its version: -X silently does nothing unless main.version is a
// string variable that the program reads.
func TestVersion_SetAtLinkTime(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}
	bin := filepath.Join(t.TempDir(), "astra")
	build := exec.Command("go", "build", "-o", bin, "-ldflags", "-X main.version=v1.2.3", ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	for _, arg := range []string{"--version", "-V", "version"} {
		out, err := exec.Command(bin, arg).Output()
		if err != nil || string(out) != "astra v1.2.3\n" {
			t.Errorf("astra %s printed %q, %v; want \"astra v1.2.3\\n\"", arg, out, err)
		}
	}
}
