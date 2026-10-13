//go:build !linux

package main

import (
	"os"
	"testing"
)

// openTerminal skips the test: it opens pseudo-terminals on Linux only.
func openTerminal(t *testing.T) *os.File {
	t.Helper()
	t.Skip("no pseudo-terminal helper on this system")
	return nil
}
