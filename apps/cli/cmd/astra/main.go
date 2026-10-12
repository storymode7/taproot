// astra — AST-aware PR diff reviewer CLI
//
// Usage:
//
//	astra diff [--base <ref> [--head <ref>] | --ref <ref> | --staged] [--preset top-down|bottom-up|clusters]
//
// Reads the git diff in the current repository, reorders hunks by
// call-graph depth, and presents an interactive review TUI.
// Zero network calls. All parsing is local.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"

	"github.com/storymode7/astra/apps/cli/internal/diff"
	"github.com/storymode7/astra/apps/cli/internal/state"
	"github.com/storymode7/astra/apps/cli/internal/tui"
	"github.com/storymode7/astra/core"
)

const usage = `astra — AST-aware PR diff reviewer

USAGE
  astra diff [options]

OPTIONS
  --base <ref>         Review a branch the way its PR shows it: changes since
                       it forked from <ref> (git diff <ref>...HEAD). Changes
                       made on <ref> after the fork are left out.
                       Examples: main  origin/main
  --head <ref>         With --base: review this branch or commit instead of
                       HEAD, without checking it out.
                       Examples: --base main --head feature
                                 --base abc123~1 --head abc123  (one commit)
  --staged             Review what is staged for commit (git diff --cached).
  --ref <ref>          Diff the working tree against <ref> exactly as it is
                       now (git diff <ref>). Includes uncommitted edits.
  --preset <name>      Reading order for --no-tty output (default: top-down)
                         top-down   Entry-point first, callees after (tree pre-order)
                         bottom-up  Leaves first, entry-point last (tree post-order)
                         clusters   Same as top-down (clustering is built in)
  --no-auto-advance    Stay on the current snippet after accept/reject/reviewed
                       (default: auto-advance to next snippet, like git add -p)
  --no-apply           Record verdicts only; leave the git index alone.
                       By default y stages the snippet (git apply --cached) and
                       x/r on an accepted snippet unstages it (-R), like
                       git add -p. Staging is off anyway with --staged, --ref
                       or --base, since those don't review the working tree.
  --no-tty             Print diff to stdout without interactive UI (plain
                       text when piped or when NO_COLOR is set)
  --no-delta           Skip delta; use bat or built-in highlighting instead.
                       Delta is used automatically when installed.
  --no-delta-config    Ignore ~/.gitconfig delta settings and use Astra's
                       GitHub-style theme (the default unless your gitconfig
                       sets delta's look).
  -V, --version        Show the version

ENVIRONMENT
  ASTRA_THEME          dark | light  (default: from COLORFGBG, else dark)
  ASTRA_DELTA_THEME    Syntax theme for delta, e.g. "OneHalfDark"
  VISUAL / EDITOR      Editor for the o key (default: vi)
  --help               Show this help

KEYBOARD SHORTCUTS (interactive mode)
  The main view is a call tree of the changed code: children are the
  functions/types the focused snippet calls; roots are entry points.

  J / ↓   Move down          y / a   Accept        ] / [  Next/prev pending
  K / ↑   Move up            n / x   Reject        j / k  Scroll / next
  l / →   Expand / drill in  r       Mark reviewed E / C  Expand/collapse all
  h / ←   Collapse / caller  o       Open editor   tab    Toggle auto
                                                   q  ?   Quit, help

  Accept/reject records state locally under .git/astra/reviews/.
  It does not stage or apply changes to your working tree.

STATE
  Review state is saved per diff under .git/astra/reviews/: one small
  file per commit range (or working tree at its HEAD) you have marked.
`

// version is set by a release build: go build -ldflags "-X main.version=v1.2.3".
var version = "dev"

// buildVersion is version, or when no release build set it, the module
// version Go recorded: a tag for go install, a pseudo-version for go build.
func buildVersion() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "astra: %v\n", err)
		if errors.As(err, new(usageError)) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

// usageError is a mistake on the command line; astra exits 2 for it.
type usageError string

func (e usageError) Error() string { return string(e) }

func usagef(format string, args ...any) error {
	return usageError(fmt.Sprintf(format, args...))
}

