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
	args := diffArgs("--unified=3")
	switch {
	case a.Staged:
		args = append(args, "--cached")
	case a.Base != "":
		args = append(args, a.Base+"..."+a.headRef())
	case a.Ref != "":
		args = append(args, a.Ref)
	}
	raw, err := a.git(append(args, "--")...)
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	return parseDiff(raw), nil
}

// diffArgs starts a git diff whose output parseDiff can read whatever the
// user's config says: no external diff, textconv or color, a/ and b/
// prefixes, one line per submodule, renames found but not copies, paths
// from the top of the repository, and every path with a byte outside
// printable ASCII quoted, so it decodes exactly.
func diffArgs(opts ...string) []string {
	return append([]string{"-c", "core.quotePath=true", "diff", "--no-color", "--no-ext-diff", "--no-textconv",
		"--src-prefix=a/", "--dst-prefix=b/", "--submodule=short", "--no-relative", "-M"}, opts...)
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
	// The user's apply.whitespace and apply.ignoreWhitespace would refuse
	// or rewrite lines with trailing whitespace, or match lines that
	// differ in it.
	args := []string{"apply", "--cached", "--unidiff-zero", "--whitespace=nowarn", "--no-ignore-whitespace"}
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

var reHunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

func parseDiff(raw string) []*FileDiff {
	var files []*FileDiff
	var cur *FileDiff
	var curHunk *Hunk
	flush := func() {
		if cur == nil {
			return
		}
		if curHunk != nil {
			cur.Hunks = append(cur.Hunks, *curHunk)
			curHunk = nil
		}
		cur.Language = detectLanguage(cur.Path)
		files = append(files, cur)
		cur = nil
	}

	// Split on "\n" alone: lines have no length limit and keep the "\r" of
	// a CRLF file, which git apply needs to match the index.
	for _, line := range strings.Split(strings.TrimSuffix(raw, "\n"), "\n") {
		if rest, ok := strings.CutPrefix(line, "diff --git "); ok {
			flush()
			oldPath, path := headerPaths(rest)
			cur = &FileDiff{OldPath: oldPath, Path: path}
			continue
		}
		if path, ok := conflictPath(line); ok {
			flush()
			cur = &FileDiff{OldPath: path, Path: path, IsConflict: true}
			continue
		}

		if cur == nil || cur.IsConflict {
			continue
		}

		switch {
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
		case curHunk != nil:
			curHunk.Lines = append(curHunk.Lines, line)

		// The extended header, before the first hunk. Its lines name the
		// paths exactly, where the diff --git line can be ambiguous.
		case strings.HasPrefix(line, "old mode "):
			cur.OldMode = line[len("old mode "):]
		case strings.HasPrefix(line, "new mode "):
			cur.NewMode = line[len("new mode "):]
		case strings.HasPrefix(line, "new file mode "):
			cur.IsNew, cur.NewMode = true, line[len("new file mode "):]
		case strings.HasPrefix(line, "deleted file mode "):
			cur.IsDeleted, cur.OldMode = true, line[len("deleted file mode "):]
		case strings.HasPrefix(line, "Binary files"):
			cur.IsBinary = true
		case strings.HasPrefix(line, "rename from "):
			cur.OldPath = gitPath(line[len("rename from "):])
		case strings.HasPrefix(line, "rename to "):
			cur.Path = gitPath(line[len("rename to "):])
		case strings.HasPrefix(line, "--- "):
			if p := gitPath(line[len("--- "):]); p != "/dev/null" {
				cur.OldPath = strings.TrimPrefix(p, "a/")
			}
		case strings.HasPrefix(line, "+++ "):
			if p := gitPath(line[len("+++ "):]); p != "/dev/null" {
				cur.Path = strings.TrimPrefix(p, "b/")
			}
		}
	}
	flush()

	return files
}

// conflictPath reads the path of an unmerged file. git diff prints its
// conflict as a combined diff, or only its name when it is unmerged in a
// way a combined diff cannot show (and always with --cached); that name
// is printed as is, never quoted.
func conflictPath(line string) (string, bool) {
	if rest, ok := strings.CutPrefix(line, "diff --cc "); ok {
		return gitPath(rest), true
	}
	if rest, ok := strings.CutPrefix(line, "diff --combined "); ok {
		return gitPath(rest), true
	}
	return strings.CutPrefix(line, "* Unmerged path ")
}

// headerPaths reads the paths of a "diff --git a/<old> b/<new>" line. git
// quotes a path that holds special characters. An unquoted path may hold
// spaces, so the line splits where both halves name the same file, as git
// apply does; a rename's "rename from/to" lines then give its paths.
func headerPaths(s string) (oldPath, newPath string) {
	n := len(s)
	if q, err := strconv.QuotedPrefix(s); err == nil {
		oldPath, newPath = gitPath(q), gitPath(strings.TrimPrefix(s[len(q):], " "))
	} else if i := strings.IndexByte(s, '"'); i > 0 {
		oldPath, newPath = s[:i-1], gitPath(s[i:])
	} else if n >= 5 && n%2 == 1 && s[n/2] == ' ' && s[2:n/2] == s[n/2+3:] {
		oldPath, newPath = s[:n/2], s[n/2+1:]
	} else if i := strings.Index(s, " b/"); i >= 0 {
		oldPath, newPath = s[:i], s[i+1:]
	} else {
		oldPath, newPath = s, s
	}
	return strings.TrimPrefix(oldPath, "a/"), strings.TrimPrefix(newPath, "b/")
}

// gitPath decodes a path as git prints it: C-quoted when it holds special
// characters, and followed by a tab on a ---/+++ line when it holds a space.
func gitPath(s string) string {
	s = strings.TrimSuffix(s, "\t")
	if strings.HasPrefix(s, `"`) {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
	}
	return s
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
