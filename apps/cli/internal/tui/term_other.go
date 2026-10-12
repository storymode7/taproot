//go:build !linux && !darwin

package tui

import (
	"errors"
	"os"
)

// rawMode is not available here, so Run falls back to the plain View.
type rawMode struct{}

func enableRawMode() (*rawMode, error) {
	return nil, errors.New("interactive mode needs Linux or macOS")
}

func (*rawMode) restore() {}

// IsTerminal reports false: there are no terminal settings to read here.
func IsTerminal(*os.File) bool { return false }

func stdinReadable(int) bool { return true }

func notifyResize(chan<- os.Signal) {}
