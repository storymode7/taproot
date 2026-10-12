//go:build linux || darwin

package tui

import (
	"os"
	"os/signal"
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
		uintptr(fd), ioctlGetTermios, uintptr(unsafe.Pointer(&saved))); errno != 0 {
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
		uintptr(fd), ioctlSetTermios, uintptr(unsafe.Pointer(&raw))); errno != 0 {
		return nil, errno
	}

	return &rawMode{fd: fd, saved: saved}, nil
}

func (r *rawMode) restore() {
	syscall.Syscall(syscall.SYS_IOCTL, //nolint:errcheck
		uintptr(r.fd), ioctlSetTermios, uintptr(unsafe.Pointer(&r.saved)))
}

// IsTerminal reports whether f is a terminal: whether it has terminal
// settings to read. A character device such as /dev/null has none.
func IsTerminal(f *os.File) bool {
	conn, err := f.SyscallConn()
	if err != nil {
		return false
	}
	var errno syscall.Errno
	var t syscall.Termios
	if err := conn.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlGetTermios, uintptr(unsafe.Pointer(&t)))
	}); err != nil {
		return false
	}
	return errno == 0
}

// notifyResize sends a signal on c whenever the terminal is resized.
func notifyResize(c chan<- os.Signal) {
	signal.Notify(c, syscall.SIGWINCH)
}
