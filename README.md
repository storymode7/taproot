# Astra

AST-aware PR diff reviewer. Reorders GitHub PR diffs by call-graph depth so you read the final implementation first, then drill one level deeper at each step.

**Status: early development.**

## Trust posture

Astra is built for an environment where supply-chain trust is a first-class concern:

- **No backend.** All processing happens locally — in your browser, extension, or terminal.
- **No OAuth.** The Chrome extension uses your existing GitHub session. No tokens stored by Astra.
- **No npm at runtime.** Grammar files are pre-compiled WASM, vendored and SHA256-pinned in this repo.
- **Zero new compilers per language.** Tree-sitter is the one runtime; adding a new language means fetching one pre-built `.wasm` file and writing a ~50-line `.scm` query file.
- **Source-available (MIT).** Auditability is the trust mechanism.

See [SECURITY.md](SECURITY.md) for the full trust policy.

## How it works

1. Fetches the PR diff (via your existing GitHub session, or unauthenticated for public repos).
2. Fetches the full post-image source of each changed file and extracts entity
   boundaries (functions, classes, types) — via [tree-sitter](https://tree-sitter.github.io)
   (WASM, vendored) in the browser surfaces, language-aware heuristics in the CLI.
3. Maps each diff hunk to its enclosing entity → **snippet** (a whole changed
   function/class, not a bare hunk).
4. Scans entity bodies for call sites naming other changed entities → a call
   graph over the diff.
5. Builds a **review tree**: roots are entry points nothing else in the diff
   calls; children are callees, in call order. The CLI TUI is a drill-down over
   this tree — read the top, unfold one level at a time.
6. A single file can appear in multiple positions — `utils.ts::formatDate`
   under one caller, `utils.ts::parseCSV` under another.

## Usage

```
astra diff                          # review uncommitted changes
astra diff --staged                 # review what's staged for commit
astra diff --base abc1^ --head abc1 # review one commit
astra diff --base main              # review this branch like its PR (changes since it forked)
astra diff --base main --head feat  # review another branch without checking it out
astra diff --base main | less       # plain text when piped
```

In the TUI: `J/K` move, `j/k` scroll the diff, `l/h` drill in/out, `y/n/r` accept/reject/reviewed,
`o` open in `$EDITOR`, `?` for everything else. Tests are listed after the
code they test, and each snippet notes which tests cover it.

## Surfaces

| Surface | Trust ask | Private repos? |
|---|---|---|
| CLI (`astra diff`) | Lowest — static Go binary, zero network | Yes (local git) |
| Chrome extension | Low — `github.com` host permission only, uses existing session | Yes |
| Static PWA | Low — self-hostable static site, unauthenticated only | Public only |

## Supported languages (MVP)

TypeScript/JavaScript · Python · Go · Java · Rust

Fallback: unsupported languages show as a single whole-file snippet.

## Development

```
# CLI
make cli            # or: go build -o astra ./apps/cli/cmd/astra

# Vendor grammar WASM files (fetch once, pin SHA256)
bash vendor/fetch-grammars.sh

# Extension / PWA — vanilla JS, no npm. The review engine is the Go core
# compiled to WASM; the built artifact is committed, so no build is needed
# unless you change /core:
make core-wasm                       # rebuild vendor/core/astra-core.wasm
sh scripts/sync-extension-lib.sh     # sync /shared/lib + core into both surfaces
make parity                          # assert Go and WASM produce identical output
```

The engine lives once in `/core` (pure Go, no I/O). The CLI links it
natively; the extension and PWA call the same code through
`vendor/core/astra-core.wasm`. Browser-side glue lives in `/shared/lib`
and is copied into each surface by the sync script — never edit the
copies.

## Directory structure

```
vendor/grammars/     pre-built .wasm files, SHA256-pinned
vendor/checksums.txt SHA256 of every vendored file
queries/<lang>/      tree-sitter .scm query files (plain text)
apps/cli/            Go CLI — zero CGO, wazero WASM runtime
apps/extension/      MV3 Chrome extension, vanilla JS
apps/pwa/            Static PWA, vanilla JS, GitHub Pages
docs/                Architecture and design notes
```
