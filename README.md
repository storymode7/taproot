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
2. Parses changed files with [tree-sitter](https://tree-sitter.github.io) (WASM, vendored).
3. Maps each diff hunk to its smallest enclosing named AST node → **snippet**.
4. Builds a cross-file import/call graph from tree-sitter queries (`.scm` files in `queries/`).
5. Orders snippets by graph depth (3 presets: top-down, bottom-up, feature-clusters).
6. A single file can appear in multiple positions — `utils.ts::formatDate` early, `utils.ts::parseCSV` late.

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
