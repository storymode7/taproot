package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStore_GetDefault_ReturnsPending(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir, "worktree@abc")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	st := store.Get("nonexistent")
	if st.Status != "pending" {
		t.Errorf("default status = %q, want pending", st.Status)
	}
	if st.ID != "nonexistent" {
		t.Errorf("default ID = %q, want nonexistent", st.ID)
	}
}

func TestStore_SetGet_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir, "worktree@abc")
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
	dir := t.TempDir()
	store, err := Open(dir, "worktree@abc")
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

	store1, err := Open(dir, "worktree@abc")
	if err != nil {
		t.Fatalf("Open store1: %v", err)
	}
	if err := store1.Set("id1", "rejected", "needs work"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Open a new store pointing to the same directory.
	store2, err := Open(dir, "worktree@abc")
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

func TestStore_Prune_RemovesStaleIDs(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir, "worktree@abc")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = store.Set("keep", "accepted", "")
	_ = store.Set("remove", "reviewed", "")

	valid := map[string]struct{}{"keep": {}}
	if err := store.Prune(valid); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	if st := store.Get("keep"); st.Status != "accepted" {
		t.Errorf("keep status = %q after prune, want accepted", st.Status)
	}
	// "remove" was pruned — should return the default pending state.
	if st := store.Get("remove"); st.Status != "pending" {
		t.Errorf("remove status = %q after prune, want pending", st.Status)
	}
}

func TestStore_Prune_PersistsToReload(t *testing.T) {
	dir := t.TempDir()
	store1, err := Open(dir, "worktree@abc")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = store1.Set("keep", "accepted", "")
	_ = store1.Set("stale", "reviewed", "")
	_ = store1.Prune(map[string]struct{}{"keep": {}})

	store2, err := Open(dir, "worktree@abc")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if st := store2.Get("stale"); st.Status != "pending" {
		t.Errorf("stale not pruned after reload: status = %q", st.Status)
	}
}

func TestStore_All_ReturnsCopy(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir, "worktree@abc")
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

func TestStore_StateFilePath(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir, "worktree@abc")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = store.Set("x", "reviewed", "")

	if got := filepath.Dir(store.path); got != filepath.Join(dir, "astra", "reviews") {
		t.Errorf("store dir = %q, want <gitDir>/astra/reviews", got)
	}
}

func TestStore_ScopesAreIndependent(t *testing.T) {
	dir := t.TempDir()
	a, _ := Open(dir, "commits a..b")
	if err := a.Set("id1", "reviewed", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}
	b, _ := Open(dir, "commits b..c")
	if st := b.Get("id1"); st.Status != "pending" {
		t.Errorf("other commit sees status %q, want pending", st.Status)
	}
	again, _ := Open(dir, "commits a..b")
	if st := again.Get("id1"); st.Status != "reviewed" {
		t.Errorf("same commit lost status: %q, want reviewed", st.Status)
	}
}

func TestStore_OpenWithoutSetWritesNothing(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, "commits a..b")
	s.Get("id1")
	if _, err := os.Stat(s.path); !os.IsNotExist(err) {
		t.Errorf("viewing without marking created %s", s.path)
	}
}

// Two sessions on one diff each save their own verdicts: a write changes
// only its snippet, and leaves no temp file behind.
func TestStore_TwoSessionsKeepEachOthersVerdicts(t *testing.T) {
	dir := t.TempDir()
	a, _ := Open(dir, "worktree@abc")
	b, _ := Open(dir, "worktree@abc")
	if err := a.Set("alpha", "accepted", ""); err != nil {
		t.Fatal(err)
	}
	if err := b.Set("beta", "reviewed", ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Set("gamma", "rejected", ""); err != nil {
		t.Fatal(err)
	}
	fresh, _ := Open(dir, "worktree@abc")
	for id, want := range map[string]string{"alpha": "accepted", "beta": "reviewed", "gamma": "rejected"} {
		if got := fresh.Get(id).Status; got != want {
			t.Errorf("%s = %q on disk, want %q", id, got, want)
		}
	}
	files, _ := os.ReadDir(filepath.Join(dir, "astra", "reviews"))
	if len(files) != 1 {
		t.Errorf("reviews holds %v, want one state file", files)
	}
}
