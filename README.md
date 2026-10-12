# Astra

Astra turns a diff into a call tree of the changed code. You read the entry
point first, then drill down one level at a time into the functions it
calls. A snippet holds the changes to one function, method or type, not a
bare hunk.

Astra comes as three tools that run the same review engine on your machine:

- a CLI for the changes in a local git repository,
- a Chrome extension for pull requests on github.com,
- a static web app for public pull requests.

There is no backend, no OAuth and no telemetry.

**Status:** early development. The interactive terminal UI is tested on
Linux. The macOS builds compile in CI, but the terminal UI has not had a
hands-on test on a Mac yet.

## How it works

1. **Read the diff.** The CLI runs git in your repository. The extension
   reads the pull request page you have open. The web app calls GitHub's
   public API.
2. **Find entities.** For each changed file in a supported language, the
   engine reads the full new source and finds its functions, methods,
   classes and types. It uses rules written for each language, and masks
   strings and comments first, so a brace or quote inside them cannot move
   a boundary. There is no parser and no tree-sitter.
3. **Cut snippets.** The engine splits each hunk by entity, so a snippet
   holds the changes to one function, method or type. Decorators,
   attributes and doc comments go with the declaration below them. Changes
   outside any entity, such as imports, get snippets of their own.
4. **Link calls.** The engine scans each changed entity for calls to the
   other changed entities. In JavaScript and `.tsx` files, a JSX element
   such as `<CartItem />` counts as a call.
5. **Build the review tree.** The roots are changed entities that nothing
   else in the diff calls. Their children are the changed entities they
   call, in call order. An entity with several changed callers appears
   once, and the other callers show a reference to it. Tests come after
   the code they test, and each snippet names the tests that call it. A
   snippet whose callers did not change names them, for example "called by
   unchanged main.go::main".

The engine is the Go package in `core/`. The CLI links it directly. The
extension and the web app run the same code compiled to WebAssembly
(`vendor/core/astra-core.wasm`), and `make parity` checks that both give
the same output on a set of test fixtures.

## Install the CLI

The CLI runs on Linux and macOS, on amd64 and arm64. Windows is not
supported in v1. On Windows, use the Linux build under WSL.

