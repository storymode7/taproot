package tui

import (
	"os"
	"syscall"
)

const (
	ioctlGetTermios = syscall.TCGETS
	ioctlSetTermios = syscall.TCSETS
)

// stdinReadable reports whether a byte can be read from stdin within ms
// milliseconds.
func stdinReadable(ms int) bool {
	fd := int(os.Stdin.Fd())
	var set syscall.FdSet
	set.Bits[fd/64] |= 1 << (uint(fd) % 64)
	tv := syscall.NsecToTimeval(int64(ms) * 1e6)
	n, err := syscall.Select(fd+1, &set, nil, nil, &tv)
	return err == nil && n > 0
}
