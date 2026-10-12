// Package diff provides adapters for the Source interface.
package diff

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// GitDiffAdapter implements Source by reading the local git working tree.
// It has zero network calls — all data comes from git commands.
type GitDiffAdapter struct {
	// RepoRoot is the absolute path to the git repository root.
	// If empty, the current working directory is used.
	RepoRoot string

	// Ref is the git ref to diff the working tree against (e.g.
	// "origin/main", "HEAD~1"). With Ref, Base and Staged unset, the diff
	// is the index against the working tree, as plain git diff shows it:
	// unstaged changes only.
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
	files, _, err := a.FilesAndPreimages()
	return files, err
}

// IndexEntry is a path's entry in the git index: its mode and object id.
type IndexEntry struct{ Mode, SHA string }

// FilesAndPreimages is Files with the pre-image of each file, by path, as
// the same git diff names it: in a working-tree review, the index entry
// its hunks apply to (a rename's old path's entry; no SHA for a new file).
func (a *GitDiffAdapter) FilesAndPreimages() ([]*FileDiff, map[string]IndexEntry, error) {
	args := diffArgs("--unified=3", "--full-index")
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
		return nil, nil, fmt.Errorf("git diff: %w", err)
	}
	files, pre := parseDiffIndex(raw)
	return files, pre, nil
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
		if out, err := a.WorktreeFile(req.Path); !errors.Is(err, fs.ErrNotExist) {
			return out, err
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

// WorktreeFile reads a working-tree file, by its path in the repository.
func (a *GitDiffAdapter) WorktreeFile(path string) ([]byte, error) {
	if a.RepoRoot != "" {
		path = filepath.Join(a.RepoRoot, path)
	}
	return readWorktreeFile(path)
}

// readWorktreeFile reads a file as git sees it: a symlink is the path it
// holds, never what it points to (a device or a pipe there could exhaust
// memory or block), and only a regular file has content.
func readWorktreeFile(path string) ([]byte, error) {
	fi, err := os.Lstat(path)
	switch {
	case err != nil:
		return nil, err
	case fi.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(path)
		return []byte(target), err
	case !fi.Mode().IsRegular():
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	return os.ReadFile(path)
}

func (a *GitDiffAdapter) headRef() string {
	if a.Head != "" {
		return a.Head
	}
	return "HEAD"
}

// IndexEntries returns the index entries of paths. A path with no entry is
// missing from the map; an unmerged path has the Mode "unmerged".
func (a *GitDiffAdapter) IndexEntries(paths []string) (map[string]IndexEntry, error) {
	entries := make(map[string]IndexEntry, len(paths))
	if len(paths) == 0 {
		return entries, nil
	}
	want := make(map[string]bool, len(paths))
	for _, p := range paths {
		want[p] = true
	}
	out, err := a.git(append([]string{"--literal-pathspecs", "ls-files", "--stage", "-z", "--"}, paths...)...)
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	for _, rec := range strings.Split(strings.TrimSuffix(out, "\x00"), "\x00") {
		// <mode> SP <object> SP <stage> TAB <path>
		info, path, ok := strings.Cut(rec, "\t")
		f := strings.Fields(info)
		if !ok || len(f) != 3 || !want[path] {
			continue
		}
		if f[2] != "0" {
			entries[path] = IndexEntry{Mode: "unmerged"}
			continue
		}
		entries[path] = IndexEntry{Mode: f[0], SHA: f[1]}
	}
	return entries, nil
}

// Blob returns the content of the blob sha, as stored.
func (a *GitDiffAdapter) Blob(sha string) ([]byte, error) {
	out, err := a.git("cat-file", "blob", sha)
	if err != nil {
		return nil, fmt.Errorf("git cat-file blob %s: %w", sha, err)
	}
	return []byte(out), nil
}

// WriteBlob stores content as a blob, exactly as given: it is the text the
// index will hold, so no clean filter or line-ending conversion runs.
func (a *GitDiffAdapter) WriteBlob(content []byte) (string, error) {
	out, err := a.gitIn(content, "hash-object", "-w", "--no-filters", "--stdin")
	if err != nil {
		return "", fmt.Errorf("git hash-object: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// SetIndex writes index entries in one git update-index, so they change
// together or not at all. An entry with no Mode removes its path; its SHA
// must still be an object id of the repository's length.
func (a *GitDiffAdapter) SetIndex(entries map[string]IndexEntry) error {
	paths := make([]string, 0, len(entries))
	for p := range entries {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var in bytes.Buffer
	for _, p := range paths {
		e := entries[p]
		if e.Mode == "" {
			fmt.Fprintf(&in, "0 %s 0\t%s\x00", strings.Repeat("0", len(e.SHA)), p)
			continue
		}
		fmt.Fprintf(&in, "%s %s 0\t%s\x00", e.Mode, e.SHA, p)
	}
	if _, err := a.gitIn(in.Bytes(), "update-index", "-z", "--index-info"); err != nil {
		return fmt.Errorf("git update-index: %w", err)
	}
	return nil
}

// IntentToAdd gives path an intent-to-add entry (git add -N), in place of
// any entry it has.
func (a *GitDiffAdapter) IntentToAdd(path string) error {
	if _, err := a.git("update-index", "--force-remove", "--", path); err != nil {
		return fmt.Errorf("git update-index: %w", err)
	}
	// --force: a file the user added with -N stays added if it is ignored.
	if _, err := a.git("--literal-pathspecs", "add", "--intent-to-add", "--force", "--", path); err != nil {
		return fmt.Errorf("git add -N: %w", err)
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
	return a.gitIn(nil, args...)
}

// gitIn is git with stdin.
func (a *GitDiffAdapter) gitIn(stdin []byte, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if a.RepoRoot != "" {
		cmd.Dir = a.RepoRoot
	}
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// ---------------------------------------------------------------------------
// Unified diff parser
// ---------------------------------------------------------------------------

var reHunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

func parseDiff(raw string) []*FileDiff {
	files, _ := parseDiffIndex(raw)
	return files
}

// parseDiffIndex parses a diff and the pre-image blob that each file's
// "index <old>..<new> [<mode>]" line names, by path.
func parseDiffIndex(raw string) ([]*FileDiff, map[string]IndexEntry) {
	var files []*FileDiff
	pre := make(map[string]IndexEntry)
	var cur *FileDiff
	var curHunk *Hunk
	var preSHA, preMode string
	flush := func() {
		if cur == nil {
			return
		}
		if curHunk != nil {
			cur.Hunks = append(cur.Hunks, *curHunk)
			curHunk = nil
		}
		cur.Language = detectLanguage(cur.Path)
		if preMode == "" {
			preMode = cur.OldMode
		}
		if strings.Trim(preSHA, "0") != "" && !cur.IsNew {
			pre[cur.Path] = IndexEntry{Mode: preMode, SHA: preSHA}
		}
		preSHA, preMode = "", ""
		files = append(files, cur)
		cur = nil
	}

	// Split on "\n" alone: lines have no length limit and keep the "\r" of
	// a CRLF file, which the index blob has too.
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
		case strings.HasPrefix(line, "index "):
			ids, mode, _ := strings.Cut(line[len("index "):], " ")
			preSHA, _, _ = strings.Cut(ids, "..")
			preMode = mode
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

	return files, pre
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
