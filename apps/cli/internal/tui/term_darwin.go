package tui

import (
	"os"
	"syscall"
)

const (
	ioctlGetTermios = syscall.TIOCGETA
	ioctlSetTermios = syscall.TIOCSETA
)

// stdinReadable reports whether a byte can be read from stdin within ms
// milliseconds.
func stdinReadable(ms int) bool {
	fd := int(os.Stdin.Fd())
	var set syscall.FdSet
	set.Bits[fd/32] |= 1 << (uint(fd) % 32)
	tv := syscall.NsecToTimeval(int64(ms) * 1e6)
	// Select returns no count here: a timeout leaves the set cleared.
	err := syscall.Select(fd+1, &set, nil, nil, &tv)
	return err == nil && set.Bits[fd/32]&(1<<(uint(fd)%32)) != 0
}
