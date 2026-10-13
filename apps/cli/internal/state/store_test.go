package state

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStore_GetDefault_ReturnsPending(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	st := store.Get("nonexistent")
	if st.Status != "pending" {
		t.Errorf("default status = %q, want pending", st.Status)
	}
	if st.Key != "nonexistent" {
		t.Errorf("default key = %q, want nonexistent", st.Key)
	}
}

func TestStore_SetGet_RoundTrip(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := store.Set("id1", "accepted", "looks good"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	st := store.Get("id1")
	if st.Status != "accepted" {
		t.Errorf("status = %q, want accepted", st.Status)
	}
	if st.Comment != "looks good" {
		t.Errorf("comment = %q, want 'looks good'", st.Comment)
	}
}

func TestStore_SetMultiple_AllRetrievable(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ids := []string{"alpha", "beta", "gamma"}
	statuses := []string{"accepted", "rejected", "reviewed"}
	for i, id := range ids {
		if err := store.Set(id, statuses[i], ""); err != nil {
			t.Fatalf("Set(%s): %v", id, err)
		}
	}
	for i, id := range ids {
		st := store.Get(id)
		if st.Status != statuses[i] {
			t.Errorf("Get(%s).Status = %q, want %q", id, st.Status, statuses[i])
		}
	}
}

func TestStore_Persistence_ReloadFromDisk(t *testing.T) {
	dir := t.TempDir()

	store1, err := Open(dir)
	if err != nil {
		t.Fatalf("Open store1: %v", err)
	}
	if err := store1.Set("id1", "rejected", "needs work"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	store2, err := Open(dir)
	if err != nil {
		t.Fatalf("Open store2: %v", err)
	}
	st := store2.Get("id1")
	if st.Status != "rejected" {
		t.Errorf("persisted status = %q, want rejected", st.Status)
	}
	if st.Comment != "needs work" {
		t.Errorf("persisted comment = %q, want 'needs work'", st.Comment)
	}
}

func TestStore_All_ReturnsCopy(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = store.Set("a", "accepted", "")
	_ = store.Set("b", "rejected", "")

	all := store.All()
	if len(all) != 2 {
		t.Fatalf("All() returned %d entries, want 2", len(all))
	}
}

func TestStore_OneFilePerGitDir(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, k := range []string{"x", "y", "z"} {
		if err := store.Set(k, "reviewed", ""); err != nil {
			t.Fatal(err)
		}
	}
	files, err := os.ReadDir(filepath.Join(dir, "taproot"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name() != "verdicts.json" {
		t.Errorf("<gitDir>/taproot holds %v, want only verdicts.json", files)
	}
}

func TestKey_FollowsTheChangeNotItsPlace(t *testing.T) {
	change := []string{"@@ -3,4 +3,4 @@ func a() {", " \tx := 1", "-\treturn x", "+\treturn x + 1", " }"}
	moved := []string{"@@ -30,3 +31,3 @@", " \ty := 2", "-\treturn x", "+\treturn x + 1"}
	if Key("id", change) != Key("id", moved) {
		t.Error("a change that moved, with other context, must keep its key")
	}
	for name, other := range map[string][]string{
		"another added line": {"@@ -3,4 +3,4 @@", "-\treturn x", "+\treturn x + 2"},
		"one more line":      {"@@ -3,4 +3,5 @@", "-\treturn x", "+\treturn x + 1", "+\tsteal()"},
		"no final newline":   {"@@ -3,4 +3,4 @@", "-\treturn x", "+\treturn x + 1", `\ No newline at end of file`},
	} {
		if Key("id", change) == Key("id", other) {
			t.Errorf("%s: same key as the original change", name)
		}
	}
	if Key("id", change) == Key("other", change) {
		t.Error("the same change in another snippet must have another key")
	}
}

func TestStore_OpenWithoutSetWritesNothing(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	s.Get("id1")
	s.Reset("id1")
	if _, err := os.Stat(filepath.Join(dir, "taproot")); !os.IsNotExist(err) {
		t.Errorf("viewing without marking created %s", filepath.Join(dir, "taproot"))
	}
}

func TestStore_ResetShowsPendingForTheSession(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	if err := s.Set("k", "accepted", "note"); err != nil {
		t.Fatal(err)
	}
	s.Reset("k")
	if st := s.Get("k"); st.Status != "pending" || st.Comment != "note" {
		t.Errorf("after Reset: %+v, want pending with the comment kept", st)
	}
	again, _ := Open(dir)
	if st := again.Get("k"); st.Status != "accepted" {
		t.Errorf("Reset changed the saved verdict: %q", st.Status)
	}
}

// Two sessions on one repository each save their own verdicts: a write
// changes only its key, and leaves no temp file behind.
func TestStore_TwoSessionsKeepEachOthersVerdicts(t *testing.T) {
	dir := t.TempDir()
	a, _ := Open(dir)
	b, _ := Open(dir)
	if err := a.Set("alpha", "accepted", ""); err != nil {
		t.Fatal(err)
	}
	if err := b.Set("beta", "reviewed", ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Set("gamma", "rejected", ""); err != nil {
		t.Fatal(err)
	}
	fresh, _ := Open(dir)
	for k, want := range map[string]string{"alpha": "accepted", "beta": "reviewed", "gamma": "rejected"} {
		if got := fresh.Get(k).Status; got != want {
			t.Errorf("%s = %q on disk, want %q", k, got, want)
		}
	}
	files, _ := os.ReadDir(filepath.Join(dir, "taproot"))
	if len(files) != 1 {
		t.Errorf("<gitDir>/taproot holds %v, want only verdicts.json", files)
	}
}

// An unreadable file never blocks a review: the store keeps verdicts in
// memory, says why, and leaves the file as it was.
func TestStore_UnreadableFileKeepsVerdictsInMemory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "taproot", "verdicts.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", `{"verdicts": [`, `{"verdicts": []}]`} {
		if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := Open(dir)
		if s == nil || err == nil || !strings.Contains(err.Error(), "not saved") || !strings.Contains(err.Error(), path) {
			t.Fatalf("Open with %q: %v, want a usable store and an error naming the file", bad, err)
		}
		if err := s.Set("k", "reviewed", ""); err != nil {
			t.Errorf("Set in memory: %v", err)
		}
		if got := s.Get("k").Status; got != "reviewed" {
			t.Errorf("status in memory = %q", got)
		}
		if got, _ := os.ReadFile(path); string(got) != bad {
			t.Errorf("the unreadable file was rewritten to %q", got)
		}
	}
}

// A read-only .git opens fine; marking says why it could not be saved and
// keeps the verdict for the session.
func TestStore_ReadOnlyGitDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open on a read-only git dir: %v", err)
	}
	if err := s.Set("k", "accepted", ""); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Set = %v, want a permission error", err)
	}
	if got := s.Get("k").Status; got != "accepted" {
		t.Errorf("status in memory = %q, want accepted", got)
	}
}
