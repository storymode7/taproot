// Package diff provides adapters for the Source interface.
package diff

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// GitDiffAdapter implements Source by reading the local git working tree.
// It has zero network calls — all data comes from git commands.
type GitDiffAdapter struct {
	// RepoRoot is the absolute path to the git repository root.
	// If empty, the current working directory is used.
	RepoRoot string

	// Ref is the git ref to diff against (e.g. "origin/main", "HEAD~1").
	// If empty, uses the working tree (unstaged + staged changes).
	Ref string

	// Base, when set, reviews committed work the way a PR shows it: the
	// diff from merge-base(Base, Head) to Head (git diff Base...Head), with
	// post-images read from Head rather than the working tree. Head
	// defaults to HEAD. Mutually exclusive with Ref.
	Base string
	Head string

	// Staged reviews what is staged for commit (git diff --cached), with
	// post-images read from the index. Mutually exclusive with Ref and Base.
	Staged bool

	// meta is lazily populated on first call to Meta().
	meta *PRMeta
}

var _ Source = (*GitDiffAdapter)(nil)

// Meta returns synthetic PR metadata derived from the local git state.
func (a *GitDiffAdapter) Meta() (*PRMeta, error) {
	if a.meta != nil {
		return a.meta, nil
	}
	headSHA, err := a.git("rev-parse", a.headRef())
	if err != nil {
		return nil, fmt.Errorf("git rev-parse %s: %w", a.headRef(), err)
	}
	baseSHA := ""
	switch {
	case a.Base != "":
		baseSHA, _ = a.git("merge-base", a.Base, a.headRef())
	case a.Ref != "":
		baseSHA, _ = a.git("rev-parse", a.Ref)
	}
	remote, _ := a.git("remote", "get-url", "origin")
	repoName := strings.TrimSuffix(filepath.Base(strings.TrimSpace(remote)), ".git")

	a.meta = &PRMeta{
		Owner:   "local",
		Repo:    strings.TrimSpace(repoName),
		Number:  0,
		Title:   "(local diff)",
		HeadSHA: strings.TrimSpace(headSHA),
		BaseSHA: strings.TrimSpace(baseSHA),
	}
	return a.meta, nil
}

// Files returns all changed files by running git diff.
func (a *GitDiffAdapter) Files() ([]*FileDiff, error) {
	args := []string{"diff", "--unified=3", "--no-color"}
	switch {
	case a.Staged:
		args = append(args, "--cached")
	case a.Base != "":
		args = append(args, a.Base+"..."+a.headRef())
	case a.Ref != "":
		args = append(args, a.Ref)
	}
	raw, err := a.git(args...)
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	return parseDiff(raw), nil
}

// FileBlob returns the full contents of a file at a given commit SHA.
// With an empty SHA the post-image is the working tree (both plain
// `git diff` and `git diff <ref>` diff against it), so the working-tree
// file is read first; HEAD is only a fallback for missing files. In Base
// mode the post-image is Head.
func (a *GitDiffAdapter) FileBlob(req BlobRequest) ([]byte, error) {
	if req.SHA == "" && a.Staged {
		out, err := a.git("show", ":"+req.Path)
		if err != nil {
			return nil, fmt.Errorf("git show :%s: %w", req.Path, err)
		}
		return []byte(out), nil
	}
	if req.SHA == "" && a.Base != "" {
		req.SHA = a.headRef()
	}
	if req.SHA == "" {
		path := req.Path
		if a.RepoRoot != "" {
			path = filepath.Join(a.RepoRoot, req.Path)
		}
		if out, err := os.ReadFile(path); err == nil {
			return out, nil
		}
		out, err := a.git("show", fmt.Sprintf("HEAD:%s", req.Path))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", req.Path, err)
		}
		return []byte(out), nil
	}
	out, err := a.git("show", fmt.Sprintf("%s:%s", req.SHA, req.Path))
	if err != nil {
		return nil, fmt.Errorf("git show %s:%s: %w", req.SHA, req.Path, err)
	}
	return []byte(out), nil
}

func (a *GitDiffAdapter) headRef() string {
	if a.Head != "" {
		return a.Head
	}
	return "HEAD"
}