func run(args []string) error {
	var ref, base, head string
	var presetName = "top-down"
	var noTTY bool
	var staged bool
	var noAutoAdvance bool
	var noApply bool

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch name, value, inline := strings.Cut(arg, "="); name {
		case "--ref", "--base", "--head", "--preset":
			if !inline && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				value = args[i]
			}
			if value == "" || strings.HasPrefix(value, "-") {
				return usagef("%s requires an argument", name)
			}
			switch name {
			case "--ref":
				ref = value
			case "--base":
				base = value
			case "--head":
				head = value
			default:
				presetName = value
			}
			continue
		}
		switch arg {
		case "--help", "-h", "help":
			fmt.Print(usage)
			return nil
		case "--version", "-V", "version":
			fmt.Println("astra", buildVersion())
			return nil
		case "--no-tty":
			noTTY = true
		case "--staged", "--cached":
			staged = true
		case "--no-auto-advance":
			noAutoAdvance = true
		case "--no-apply":
			noApply = true
		case "--apply":
			// no-op: staging on accept is now the default
		case "--no-delta":
			tui.NoDelta = true
		case "--use-delta":
			// no-op: delta is now the default when installed
		case "--no-delta-config":
			tui.NoDeltaConfig = true
		case "diff":
			// the only subcommand
			if i > 0 {
				return usagef("unexpected argument %q", arg)
			}
		default:
			if strings.HasPrefix(arg, "-") {
				return usagef("unknown flag: %s (see astra --help)", arg)
			}
			return usagef("unexpected argument %q; to review a branch or commit, use\n"+
				"  --base <ref>   its changes since it forked, as a PR shows them\n"+
				"  --ref <ref>    the working tree against it, like git diff <ref>", arg)
		}
	}
	tui.DetectHighlighter()

	preset, err := parsePreset(presetName)
	if err != nil {
		return err
	}

	if staged && (ref != "" || base != "") {
		return usagef("--staged can't be combined with --ref or --base")
	}
	if ref != "" && base != "" {
		return usagef("--ref and --base are exclusive: --base reviews committed work since the fork point, --ref diffs the working tree")
	}
	if head != "" && base == "" {
		return usagef("--head needs --base (e.g. --base main --head %s)", head)
	}
	applyMode := !noApply && !staged && ref == "" && base == ""

	// Find git repo root.
	repoRoot, err := findRepoRoot()
	if errors.Is(err, exec.ErrNotFound) {
		return errors.New("git is not installed or not on PATH")
	}
	if err != nil {
		return fmt.Errorf("not inside a git repository: %w", err)
	}

	adapter := &diff.GitDiffAdapter{RepoRoot: repoRoot, Ref: ref, Base: base, Head: head, Staged: staged}
	if err := checkRefs(adapter); err != nil {
		return err
	}

	gitDir, err := adapter.RunGit("rev-parse", "--absolute-git-dir")
	if err != nil {
		return fmt.Errorf("find git dir: %w", err)
	}
	store, err := state.Open(strings.TrimSpace(gitDir), reviewScope(adapter))
	if err != nil {
		return fmt.Errorf("open state store: %w", err)
	}

	// Fetch diff.
	files, err := adapter.Files()
	if err != nil {
		return fmt.Errorf("read diff: %w", err)
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "No changes found."+emptyDiffHint(adapter))
		return nil
	}

	// Fill in post-image source so the core can extract entity boundaries
	// and call sites (the core is pure — all I/O happens here).
	for _, f := range files {
		if f.Language == "" || f.IsDeleted || f.IsBinary {
			continue
		}
		if src, err := adapter.FileBlob(diff.BlobRequest{Path: f.Path}); err == nil {
			f.Source = src
		}
	}

	// Build snippets using entity-aware splitting.
	snippets := core.BuildSnippets(files)
	skipped := skippedFiles(files)
	if len(snippets) == 0 {
		if len(skipped) > 0 {
			fmt.Println(tui.NotShownLine(skipped))
		} else {
			fmt.Println("No reviewable snippets found.")
		}
		return nil
	}

	// Annotate snippets whose callers live in unchanged regions of changed
	// files, so context-free roots can say who calls them.
	core.AnnotateUnchangedCallers(files, snippets)

	// Build symbol graph: call edges between changed entities drive review
	// order; import edges only group snippets into clusters.
	edges := append(core.BuildCallEdges(snippets), core.BuildEdgesFromDiff(snippets)...)

	// Build the review forest and its flattened reading order.
	forest := core.BuildForest(snippets, edges)
	order := core.OrderPre
	if preset == core.PresetBottomUp {
		order = core.OrderPost
	}
	ordered := forest.Flatten(order)

	if noTTY || !isTTY() {
		tui.Plain = !isTTY() || os.Getenv("NO_COLOR") != ""
		if len(skipped) > 0 {
			fmt.Println(tui.NotShownLine(skipped))
		}
		tui.View(ordered, store)
		return nil
	}

	var stage func(s *core.Snippet, reverse bool) error
	if applyMode {
		stage = func(s *core.Snippet, reverse bool) error {
			return adapter.ApplyCached(buildAcceptedPatch([]*core.Snippet{s}), reverse)
		}
	}

	runner := tui.NewRunner(forest, store, !noAutoAdvance, applyMode, stage)
	runner.RepoRoot = repoRoot
	runner.Skipped = skipped
	return runner.Run()
}

