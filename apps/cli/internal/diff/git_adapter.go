// Package diff provides adapters for the Source interface.
package diff

import (
	"bufio"
	"bytes"
	"fmt"
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

	// meta is lazily populated on first call to Meta().
	meta *PRMeta
}

var _ Source = (*GitDiffAdapter)(nil)

// Meta returns synthetic PR metadata derived from the local git state.
func (a *GitDiffAdapter) Meta() (*PRMeta, error) {
	if a.meta != nil {
		return a.meta, nil
	}
	headSHA, err := a.git("rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	baseSHA := ""
	if a.Ref != "" {
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
	if a.Ref != "" {
		args = append(args, a.Ref)
	}
	raw, err := a.git(args...)
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	return parseDiff(raw), nil
}

// FileBlob returns the full contents of a file at a given commit SHA.
func (a *GitDiffAdapter) FileBlob(req BlobRequest) ([]byte, error) {
	if req.SHA == "" {
		// Fall back to working tree.
		path := req.Path
		if a.RepoRoot != "" {
			path = filepath.Join(a.RepoRoot, req.Path)
		}
		out, err := a.git("show", fmt.Sprintf("HEAD:%s", req.Path))
		if err != nil {
			// Try working tree file directly.
			return exec.Command("cat", path).Output()
		}
		return []byte(out), nil
	}
	out, err := a.git("show", fmt.Sprintf("%s:%s", req.SHA, req.Path))
	if err != nil {
		return nil, fmt.Errorf("git show %s:%s: %w", req.SHA, req.Path, err)
	}
	return []byte(out), nil
}

// PostReviewComment is not supported for local diffs.
func (a *GitDiffAdapter) PostReviewComment(_ ReviewComment) (int64, error) {
	return 0, ErrReadOnly
}

// MarkFileViewed is not supported for local diffs.
func (a *GitDiffAdapter) MarkFileViewed(_ string, _ bool) error {
	return ErrReadOnly
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
	reDiffHeader = regexp.MustCompile(`^diff --git a/(.+) b/(.+)$`)
	reHunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)
)

func parseDiff(raw string) []*FileDiff {
	var files []*FileDiff
	var cur *FileDiff
	var curHunk *Hunk

	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()

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
