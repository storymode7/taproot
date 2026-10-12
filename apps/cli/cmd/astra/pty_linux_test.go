package main

import (
	"fmt"
	"io"
	"os"
	"syscall"
	"testing"
	"unsafe"
)

func ioctl(f *os.File, req uintptr, arg unsafe.Pointer) error {
	var errno syscall.Errno
	conn, err := f.SyscallConn()
	if err != nil {
		return err
	}
	if err := conn.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg))
	}); err != nil {
		return err
	}
	if errno != 0 {
		return errno
	}
	return nil
}

// openTerminal returns the terminal side of a new pseudo-terminal whose
// output is read and dropped. It skips the test when there is none.
func openTerminal(t *testing.T) *os.File {
	t.Helper()
	ctl, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skip("no pseudo-terminal:", err)
	}
	t.Cleanup(func() { ctl.Close() })
	var unlock int32
	var n uint32
	if err := ioctl(ctl, syscall.TIOCSPTLCK, unsafe.Pointer(&unlock)); err != nil {
		t.Fatal(err)
	}
	if err := ioctl(ctl, syscall.TIOCGPTN, unsafe.Pointer(&n)); err != nil {
		t.Fatal(err)
	}
	term, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { term.Close() })
	go io.Copy(io.Discard, ctl)
	return term
}
