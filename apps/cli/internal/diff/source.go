// Package diff defines the DiffSource interface and the data types that all
// adapters (local git, GitHub session, GitHub public API) must produce.
package diff

import "github.com/storymode7/taproot/core"

// FileDiff and Hunk are defined in the pure core package (which the WASM
// build for the browser surfaces also uses); aliased here so adapters keep
// their existing API.
type (
	FileDiff = core.FileDiff
	Hunk     = core.Hunk
)

// PRMeta holds top-level metadata about a pull request.
type PRMeta struct {
	Owner  string
	Repo   string
	Number int
	Title  string
	// HeadSHA is the commit SHA at the head of the PR branch.
	HeadSHA string
	// BaseSHA is the commit SHA of the base (merge target).
	BaseSHA string
}

// BlobRequest identifies a specific file revision for fetching full content.
type BlobRequest struct {
	Path string
	SHA  string // commit or blob SHA
}

// ReviewComment is a comment to post on a specific line of a file.
type ReviewComment struct {
	Path string
	Line int // line number in the post-image
	Body string
	// ReplyToID is set when the comment is a reply; 0 for top-level.
	ReplyToID int64
}

// Source is the interface every diff adapter must implement.
// The three implementations are:
//   - GitDiffAdapter (CLI, reads local git diff — zero network)
//   - GitHubSessionAdapter (extension, uses existing GH session)
//   - GitHubPublicAPIAdapter (PWA, unauthenticated api.github.com)
type Source interface {
	// Meta returns top-level PR metadata.
	Meta() (*PRMeta, error)

	// Files returns all changed files with their diff hunks.
	Files() ([]*FileDiff, error)

	// FileBlob returns the full content of a file at a given commit SHA.
	// The core finds the entities and calls around hunks in it.
	FileBlob(req BlobRequest) ([]byte, error)

	// PostReviewComment posts an inline comment on the PR.
	// Returns the ID of the created comment.
	// May return ErrReadOnly if the adapter does not support writes
	// (e.g. GitHubPublicAPIAdapter without auth).
	PostReviewComment(comment ReviewComment) (int64, error)

	// MarkFileViewed marks a file as "Viewed" in GitHub's native UI.
	// May return ErrReadOnly for read-only adapters.
	MarkFileViewed(path string, viewed bool) error
}

// ErrReadOnly is returned by write methods of adapters that are read-only.
var ErrReadOnly = readOnlyErr("this adapter is read-only")

type readOnlyErr string

func (e readOnlyErr) Error() string { return string(e) }
