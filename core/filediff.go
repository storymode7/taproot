package core

import "sort"

// FileDiff is a single changed file within a PR or working-tree diff.
// Adapters (local git, GitHub session, GitHub public API) produce these;
// the core is pure — it never does I/O of its own.
type FileDiff struct {
	// Path is the file path relative to repo root (new path on renames).
	Path string
	// OldPath is set on renames; equals Path otherwise.
	OldPath string
	// Language is the detected language identifier (e.g. "typescript", "go").
	// Empty string means the language is unsupported / unknown.
	Language string
	// Hunks contains all diff hunks for this file.
	Hunks []Hunk
	// IsBinary is true for binary files (no hunk content).
	IsBinary bool
	// IsNew is true when the file was added in this diff.
	IsNew bool
	// IsDeleted is true when the file was removed in this diff.
	IsDeleted bool
	// OldMode and NewMode are the file's modes as the diff gives them
	// ("100644", "100755"), empty when it gives none.
	OldMode, NewMode string
	// IsConflict marks an unmerged file (a combined "diff --cc" section).
	IsConflict bool
	// Source is the full post-image content of the file, when the adapter
	// can provide it. Entity extraction and call scanning need it; leave
	// nil to fall back to one-snippet-per-hunk.
	Source []byte
	// Entities optionally carries entity spans parsed elsewhere. When set
	// they are used as-is; when nil the core extracts entities from Source
	// with its language-aware heuristics, as it does for every surface.
	Entities []Entity
}

// Hunk is a contiguous block of changes within a file.
type Hunk struct {
	// OldStart and OldLines describe the range in the pre-image.
	OldStart, OldLines int
	// NewStart and NewLines describe the range in the post-image.
	NewStart, NewLines int
	// Lines holds the raw unified-diff lines (each prefixed with ' ', '+', or '-').
	Lines []string
}

// SkippedFile is a changed file the review cannot show as snippets.
type SkippedFile struct {
	Path    string `json:"path"`
	OldPath string `json:"oldPath,omitempty"`
	Reason  string `json:"reason"`
}

// SkippedFiles lists, sorted by path, the files BuildSnippets makes no
// snippet for, each with the reason: "binary", "renamed" (moved, its text
// unchanged), "mode" (only its mode changed), "empty" (an empty file added
// or deleted), "conflict" (unmerged) or "no-diff" (no hunks otherwise).
// OldPath is set for a renamed file.
func SkippedFiles(files []FileDiff) []SkippedFile {
	var out []SkippedFile
	for i := range files {
		f := &files[i]
		r := skipReason(f)
		if r == "" {
			continue
		}
		sf := SkippedFile{Path: f.Path, Reason: r}
		if f.OldPath != f.Path {
			sf.OldPath = f.OldPath
		}
		out = append(out, sf)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// skipReason says why a file makes no snippet, "" when it makes some.
func skipReason(f *FileDiff) string {
	switch {
	case f.IsConflict:
		return "conflict"
	case f.IsBinary:
		return "binary"
	case len(f.Hunks) > 0:
		return ""
	case f.OldPath != "" && f.OldPath != f.Path:
		return "renamed"
	case f.OldMode != "" && f.NewMode != "" && f.OldMode != f.NewMode:
		return "mode"
	case f.IsNew || f.IsDeleted:
		return "empty"
	}
	return "no-diff"
}
