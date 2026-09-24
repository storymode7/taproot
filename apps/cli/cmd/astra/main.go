// astra — AST-aware PR diff reviewer CLI
//
// Usage:
//
//	astra diff [--base <ref> [--head <ref>] | --ref <ref>] [--preset top-down|bottom-up|clusters]
//
// Reads the git diff in the current repository, reorders hunks by
// call-graph depth, and presents an interactive review TUI.
// Zero network calls. All parsing is local.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/astra-review/astra/apps/cli/internal/diff"
	"github.com/astra-review/astra/apps/cli/internal/state"
	"github.com/astra-review/astra/apps/cli/internal/tui"
	"github.com/astra-review/astra/core"
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
  --ref <ref>          Diff the working tree against <ref> exactly as it is
                       now (git diff <ref>). Includes uncommitted edits.
  --preset <name>      Reading order for --no-tty output (default: top-down)
                         top-down   Entry-point first, callees after (tree pre-order)
                         bottom-up  Leaves first, entry-point last (tree post-order)
                         clusters   Same as top-down (clustering is built in)
  --no-auto-advance    Stay on the current snippet after accept/reject/reviewed
                       (default: auto-advance to next snippet, like git add -p)
  --apply              Stage accepted hunks to the git index on quit (git add -p style).
                       Accepted snippets → git apply --cached. Rejected → skipped (left
                       in working tree). Cannot be combined with --ref.
  --no-tty             Print diff to stdout without interactive UI (plain
                       text when piped or when NO_COLOR is set)
  --no-delta           Skip delta; use bat or built-in highlighting instead.
                       Delta is used automatically when installed.
  --no-delta-config    Ignore ~/.gitconfig delta settings and use Astra's
                       GitHub-style theme (the default unless your gitconfig
                       sets delta's look).

ENVIRONMENT
  ASTRA_THEME          dark | light  (default: from COLORFGBG, else dark)
  ASTRA_DELTA_THEME    Syntax theme for delta, e.g. "OneHalfDark"
  VISUAL / EDITOR      Editor for the o key (default: vi)
  --help               Show this help

KEYBOARD SHORTCUTS (interactive mode)
  The main view is a call tree of the changed code: children are the
  functions/types the focused snippet calls; roots are entry points.

  j / ↓   Move down          y / a   Accept        ] / [  Next/prev pending
  k / ↑   Move up            n / x   Reject        J / K  Scroll diff
  l / →   Expand / drill in  r       Mark reviewed E / C  Expand/collapse all
  h / ←   Collapse / caller  o       Open editor   tab    Toggle auto
                                                   q  ?   Quit, help

  Accept/reject records state locally in .git/astra/state.json.
  It does not stage or apply changes to your working tree.

STATE
  Review state is saved to .git/astra/state.json (gitignored).
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "astra: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	var ref, base, head string
	var presetName = "top-down"
	var noTTY bool
	var noAutoAdvance bool
	var applyMode bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--help", "-h":
			fmt.Print(usage)
			return nil
		case "--ref":
			if i+1 >= len(args) {
				return fmt.Errorf("--ref requires an argument")
			}
			i++
			ref = args[i]
		case "--base", "--head":
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires an argument", args[i])
			}
			if args[i] == "--base" {
				base = args[i+1]
			} else {
				head = args[i+1]
			}
			i++
		case "--preset":
			if i+1 >= len(args) {
				return fmt.Errorf("--preset requires an argument")
			}
			i++
			presetName = args[i]
		case "--no-tty":
			noTTY = true
		case "--no-auto-advance":
			noAutoAdvance = true
		case "--apply":
			applyMode = true
		case "--no-delta":
			tui.NoDelta = true
		case "--use-delta":
			// no-op: delta is now the default when installed
		case "--no-delta-config":
			tui.NoDeltaConfig = true
		case "diff":
			// subcommand, no-op (default action)
		default:
			if strings.HasPrefix(args[i], "--") {
				return fmt.Errorf("unknown flag: %s", args[i])
			}
		}
	}

	preset, err := parsePreset(presetName)
	if err != nil {
		return err
	}

	if ref != "" && base != "" {
		return fmt.Errorf("--ref and --base are exclusive: --base reviews committed work since the fork point, --ref diffs the working tree")
	}
	if head != "" && base == "" {
		return fmt.Errorf("--head needs --base (e.g. --base main --head %s)", head)
	}
	if applyMode && (ref != "" || base != "") {
		return fmt.Errorf("--apply works on the working tree only: staging hunks from a committed diff is not supported")
	}

	// Find git repo root.
	repoRoot, err := findRepoRoot()
	if err != nil {
		return fmt.Errorf("not inside a git repository: %w", err)
	}

	// Open (or create) state store.
	store, err := state.Open(repoRoot)
	if err != nil {
		return fmt.Errorf("open state store: %w", err)
	}

	// Fetch diff.
	adapter := &diff.GitDiffAdapter{RepoRoot: repoRoot, Ref: ref, Base: base, Head: head}
	files, err := adapter.Files()
	if err != nil {
		return fmt.Errorf("read diff: %w", err)
	}
	if len(files) == 0 {
		fmt.Println("No changes found.")
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
	if len(snippets) == 0 {
		fmt.Println("No reviewable snippets found.")
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
		tui.View(ordered, store)
		return nil
	}

	var onQuit func() error
	if applyMode {
		onQuit = func() error {
			var accepted []*core.Snippet
			for _, s := range ordered {
				if st := store.Get(s.ID); st.Status == "accepted" {
					accepted = append(accepted, s)
				}
			}
			patch := buildAcceptedPatch(accepted)
			if patch == "" {
				fmt.Println("No accepted hunks — nothing staged.")
				return nil
			}
			if err := adapter.ApplyCached(patch); err != nil {
				return err
			}
			fmt.Printf("Staged %d hunk(s) via git apply --cached.\n", len(accepted))
			return nil
		}
	}

	runner := tui.NewRunner(forest, store, !noAutoAdvance, applyMode, onQuit)
	runner.RepoRoot = repoRoot
	return runner.Run()
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
		return 0, fmt.Errorf("unknown preset %q (want: top-down, bottom-up, clusters)", name)
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
