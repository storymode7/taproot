package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"strings"

	"github.com/storymode7/astra/apps/cli/internal/diff"
	"github.com/storymode7/astra/core"
)

// stager stages the snippets of a working-tree review. On each y, x or r
// it writes a changed file's whole index entry: the blob git diff read as
// the file's pre-image, with the fragments of every snippet this session
// accepted in the file put in, each at its exact old line. Whatever the
// order of the keys, the index then holds exactly the accepted changes.
type stager struct {
	git   *diff.GitDiffAdapter
	head  string
	files map[string]*stagedFile // by path
	order []*stagedFile
}

// stagedFile is one changed file of the review and the index entries astra
// expects it to have: the ones it found, or the ones it last wrote.
type stagedFile struct {
	path, oldPath string // oldPath differs for a rename
	isNew         bool   // intent-to-add: the pre-image is empty
	isDeleted     bool
	mode          string          // the mode path is staged with
	base          diff.IndexEntry // the pre-image's entry: oldPath's for a rename
	added         diff.IndexEntry // path's intent-to-add entry, for a new or renamed file
	text          []byte          // base's blob, read on first use
	read          bool
	snippets      []*core.Snippet
	accepted      map[string]bool // IDs of the snippets this session staged
	expect        map[string]diff.IndexEntry
	disk          map[string]string // working-tree fingerprints at load
}

// emptyBlob is the empty blob's id, by the length of object ids.
var emptyBlob = map[int]string{
	40: "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391",
	64: "473a0f4c3be8a93681a267e3b1e9a7dcda1185436fe141f7749120a303721813",
}

// newStager records, for each file with snippets, the index entries the
// diff was read from and what its working-tree files hold.
func newStager(a *diff.GitDiffAdapter, files []*core.FileDiff, pre map[string]diff.IndexEntry, snippets []*core.Snippet) (*stager, error) {
	st := &stager{git: a, head: headSHA(a), files: make(map[string]*stagedFile)}
	for _, s := range snippets {
		sf := st.files[s.FilePath]
		if sf == nil {
			sf = &stagedFile{path: s.FilePath, accepted: make(map[string]bool)}
			st.files[s.FilePath] = sf
		}
		sf.snippets = append(sf.snippets, s)
	}
	var paths []string
	for _, f := range files {
		sf := st.files[f.Path]
		if sf == nil {
			continue
		}
		sf.oldPath, sf.isNew, sf.isDeleted, sf.base = f.OldPath, f.IsNew, f.IsDeleted, pre[f.Path]
		if sf.oldPath == "" {
			sf.oldPath = sf.path
		}
		st.order = append(st.order, sf)
		paths = append(paths, sf.paths()...)
	}
	now, err := a.IndexEntries(paths)
	if err != nil {
		return nil, err
	}
	for _, sf := range st.order {
		sf.mode = sf.base.Mode
		sf.expect = map[string]diff.IndexEntry{sf.oldPath: sf.base}
		if sf.isNew || sf.oldPath != sf.path {
			// The diff names no blob for the new path: it must still be
			// the empty intent-to-add entry, or the index changed since.
			sf.added = now[sf.path]
			if sf.added.SHA != emptyBlob[len(sf.added.SHA)] {
				sf.added = diff.IndexEntry{Mode: "intent-to-add"}
			}
			sf.expect[sf.path] = sf.added
			if sf.isNew {
				sf.mode = sf.added.Mode
			}
		}
		sf.disk = make(map[string]string)
		for _, p := range sf.paths() {
			sf.disk[p] = st.fingerprint(p)
		}
	}
	return st, nil
}

// paths lists the index paths a file's staging writes.
func (sf *stagedFile) paths() []string {
	if sf.oldPath != sf.path {
		return []string{sf.oldPath, sf.path}
	}
	return []string{sf.path}
}

