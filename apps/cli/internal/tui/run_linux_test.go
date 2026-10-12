package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

func TestRun_NamesFilesNotShownAtStart(t *testing.T) {
	p := openPTY(t, 24, 120)
	r := testRunner(t, manySnippets(3, 5)...)
	r.Skipped = []core.SkippedFile{{Path: "logo.png", Reason: "binary"}}
	done := runTUI(r)
	off := p.waitFor(t, 0, "Not shown: binary logo.png")
	p.ctl.Write([]byte("J"))
	p.waitFor(t, off, "quit")
	p.ctl.Write([]byte("q"))
	if v := waitDone(t, done); v != nil {
		t.Fatalf("Run = %v", v)
	}
}

func TestRun_OpensTheReviewedVersion(t *testing.T) {
	p := openPTY(t, 24, 120)
	repo, dir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "m.py"), []byte("checked out\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	log, editor := filepath.Join(dir, "log"), filepath.Join(dir, "ed")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + log + "'\ncat \"$2\" >> '" + log + "'\n"
	if err := os.WriteFile(editor, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", editor)
	r := testRunner(t, &core.Snippet{ID: "m", FilePath: "m.py", SymbolPath: "f", NodeType: "function", StartLine: 1,
		DiffLines: []string{"@@ -1,1 +1,2 @@", " def f():", "+    return 2"}})
	r.RepoRoot = repo
	r.Reviewed = func(string) ([]byte, error) { return []byte("def f():\n    return 2\n"), nil }
	done := runTUI(r)
	off := p.waitFor(t, 0, "quit")
	p.ctl.Write([]byte("o"))
	p.waitFor(t, off, "opened the reviewed m.py as a read-only copy")
	got, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.SplitN(string(got), "\n", 3)
	if args[0] != "+2" || filepath.Base(args[1]) != "m.py" || args[2] != "def f():\n    return 2\n" {
		t.Errorf("the editor got %q", got)
	}
	p.ctl.Write([]byte("q"))
	if v := waitDone(t, done); v != nil {
		t.Fatalf("Run = %v", v)
	}
	if _, err := os.Stat(args[1]); !os.IsNotExist(err) {
		t.Errorf("the copy outlived the session: %v", err)
	}
}

// TestRun_SignalRemovesCopies: a signal ends astra through os.Exit, which
// skips Run's defers. The TUI runs in a child process that SIGTERM ends.
func TestRun_SignalRemovesCopies(t *testing.T) {
	if report := os.Getenv("ASTRA_TEST_SIGNAL_REPORT"); report != "" {
		p := openPTY(t, 24, 80)
		r := testRunner(t, manySnippets(1, 3)...)
		r.RepoRoot = t.TempDir()
		r.Reviewed = func(string) ([]byte, error) { return []byte("reviewed\n"), nil }
		if _, _, err := r.editorPath(r.flat[0]); err != nil {
			t.Fatal(err)
		}
		copies := r.copies
		done := runTUI(r)
		p.waitFor(t, 0, "quit")
		if err := os.WriteFile(report+".tmp", []byte(copies), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(report+".tmp", report); err != nil {
			t.Fatal(err)
		}
		t.Fatalf("Run = %v, want SIGTERM to end the process", waitDone(t, done))
	}

	tmp := t.TempDir()
	report := filepath.Join(tmp, "copies")
	var out strings.Builder
	cmd := exec.Command(os.Args[0], "-test.run=^TestRun_SignalRemovesCopies$")
	cmd.Env = append(os.Environ(), "ASTRA_TEST_SIGNAL_REPORT="+report, "TMPDIR="+tmp)
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var copies []byte
	for end := time.Now().Add(5 * time.Second); len(copies) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(end) {
			cmd.Process.Kill()
			cmd.Wait()
			t.Fatalf("the TUI did not start:\n%s", out.String())
		}
		copies, _ = os.ReadFile(report)
	}
	cmd.Process.Signal(syscall.SIGTERM)
	cmd.Wait()
	if code := cmd.ProcessState.ExitCode(); code != 128+int(syscall.SIGTERM) {
		t.Errorf("exit status %d, want %d:\n%s", code, 128+int(syscall.SIGTERM), out.String())
	}
	if _, err := os.Stat(string(copies)); !os.IsNotExist(err) {
		t.Errorf("SIGTERM left the copies in %s", copies)
	}
}

// TestRun_EditReloadsTheDiff: after o, a file the editor changed is read
// again, so y never stages the version from before the edit.
func TestRun_EditReloadsTheDiff(t *testing.T) {
	p := openPTY(t, 24, 120)
	repo, dir := t.TempDir(), t.TempDir()
	path := filepath.Join(repo, "m.py")
	if err := os.WriteFile(path, []byte("def f():\n    return 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	flag, editor := filepath.Join(dir, "edit"), filepath.Join(dir, "ed")
	script := "#!/bin/sh\nif [ -e '" + flag + "' ]; then echo '    # fixed' >> \"$2\"; fi\n"
	if err := os.WriteFile(editor, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", editor)
	r := testRunner(t, &core.Snippet{ID: "m", FilePath: "m.py", SymbolPath: "f", NodeType: "function", StartLine: 1,
		DiffLines: []string{"@@ -1,1 +1,2 @@", " def f():", "+    return 2"}})
	r.RepoRoot = repo
	reloads := 0
	r.Reload = func() (*core.ReviewForest, []core.SkippedFile, error) {
		reloads++
		return core.BuildForest([]*core.Snippet{{ID: "m", FilePath: "m.py", SymbolPath: "f", NodeType: "function", StartLine: 1,
			DiffLines: []string{"@@ -1,1 +1,3 @@", " def f():", "+    return 2", "+    # fixed"}}}, nil), nil, nil
	}
	done := runTUI(r)
	off := p.waitFor(t, 0, "quit")
	p.ctl.Write([]byte("o"))
	off = p.waitFor(t, off, "quit")
	if err := os.WriteFile(flag, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	p.ctl.Write([]byte("o"))
	p.waitFor(t, off, "m.py changed: reloaded the diff")
	p.ctl.Write([]byte("q"))
	if v := waitDone(t, done); v != nil {
		t.Fatalf("Run = %v", v)
	}
	if reloads != 1 {
		t.Errorf("reloaded %d times, want once: only the second o changed the file", reloads)
	}
}

func TestRun_ShowsTheNoticeAtStart(t *testing.T) {
	p := openPTY(t, 24, 120)
	r := testRunner(t, manySnippets(3, 5)...)
	r.Notice = "verdicts are not saved this session"
	done := runTUI(r)
	p.waitFor(t, 0, "verdicts are not saved this session")
	p.ctl.Write([]byte("q"))
	if v := waitDone(t, done); v != nil {
		t.Fatalf("Run = %v", v)
	}
}
