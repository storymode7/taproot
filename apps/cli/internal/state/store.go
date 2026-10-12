// Package state manages review state persistence for the CLI.
//
// Each reviewed diff (a commit range, the index, or the working tree at a
// given HEAD) gets its own file under .git/astra/reviews/, so marking a
// function in one commit leaves it pending in the others. Files live inside
// .git and never leave the developer's machine.
package state

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// SnippetState holds the review status and optional comment for one snippet.
type SnippetState struct {
	// ID is the snippet's stable hash (file + symbolPath + startLine).
	ID string `json:"id"`
	// Status is one of: pending, reviewed, accepted, rejected.
	Status string `json:"status"`
	// Comment is a local draft comment for this snippet.
	Comment string `json:"comment,omitempty"`
}

// Store persists the snippet review states of one diff.
// It is safe for concurrent use.
type Store struct {
	mu       sync.Mutex
	path     string
	scope    string
	snippets map[string]*SnippetState // keyed by snippet ID
}

// Open opens the state store for one diff, identified by scope, under gitDir
// (the repo's .git directory, or a linked worktree's own git dir). The file is
// only written once a status is set.
func Open(gitDir, scope string) (*Store, error) {
	dir := filepath.Join(gitDir, "astra", "reviews")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}

	h := sha256.Sum256([]byte(scope))
	s := &Store{
		path:  filepath.Join(dir, fmt.Sprintf("%x.json", h[:8])),
		scope: scope,
	}
	saved, err := s.read()
	if err != nil {
		return nil, fmt.Errorf("load state: %w", err)
	}
	s.snippets = saved
	return s, nil
}

// Get returns the state for a snippet. Returns a default (pending) state if
// the snippet has not been seen before.
func (s *Store) Get(id string) *SnippetState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.snippets[id]; ok {
		return st
	}
	return &SnippetState{ID: id, Status: "pending"}
}

// Set updates the state for a snippet and saves it. The file is read again
// and only this snippet changes, so another astra on the same diff keeps
// its verdicts.
func (s *Store) Set(id, status, comment string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := &SnippetState{ID: id, Status: status, Comment: comment}
	s.snippets[id] = st
	saved, err := s.read()
	if err != nil {
		return err
	}
	saved[id] = st
	return s.write(saved)
}

// All returns a copy of all stored states.
func (s *Store) All() []*SnippetState {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*SnippetState, 0, len(s.snippets))
	for _, v := range s.snippets {
		cp := *v
		out = append(out, &cp)
	}
	return out
}

// Prune removes any stored states whose IDs are not in the given set.
// Call this after a force-push invalidates old snippet hashes.
func (s *Store) Prune(validIDs map[string]struct{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.snippets {
		if _, ok := validIDs[id]; !ok {
			delete(s.snippets, id)
		}
	}
	return s.write(s.snippets)
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

type storedFile struct {
	Scope    string          `json:"scope"`
	Snippets []*SnippetState `json:"snippets"`
}

// read returns the saved states; none when there is no file yet.
func (s *Store) read() (map[string]*SnippetState, error) {
	snippets := make(map[string]*SnippetState)
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return snippets, nil
	}
	if err != nil {
		return nil, err
	}
	var f storedFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	for _, st := range f.Snippets {
		snippets[st.ID] = st
	}
	return snippets, nil
}

// write saves snippets through a temp file of its own, renamed into place,
// so a reader never sees half a file and two writers never share one.
func (s *Store) write(snippets map[string]*SnippetState) error {
	f := storedFile{Scope: s.scope, Snippets: make([]*SnippetState, 0, len(snippets))}
	for _, v := range snippets {
		f.Snippets = append(f.Snippets, v)
	}
	sort.Slice(f.Snippets, func(i, j int) bool { return f.Snippets[i].ID < f.Snippets[j].ID })
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), "state-*.tmp")
	if err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), s.path)
	}
	if err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("write state: %w", err)
	}
	return nil
}
