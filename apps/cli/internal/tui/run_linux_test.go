package tui

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/storymode7/astra/core"
)

// ptyTerm is a pseudo-terminal whose terminal side is stdin and stdout for
// one test; out collects what the TUI writes to it.
type ptyTerm struct {
	ctl, term *os.File
	mu        sync.Mutex
	out       strings.Builder
}

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

func openPTY(t *testing.T, rows, cols int) *ptyTerm {
	t.Helper()
	ctl, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skip("no pseudo-terminal:", err)
	}
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
	p := &ptyTerm{ctl: ctl, term: term}
	p.resize(t, rows, cols)
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := ctl.Read(b)
			p.mu.Lock()
			p.out.Write(b[:n])
			p.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	stdin, stdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = term, term
	t.Cleanup(func() {
		os.Stdin, os.Stdout = stdin, stdout
		term.Close()
		ctl.Close()
	})
	return p
}

func (p *ptyTerm) resize(t *testing.T, rows, cols int) {
	ws := [4]uint16{uint16(rows), uint16(cols)}
	if err := ioctl(p.term, syscall.TIOCSWINSZ, unsafe.Pointer(&ws)); err != nil {
		t.Fatal(err)
	}
}

func (p *ptyTerm) output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.out.String()
}

// waitFor waits until the output after offset from contains s, and returns
// the offset just past it.
func (p *ptyTerm) waitFor(t *testing.T, from int, s string) int {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if i := strings.Index(p.output()[from:], s); i >= 0 {
			return from + i + len(s)
		}
	}
	t.Fatalf("timed out waiting for %q; output:\n%q", s, p.output()[from:])
	return 0
}

func (p *ptyTerm) termios(t *testing.T) syscall.Termios {
	var tio syscall.Termios
	if err := ioctl(p.term, syscall.TCGETS, unsafe.Pointer(&tio)); err != nil {
		t.Fatal(err)
	}
	return tio
}

// runTUI starts r.Run and returns a channel that gets its error, or the
// value it panicked with.
func runTUI(r *Runner) chan any {
	done := make(chan any, 1)
	go func() {
		defer func() {
			if v := recover(); v != nil {
				done <- v
			}
		}()
		done <- r.Run()
	}()
	return done
}

func waitDone(t *testing.T, done chan any) any {
	t.Helper()
	select {
	case v := <-done:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("the TUI did not exit")
		return nil
	}
}

func assertRestored(t *testing.T, p *ptyTerm, before syscall.Termios) {
	t.Helper()
	const flags = syscall.ECHO | syscall.ICANON | syscall.ISIG
	if got := p.termios(t); got.Lflag&flags != before.Lflag&flags || got.Iflag != before.Iflag {
		t.Errorf("terminal not restored: lflag %#x, want %#x", got.Lflag, before.Lflag)
	}
	p.waitFor(t, 0, "\033[?1049l")
	out := p.output()
	if !strings.Contains(out, "\033[?1049h") {
		t.Error("the TUI did not switch to the alternate screen")
	}
	if !strings.HasSuffix(out, "\033[?25h\033[?1049l") {
		t.Errorf("exit does not show the cursor and leave the alternate screen: %q", out[max(0, len(out)-40):])
	}
}

func TestRun_CtrlCQuitsAndRestoresTerminal(t *testing.T) {
	p := openPTY(t, 24, 80)
	before := p.termios(t)
	done := runTUI(testRunner(t, manySnippets(3, 5)...))
	p.waitFor(t, 0, "quit")
	p.ctl.Write([]byte("\x03"))
	if v := waitDone(t, done); v != nil {
		t.Fatalf("Run = %v", v)
	}
	assertRestored(t, p, before)
}

func TestRun_PanicRestoresTerminal(t *testing.T) {
	p := openPTY(t, 24, 80)
	before := p.termios(t)
	r := testRunner(t, manySnippets(3, 5)...)
	r.applyMode = true
	r.stage = func(*core.Snippet, bool) error { panic("boom") }
	done := runTUI(r)
	p.waitFor(t, 0, "quit")
	p.ctl.Write([]byte("y"))
	if v := waitDone(t, done); v != "boom" {
		t.Fatalf("Run = %v, want the panic", v)
	}
	assertRestored(t, p, before)
}

func TestRun_ResizeRedraws(t *testing.T) {
	p := openPTY(t, 24, 80)
	done := runTUI(testRunner(t, manySnippets(3, 5)...))
	resize := func(rows, cols int) {
		p.resize(t, rows, cols)
		syscall.Kill(os.Getpid(), syscall.SIGWINCH)
	}
	rule := func(cols int) string { return dim + strings.Repeat("─", cols) + reset }

	off := p.waitFor(t, 0, "quit")
	resize(16, 60)
	off = p.waitFor(t, off, rule(60))

	p.ctl.Write([]byte("?"))
	off = p.waitFor(t, off, "Press any key")
	resize(20, 70)
	off = p.waitFor(t, off, "Press any key")
	p.ctl.Write([]byte("x"))
	off = p.waitFor(t, off, rule(70))

	p.ctl.Write([]byte("/ab"))
	off = p.waitFor(t, off, "/ab")
	resize(18, 72)
	off = p.waitFor(t, off, rule(72))
	off = p.waitFor(t, off, "/ab")

	p.ctl.Write([]byte("\x1b"))
	p.waitFor(t, off, "quit")
	p.ctl.Write([]byte("\x03"))
	if v := waitDone(t, done); v != nil {
		t.Fatalf("Run = %v", v)
	}
	if strings.Contains(p.output(), "rejected") {
		t.Error("the key that closed the help screen was run as a command")
	}
}