// headSHA is HEAD's commit, "" on a branch with no commits.
func headSHA(a *diff.GitDiffAdapter) string {
	out, err := a.RunGit("rev-parse", "-q", "--verify", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// fingerprint identifies a working-tree file's content, "" when there is
// no such file.
func (st *stager) fingerprint(path string) string {
	b, err := st.git.WorktreeFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return ""
	case err != nil:
		return "unreadable: " + err.Error()
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

// changed names what changed outside astra since the diff was read: HEAD,
// an index entry astra did not write, or a working-tree file. It returns
// "" when the diff on screen is still the one git would show.
func (st *stager) changed() string {
	if headSHA(st.git) != st.head {
		return "HEAD moved"
	}
	var paths []string
	for _, sf := range st.order {
		paths = append(paths, sf.paths()...)
	}
	now, err := st.git.IndexEntries(paths)
	if err != nil {
		return "the index could not be read"
	}
	for _, sf := range st.order {
		for _, p := range sf.paths() {
			if now[p] != sf.expect[p] {
				return "the index entry of " + p + " changed"
			}
		}
	}
	for _, sf := range st.order {
		for _, p := range sf.paths() {
			if st.fingerprint(p) != sf.disk[p] {
				return p + " changed on disk"
			}
		}
	}
	return ""
}

// stage stages s (accept) or takes it back out of the index. It refuses,
// changing nothing, when the file's index entries are not the ones astra
// found or last wrote.
func (st *stager) stage(s *core.Snippet, accept bool) error {
	sf := st.files[s.FilePath]
	if sf == nil {
		return fmt.Errorf("%s has no changes to stage", s.FilePath)
	}
	if sf.accepted[s.ID] == accept {
		return nil
	}
	switch {
	case sf.mode == "160000":
		return fmt.Errorf("%s is a submodule: stage it with git add", s.FilePath)
	case sf.base.SHA == "" && !sf.isNew:
		return fmt.Errorf("git diff named no index blob for %s", s.FilePath)
	}
	next := maps.Clone(sf.accepted)
	if accept {
		next[s.ID] = true
	} else {
		delete(next, s.ID)
	}
	want, err := sf.entries(st.git, next)
	if err != nil {
		return err
	}
	now, err := st.git.IndexEntries(sf.paths())
	if err != nil {
		return err
	}
	for _, p := range sf.paths() {
		if now[p] != sf.expect[p] {
			return fmt.Errorf("%s changed in the index outside astra; restart astra to review it again", p)
		}
	}
	if err := sf.write(st.git, want, len(next) == 0); err != nil {
		return err
	}
	sf.expect, sf.accepted = want, next
	return nil
}

// entries returns the index entries the file must have for the snippets in
// accepted to be staged, writing the blob they need.
func (sf *stagedFile) entries(git *diff.GitDiffAdapter, accepted map[string]bool) (map[string]diff.IndexEntry, error) {
	want := map[string]diff.IndexEntry{sf.oldPath: sf.base}
	if sf.isNew || sf.oldPath != sf.path {
		want[sf.path] = sf.added
	}
	if len(accepted) == 0 {
		return want, nil
	}
	if !sf.read {
		if !sf.isNew {
			text, err := git.Blob(sf.base.SHA)
			if err != nil {
				return nil, err
			}
			sf.text = text
		}
		sf.read = true
	}
	var frags []fragment
	for _, s := range sf.snippets {
		if !accepted[s.ID] {
			continue
		}
		more, err := fragments(s)
		if err != nil {
			return nil, err
		}
		frags = append(frags, more...)
	}
	text, err := applyFragments(sf.text, frags)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", sf.path, err)
	}
	want[sf.oldPath] = diff.IndexEntry{}
	if sf.isDeleted {
		if len(text) > 0 {
			return nil, fmt.Errorf("%s: the deletion leaves %d bytes", sf.path, len(text))
		}
		return want, nil
	}
	sha, err := git.WriteBlob(text)
	if err != nil {
		return nil, err
	}
	want[sf.path] = diff.IndexEntry{Mode: sf.mode, SHA: sha}
	return want, nil
}

// write gives the file's paths the entries in want. With nothing accepted,
// a new path gets its intent-to-add entry back.
func (sf *stagedFile) write(git *diff.GitDiffAdapter, want map[string]diff.IndexEntry, none bool) error {
	ita := none && (sf.isNew || sf.oldPath != sf.path)
	set := make(map[string]diff.IndexEntry)
	for p, e := range want {
		if e == sf.expect[p] || ita && p == sf.path {
			continue
		}
		if e.Mode == "" {
			e.SHA = sf.expect[p].SHA // removing: any id of the right length
		}
		set[p] = e
	}
	if len(set) > 0 {
		if err := git.SetIndex(set); err != nil {
			return err
		}
	}
	if ita {
		return git.IntentToAdd(sf.path)
	}
	return nil
}
