package state

import (
	"path/filepath"
	"testing"
)

func TestStore_GetDefault_ReturnsPending(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
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
	store, err := Open(dir)
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
	store, err := Open(dir)
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

	// Open a new store pointing to the same directory.
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

func TestStore_Prune_RemovesStaleIDs(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
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
	store1, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = store1.Set("keep", "accepted", "")
	_ = store1.Set("stale", "reviewed", "")
	_ = store1.Prune(map[string]struct{}{"keep": {}})

	store2, err := Open(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if st := store2.Get("stale"); st.Status != "pending" {
		t.Errorf("stale not pruned after reload: status = %q", st.Status)
	}
}

func TestStore_All_ReturnsCopy(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
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
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = store.Set("x", "reviewed", "")

	expected := filepath.Join(dir, ".git", "astra", "state.json")
	if store.path != expected {
		t.Errorf("store.path = %q, want %q", store.path, expected)
	}
}