It needs `git`. For syntax highlighting it uses
[delta](https://github.com/dandavison/delta) when it is installed, else
[bat](https://github.com/sharkdp/bat), else its own highlighting.

### Build from source

You need Go 1.26.3 or newer and `make`.

```sh
git clone https://github.com/storymode7/astra
cd astra
make cli
./astra --version
```

Copy `./astra` to a directory on your `PATH`. `make cli` runs
`CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o astra ./apps/cli/cmd/astra`;
run that line yourself if you do not have `make`.

### Prebuilt binaries

When a release is out, the
[Releases page](https://github.com/storymode7/astra/releases) has binaries
named `astra-<version>-<os>-<arch>`, `LICENSE`, `THIRD_PARTY_NOTICES`, the
extension zip and `SHA256SUMS`.
Releases are built by hand with `make dist` and are not signed. For
example, for v0.1.0 on Linux amd64:

```sh
sha256sum -c --ignore-missing SHA256SUMS
chmod +x astra-v0.1.0-linux-amd64
mv astra-v0.1.0-linux-amd64 astra      # then move it to a directory on your PATH
./astra --version
```

On macOS, check with `shasum -a 256 -c --ignore-missing SHA256SUMS`. If
macOS refuses to run a binary that a browser downloaded, remove the
quarantine flag: `xattr -d com.apple.quarantine astra`.

## Usage

```sh
astra diff                                # unstaged changes, as git diff shows them
astra diff --staged                       # what is staged for commit
astra diff --base main                    # this branch, the way its PR shows it
astra diff --base main --head feature     # another branch, without checking it out
astra diff --base abc123~1 --head abc123  # one commit
astra diff --ref main                     # the working tree against main, like git diff main
astra diff --base main | less             # plain text when piped
```

Plain `astra diff` leaves out staged changes and untracked files, as
`git diff` does. To review a new file, mark it with `git add -N` first, or
`git add` it and use `--staged`.

| Option | Meaning (from `astra --help`) |
|---|---|
| `--base <ref>` | Review a branch the way its PR shows it: changes since it forked from `<ref>` (`git diff <ref>...HEAD`). |
| `--head <ref>` | With `--base`: review this branch or commit instead of HEAD, without checking it out. |
| `--staged` | Review what is staged for commit (`git diff --cached`). |
| `--ref <ref>` | Diff the working tree against `<ref>` exactly as it is now (`git diff <ref>`). Includes uncommitted edits. |
| `--preset <name>` | Reading order for `--no-tty` output: `top-down` (default), `bottom-up`, or `clusters` (same as top-down). |
| `--no-auto-advance` | Stay on the current snippet after accept, reject or reviewed. |
| `--no-apply` | Record verdicts only; leave the git index alone. |
| `--no-tty` | Print the review without the interactive UI (plain text when piped or when `NO_COLOR` is set). |
| `--no-delta` | Skip delta; use bat or the built-in highlighting instead. |
| `--no-delta-config` | Ignore `~/.gitconfig` delta settings and use Astra's GitHub-style theme. |
| `-h`, `--help` | Show the help. |
| `-V`, `--version` | Show the version. |

A value can also follow `=`, as in `--base=main`. `--staged`, `--ref` and
`--base` exclude each other, and `--head` needs `--base`.

Environment: `ASTRA_THEME` (`dark` or `light`; the default comes from
`COLORFGBG`, else dark), `ASTRA_DELTA_THEME` (a delta syntax theme) and
`VISUAL` / `EDITOR` (the editor for `o`; the default is vi).

Exit status: 0 on success, 2 for a mistake on the command line (an unknown
flag or preset, a stray argument, a missing value or conflicting flags),
and 1 for any other error.

### Keys

In a terminal, `astra diff` opens the review: the call tree on top and the
focused snippet's diff below it.

| Key | Action |
|---|---|
| `J` / `↓`, `K` / `↑` | Move down / up the tree |
| `l` / `→` / `Enter` | Expand a node; if it is expanded, go to its first callee; on a `→` reference, jump to where the snippet is shown |
| `h` / `←` | Collapse a node; if it is collapsed, jump to its caller |
| `y` / `a` | Accept the snippet |
| `x` | Reject the snippet |
| `r` | Mark the snippet as reviewed (no judgement) |
| `]` / `[` | Next / previous pending snippet |
| `j` / `k` | Scroll the diff; at its end, next / previous snippet |
| `gg` / `G` | Scroll the diff to its top / bottom |
| `Home` / `End` | First / last row of the tree |
| `/` | Search paths, symbols and diff text (a regexp; case-insensitive unless it has an uppercase letter) |
| `n` / `N` | Next / previous search hit |
| `o` | Open the snippet in `$VISUAL` / `$EDITOR` at its first change |
| `E` / `C` | Expand / collapse the whole tree |
| `Tab` | Turn auto-advance on or off (jump to the next pending snippet after a verdict) |
| `q` / `Ctrl-C` | Quit |
| `?` | Show all keys |

### Staging and verdicts

In the default review (no `--staged`, `--ref` or `--base`), the header
shows `[staging]`:

- `y` stages exactly that snippet, like `git add -p`. Other changes in the
  same file stay unstaged.
- `x` or `r` on a snippet you accepted unstages it again.
- `--no-apply` turns staging off, so the keys only record verdicts.

Astra changes only the index, never your files. A staged snippet leaves
the unstaged diff, so the next `astra diff` no longer shows it, and
`astra diff --staged` shows it as accepted. If HEAD, the index or a file
changes outside astra during a staging review, the next `y`, `x` or `r`
reloads the diff instead; press the key again.

Verdicts are saved in `.git/astra/verdicts.json`, keyed by the snippet and
its exact change. If you edit a change after judging it, the snippet is
pending again. A snippet with a name, such as a function, a type or the
imports, is keyed by that name, so its verdict follows the change across
commit, amend and rebase, and every mode sees it. A snippet without a
name, such as a comment between two functions or a hunk in a Markdown
file, is keyed by the line it starts on. Its verdict resets when lines
above it are added or removed, for example by a rebase, and
`astra diff --staged` can show it as pending after `y` staged it, when
unstaged changes sit above it. In the staging review, ✓ means staged: an
accepted snippet that is still unstaged shows as pending.

Changed files that give no snippet are listed on a `Not shown:` line with
the reason: binary, renamed, mode, empty, conflict or no-diff. In
`--base` and `--staged` reviews, when your working tree no longer holds
the reviewed version of a file, `o` opens a read-only copy of that version.
Astra deletes these copies when it exits.

## Chrome extension

The extension adds an **⭐ Astra** button to the Files changed tab of a
pull request on github.com (the `…/pull/<n>/changes` page). The button
opens a panel with the review tree and the snippets.

It is not in the Chrome Web Store. To install it:

1. Get the extension folder. Unzip `astra-extension-<version>.zip` from a
   [release](https://github.com/storymode7/astra/releases), or use
   `apps/extension/` in a clone of this repository.
2. Open `chrome://extensions` and turn on Developer mode.
3. Click **Load unpacked** and select the folder.

What it can do:

- It has one content script, which runs on `https://github.com/*`. It asks
  for no permissions and no host permissions. Chrome still warns that it
  can "Read and change your data on github.com", because the script runs
  on those pages.
- On a pull request page, it reads the page's own data, the diff entries
  that GitHub loads later, and the raw source of each changed file in a
  supported language. These are same-origin requests from the page, so the
  browser sends your GitHub session with them, and private repositories
  you can open work. Astra stores no token.
- It never writes to GitHub. Verdicts and comments stay in the extension's
  storage in this browser.

Keys in the panel: `j` / `k` move, `y` or `a` accepts, `x` rejects, `r`
marks reviewed, `c` comments and `z` folds a snippet. In the comment box,
`Ctrl+Enter` (`Cmd+Enter` on a Mac) saves, and `Esc` hides the box but
keeps the draft.

## Web app

`apps/pwa/` is a static web app for public pull requests. Paste a pull
request URL. The app gets the file list from `api.github.com` and the
changed files' sources from `raw.githubusercontent.com`, with no login,
and runs the engine in your browser. It cannot read private repositories,
and it cannot post to GitHub. Verdicts and comments stay in your browser.
The keys are the same as in the extension.

GitHub allows 60 unauthenticated API requests an hour from one IP
address. A review uses one request for the pull request and one for each
100 changed files. GitHub lists at most 3000 files of a pull request.

To run it on your machine:

```sh
cd apps/pwa
python3 -m http.server --bind 127.0.0.1 8080
```

Then open <http://127.0.0.1:8080/>. To host your own copy, see
[docs/self-hosting.md](docs/self-hosting.md).

## Supported languages

| Language | Files |
|---|---|
| Go | `.go` |
| JavaScript | `.js`, `.jsx`, `.mjs`, `.cjs` |
| TypeScript | `.ts`, `.tsx`, `.mts` |
| Python | `.py` |
| Java | `.java` |
| Rust | `.rs` |

Other files show one snippet for each diff hunk, with no call structure.

## Security

- No backend, no OAuth, no telemetry, and no stored tokens.
- No part of Astra writes to GitHub.
- The Go module has no third-party dependencies, and the web code uses no
  npm packages. The few vendored files are pinned by SHA-256.
- Releases are not signed.

[SECURITY.md](SECURITY.md) describes the trust model in full and tells you
how to report a vulnerability.

## Development

You need Go 1.26.3 or newer. Node.js is needed only for the parity check
and the browser tests, and `make core-wasm` also needs `sha256sum`. There
is no npm install and no build step for the web code.

```sh
make cli                              # build ./astra
make test                             # go test ./... and the browser tests
go vet ./...
node --test shared/test/*.test.mjs    # tests for the browser code
make core-wasm                        # rebuild vendor/core/astra-core.wasm
sh scripts/sync-extension-lib.sh      # copy shared/lib and vendor/core into both web apps
make parity                           # check the committed WASM against the Go golden files
```

- `core/` is the engine: pure Go, with no I/O. After you change it, run
  `make core-wasm`, then the sync script, then `make parity`. If a parity
  fixture's output changes on purpose, update the golden files with
  `go test ./core -run Parity -update` and read the diff.
- `shared/lib/` holds the browser code that both web surfaces use. The sync
  script copies it to `apps/pwa/lib/shared/` and
  `apps/extension/lib/shared/`, copies `vendor/core/` to each app's
  `vendor/core/`, and copies `LICENSE` and `THIRD_PARTY_NOTICES` to
  `apps/pwa/`. Do not edit the copies.
  `sh scripts/sync-extension-lib.sh --check` fails when they differ.

CI (`.github/workflows/ci.yml`) runs on every push and pull request. It
runs `go vet` and `go test`, builds the CLI for linux and darwin on amd64
and arm64, builds the WebAssembly entry point, checks
`vendor/core/checksums.txt`, and runs the sync check, the parity check
and the browser tests.

## Releasing

No workflow builds or publishes releases. To make one by hand:

1. If the extension changed, bump `"version"` in
   `apps/extension/manifest.json`. Chrome accepts one to four numbers
   separated by dots. The zip is named after this version.
2. Tag the release commit, for example `git tag v0.1.0`.
3. Run `make dist`. `VERSION` defaults to
   `git describe --tags --always --dirty`, which is the tag on a tagged
   commit. To set it, run `make dist VERSION=v0.1.0`. `make dist` needs
   git, go, node, zip and sha256sum.
4. `make dist` runs the sync check and the parity check, then writes
   `dist/`:
   - `astra-<VERSION>-linux-amd64`, `-linux-arm64`, `-darwin-amd64` and
     `-darwin-arm64`, built with `CGO_ENABLED=0`, `-trimpath` and
     `-ldflags "-s -w -X main.version=<VERSION>"`;
   - `LICENSE` and `THIRD_PARTY_NOTICES`, which go with the binaries;
   - `astra-extension-<manifest version>.zip`, with the tracked files of
     `apps/extension/`, `LICENSE` and `THIRD_PARTY_NOTICES`;
   - `SHA256SUMS`.
5. Check the files: `cd dist && sha256sum -c SHA256SUMS`. On macOS, use
   `shasum -a 256 -c SHA256SUMS`.
6. Create a GitHub release for the tag, and upload every file in `dist/`.

## Layout

```
core/              review engine (Go, no I/O): entities, snippets, call edges, review tree
core/wasmmain/     WebAssembly entry point for the browser surfaces
apps/cli/          the astra CLI and its terminal UI
apps/extension/    Chrome extension (Manifest V3, plain JavaScript)
apps/pwa/          static web app for public pull requests (plain JavaScript)
shared/lib/        browser code for both web surfaces; synced into each app
shared/test/       node tests for the browser code
vendor/core/       the engine as WebAssembly, Go's wasm_exec.js, checksums.txt
scripts/           sync-extension-lib.sh, parity-check.mjs
docs/              self-hosting guide for the web app
```

## License

Astra is open source under the MIT license; see [LICENSE](LICENSE).

The CLI binaries and `astra-core.wasm` contain the Go runtime and standard
library, and the web surfaces ship Go's `wasm_exec.js`. The extension also
ships highlight.js 11.11.1. Both are under the BSD 3-Clause license; see
[THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES).
