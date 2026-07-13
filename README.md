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
cd apps/cli && go build ./cmd/astra

# Vendor grammar WASM files (fetch once, pin SHA256)
bash vendor/fetch-grammars.sh

# Extension / PWA — no build step, vanilla JS
# Open apps/extension/ or apps/pwa/ directly
```

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
