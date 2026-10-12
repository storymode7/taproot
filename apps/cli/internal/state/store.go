// Package state manages review state persistence for the CLI.
//
// A repository's verdicts live in one file, .git/astra/verdicts.json, each
// keyed by a snippet and its exact change (Key). So a verdict follows the
// change, not the commit: it survives commit, amend and rebase, every
// review mode sees it, and it resets when the change itself changes. The
// file lives inside .git and never leaves the developer's machine.
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

// SnippetState holds the review status and optional comment for one
// snippet's change.
type SnippetState struct {
	// Key is the snippet's ID and change (Key).
	Key string `json:"key"`
	// Status is one of: pending, reviewed, accepted, rejected.
	Status string `json:"status"`
	// Comment is a local draft comment for this snippet.
	Comment string `json:"comment,omitempty"`
}

// Key names the verdict on a snippet's change: its ID and a hash of its
// changed lines (+, - and "\ No newline at end of file"), without @@
// headers or context, so lines shifting around it keep the verdict.
func Key(id string, diffLines []string) string {
	h := sha256.New()
	for _, l := range diffLines {
		if l != "" && (l[0] == '+' || l[0] == '-' || l[0] == '\\') {
			h.Write([]byte(l))
			h.Write([]byte{'\n'})
		}
	}
	return fmt.Sprintf("%s:%x", id, h.Sum(nil)[:8])
}

// Store persists the verdicts of one repository (one git dir).
// It is safe for concurrent use.
type Store struct {
	mu       sync.Mutex
	path     string
	verdicts map[string]*SnippetState // by key
}

// Open opens the verdicts saved under gitDir (the repo's .git directory, or
// a linked worktree's own git dir). The file is only written once a status
// is set.
func Open(gitDir string) (*Store, error) {
	s := &Store{path: filepath.Join(gitDir, "astra", "verdicts.json")}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}
	saved, err := s.read()
	if err != nil {
		return nil, fmt.Errorf("load state: %w", err)
	}
	s.verdicts = saved
	return s, nil
}

// Get returns the state for a key. Returns a default (pending) state if the
// change has no verdict.
func (s *Store) Get(key string) *SnippetState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.verdicts[key]; ok {
		return st
	}
	return &SnippetState{Key: key, Status: "pending"}
}

// Set records a verdict and saves it. The file is read again and only this
// key changes, so another astra on the same repository keeps its verdicts.
func (s *Store) Set(key, status, comment string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := &SnippetState{Key: key, Status: status, Comment: comment}
	s.verdicts[key] = st
	saved, err := s.read()
	if err != nil {
		return err
	}
	saved[key] = st
	return s.write(saved)
}

// Reset shows key as pending for the rest of this session, keeping its
// comment. The saved verdict is left alone.
func (s *Store) Reset(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.verdicts[key]; ok {
		s.verdicts[key] = &SnippetState{Key: key, Status: "pending", Comment: st.Comment}
	}
}

// All returns a copy of all stored states.
func (s *Store) All() []*SnippetState {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*SnippetState, 0, len(s.verdicts))
	for _, v := range s.verdicts {
		cp := *v
		out = append(out, &cp)
	}
	return out
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

type storedFile struct {
	Verdicts []*SnippetState `json:"verdicts"`
}

// read returns the saved verdicts; none when there is no file yet.
func (s *Store) read() (map[string]*SnippetState, error) {
	verdicts := make(map[string]*SnippetState)
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return verdicts, nil
	}
	if err != nil {
		return nil, err
	}
	var f storedFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	for _, st := range f.Verdicts {
		verdicts[st.Key] = st
	}
	return verdicts, nil
}

// write saves verdicts through a temp file of its own, renamed into place,
// so a reader never sees half a file and two writers never share one.
func (s *Store) write(verdicts map[string]*SnippetState) error {
	f := storedFile{Verdicts: make([]*SnippetState, 0, len(verdicts))}
	for _, v := range verdicts {
		f.Verdicts = append(f.Verdicts, v)
	}
	sort.Slice(f.Verdicts, func(i, j int) bool { return f.Verdicts[i].Key < f.Verdicts[j].Key })
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), "verdicts-*.tmp")
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
