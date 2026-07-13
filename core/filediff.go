package core

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
	// Source is the full post-image content of the file, when the adapter
	// can provide it. Entity extraction and call scanning need it; leave
	// nil to fall back to one-snippet-per-hunk.
	Source []byte
	// Entities optionally carries pre-parsed entity spans (e.g. from
	// web-tree-sitter in the browser surfaces). When set they are used
	// as-is; when nil the core extracts entities from Source with its
	// language-aware heuristics.
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
