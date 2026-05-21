// Package state manages review state persistence for the CLI.
//
// State is stored in .git/astra/state.json relative to the repository root.
// The file is gitignored and never leaves the developer's machine.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// Store persists snippet review states to .git/astra/state.json.
// It is safe for concurrent use.
type Store struct {
	mu       sync.Mutex
	path     string
	snippets map[string]*SnippetState // keyed by snippet ID
}

// Open opens (or creates) the state store for the git repo rooted at repoRoot.
// Creates the .git/astra/ directory and state.json if they don't exist.
func Open(repoRoot string) (*Store, error) {
	dir := filepath.Join(repoRoot, ".git", "astra")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}

	path := filepath.Join(dir, "state.json")
	s := &Store{
		path:     path,
		snippets: make(map[string]*SnippetState),
	}

	if err := s.load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load state: %w", err)
	}

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

// Set updates the state for a snippet and flushes to disk.
func (s *Store) Set(id, status, comment string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snippets[id] = &SnippetState{ID: id, Status: status, Comment: comment}
	return s.flush()
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
	return s.flush()
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

type storedFile struct {
	Snippets []*SnippetState `json:"snippets"`
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var f storedFile
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("parse state.json: %w", err)
	}
	for _, st := range f.Snippets {
		s.snippets[st.ID] = st
	}
	return nil
}

func (s *Store) flush() error {
	states := make([]*SnippetState, 0, len(s.snippets))
	for _, v := range s.snippets {
		states = append(states, v)
	}
	data, err := json.MarshalIndent(storedFile{Snippets: states}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	// Write atomically via a temp file + rename.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write state tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("rename state: %w", err)
	}
	return nil
}
