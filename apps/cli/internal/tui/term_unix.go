package tui

import (
	"os"
	"syscall"
	"unsafe"
)

type rawMode struct {
	fd    int
	saved syscall.Termios
}

func enableRawMode() (*rawMode, error) {
	fd := int(os.Stdin.Fd())

	var saved syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		uintptr(fd), syscall.TCGETS, uintptr(unsafe.Pointer(&saved))); errno != 0 {
		return nil, errno
	}

	raw := saved
	// Disable echo, canonical mode, signals, flow control.
	// NOTE: intentionally keep OPOST so \n → \r\n translation is preserved.
	// Without it the terminal produces a staircase effect (each line indented
	// further right) since raw output mode suppresses the CR-NL mapping.
	raw.Iflag &^= syscall.BRKINT | syscall.ICRNL | syscall.INPCK | syscall.ISTRIP | syscall.IXON
	raw.Cflag |= syscall.CS8
	raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.IEXTEN | syscall.ISIG
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0

	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		uintptr(fd), syscall.TCSETS, uintptr(unsafe.Pointer(&raw))); errno != 0 {
		return nil, errno
	}

	return &rawMode{fd: fd, saved: saved}, nil
}

func (r *rawMode) restore() {
	syscall.Syscall(syscall.SYS_IOCTL, //nolint:errcheck
		uintptr(r.fd), syscall.TCSETS, uintptr(unsafe.Pointer(&r.saved)))
}

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