// ApplyCached applies the given unified-diff patch to the git index.
// Equivalent to: echo "$patch" | git apply --cached --unidiff-zero [-R]
// Hunks cut at entity boundaries end on their last changed line, with no
// trailing context; without --unidiff-zero git would anchor them to EOF.
// It is idempotent: a patch already in the index (or, with reverse, already
// absent from it) is left alone, so review state recorded before staging
// existed, or an index the user touched by hand, never makes a keypress fail.
func (a *GitDiffAdapter) ApplyCached(patch string, reverse bool) error {
	if a.gitApplyCached(patch, !reverse, true) == nil {
		return nil
	}
	return a.gitApplyCached(patch, reverse, false)
}

func (a *GitDiffAdapter) gitApplyCached(patch string, reverse, check bool) error {
	args := []string{"apply", "--cached", "--unidiff-zero"}
	if reverse {
		args = append(args, "-R")
	}
	if check {
		args = append(args, "--check")
	}
	cmd := exec.Command("git", args...)
	if a.RepoRoot != "" {
		cmd.Dir = a.RepoRoot
	}
	cmd.Stdin = strings.NewReader(patch)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git apply --cached: %w: %s", err, stderr.String())
	}
	return nil
}

// PostReviewComment is not supported for local diffs.
func (a *GitDiffAdapter) PostReviewComment(_ ReviewComment) (int64, error) {
	return 0, ErrReadOnly
}

// MarkFileViewed is not supported for local diffs.
func (a *GitDiffAdapter) MarkFileViewed(_ string, _ bool) error {
	return ErrReadOnly
}

// RunGit runs a git command and returns stdout. Exported for use by the CLI.
func (a *GitDiffAdapter) RunGit(args ...string) (string, error) {
	return a.git(args...)
}

// git runs a git command in RepoRoot and returns stdout as a string.
func (a *GitDiffAdapter) git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if a.RepoRoot != "" {
		cmd.Dir = a.RepoRoot
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, stderr.String())
	}
	return stdout.String(), nil
}

// ---------------------------------------------------------------------------
// Unified diff parser
// ---------------------------------------------------------------------------

var (
	reDiffHeader = regexp.MustCompile(`^diff --git [^/]+/(.+) [^/]+/(.+)$`)
	reHunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)
)

func parseDiff(raw string) []*FileDiff {
	var files []*FileDiff
	var cur *FileDiff
	var curHunk *Hunk

	// Split on "\n" alone: lines have no length limit and keep the "\r" of
	// a CRLF file, which git apply needs to match the index.
	for _, line := range strings.Split(strings.TrimSuffix(raw, "\n"), "\n") {
		if m := reDiffHeader.FindStringSubmatch(line); m != nil {
			if curHunk != nil && cur != nil {
				cur.Hunks = append(cur.Hunks, *curHunk)
				curHunk = nil
			}
			if cur != nil {
				files = append(files, cur)
			}
			cur = &FileDiff{OldPath: m[1], Path: m[2]}
			cur.Language = detectLanguage(cur.Path)
			continue
		}

		if cur == nil {
			continue
		}

		switch {
		case strings.HasPrefix(line, "new file"):
			cur.IsNew = true
		case strings.HasPrefix(line, "deleted file"):
			cur.IsDeleted = true
		case strings.HasPrefix(line, "Binary files"):
			cur.IsBinary = true

		case reHunkHeader.MatchString(line):
			if curHunk != nil {
				cur.Hunks = append(cur.Hunks, *curHunk)
			}
			m := reHunkHeader.FindStringSubmatch(line)
			oldStart, _ := strconv.Atoi(m[1])
			oldLines := 1
			if m[2] != "" {
				oldLines, _ = strconv.Atoi(m[2])
			}
			newStart, _ := strconv.Atoi(m[3])
			newLines := 1
			if m[4] != "" {
				newLines, _ = strconv.Atoi(m[4])
			}
			curHunk = &Hunk{
				OldStart: oldStart,
				OldLines: oldLines,
				NewStart: newStart,
				NewLines: newLines,
				Lines:    []string{line},
			}

		default:
			if curHunk != nil {
				curHunk.Lines = append(curHunk.Lines, line)
			}
		}
	}

	if curHunk != nil && cur != nil {
		cur.Hunks = append(cur.Hunks, *curHunk)
	}
	if cur != nil {
		files = append(files, cur)
	}

	return files
}

func detectLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".js", ".mjs", ".cjs", ".jsx":
		return "javascript"
	case ".ts", ".tsx", ".mts":
		return "typescript"
	case ".py":
		return "python"
	case ".go":
		return "go"
	case ".java":
		return "java"
	case ".rs":
		return "rust"
	}
	return ""
}