// emptyDiffHint explains an empty review of the working tree, which shows
// what git diff shows: neither staged changes nor untracked files.
func emptyDiffHint(a *diff.GitDiffAdapter) string {
	if a.Staged || a.Ref != "" || a.Base != "" {
		return ""
	}
	var hint string
	if out, err := a.RunGit("diff", "--cached", "--name-only", "-z"); err == nil && out != "" {
		hint += "\nStaged changes are not shown: astra diff --staged reviews them."
	}
	if out, err := a.RunGit("ls-files", "--others", "--exclude-standard", "--directory", "--no-empty-directory", "-z"); err == nil && out != "" {
		hint += "\nUntracked files are not reviewed: git add them, then run astra diff --staged."
	}
	return hint
}

// skippedFiles lists the changed files that make no snippet, and why.
func skippedFiles(files []*core.FileDiff) []core.SkippedFile {
	plain := make([]core.FileDiff, len(files))
	for i, f := range files {
		plain[i] = *f
	}
	return core.SkippedFiles(plain)
}

// checkRefs names the flag whose branch or commit git cannot find, where
// git diff would fail with advice about paths.
func checkRefs(a *diff.GitDiffAdapter) error {
	resolves := func(ref string) bool {
		_, err := a.RunGit("rev-parse", "--verify", "--quiet", ref+"^{commit}")
		return err == nil
	}
	if a.Base != "" && a.Head == "" && !resolves("HEAD") {
		return errors.New("--base reviews commits, and this branch has none yet (--staged reviews what is staged)")
	}
	for _, f := range [][2]string{{"--base", a.Base}, {"--head", a.Head}, {"--ref", a.Ref}} {
		if f[1] == "" || resolves(f[1]) {
			continue
		}
		if !resolves("HEAD") {
			return fmt.Errorf("%s %s: no such branch, tag or commit; this branch has no commits yet", f[0], f[1])
		}
		return fmt.Errorf("%s %s: no such branch, tag or commit", f[0], f[1])
	}
	return nil
}

// reviewScope names the diff under review by commit SHAs, so review state
// follows the exact commits rather than branch names that move.
func reviewScope(a *diff.GitDiffAdapter) string {
	sha := func(args ...string) string {
		out, err := a.RunGit(args...)
		if err != nil {
			return "unborn"
		}
		return strings.TrimSpace(out)
	}
	head := sha("rev-parse", "HEAD")
	switch {
	case a.Base != "":
		tip := a.Head
		if tip == "" {
			tip = "HEAD"
		}
		return "commits " + sha("merge-base", a.Base, tip) + ".." + sha("rev-parse", tip+"^{commit}")
	case a.Staged:
		return "staged@" + head
	case a.Ref != "":
		return "worktree vs " + sha("rev-parse", a.Ref+"^{commit}") + "@" + head
	default:
		return "worktree@" + head
	}
}

func parsePreset(name string) (core.Preset, error) {
	switch name {
	case "top-down":
		return core.PresetTopDown, nil
	case "bottom-up":
		return core.PresetBottomUp, nil
	case "clusters":
		return core.PresetClusters, nil
	default:
		return 0, usagef("unknown preset %q (want: top-down, bottom-up, clusters)", name)
	}
}

func findRepoRoot() (string, error) {
	a := &diff.GitDiffAdapter{}
	out, err := a.RunGit("rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
