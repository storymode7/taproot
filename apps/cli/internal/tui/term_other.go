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

func stdinReadable(int) bool { return true }

func notifyResize(chan<- os.Signal) {}
