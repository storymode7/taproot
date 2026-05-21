# Security Policy

## Trust model

Astra's security posture is a first-class design constraint, not an afterthought. These commitments are binding on all code in this repository:

### No backend
Astra has no server-side component. Diff fetching, AST parsing, ordering, and state storage all happen on the user's device. There is no Astra-operated server that ever receives source code.

### No OAuth app
The Chrome extension uses the user's existing GitHub session via same-origin `fetch` inside the content script context. No token is stored by Astra. The extension never reads, stores, or transmits authentication credentials.

### No npm at runtime
Grammar files are pre-compiled WASM distributed by their respective grammar authors on GitHub releases. They are vendored (SHA256-pinned, committed to this repo) in `vendor/grammars/`. The extension and PWA load them as static assets. Zero npm packages are fetched at runtime.

### No telemetry
Astra collects no usage data, no error reports, no analytics. There are no network calls beyond what the user explicitly triggers (fetching a PR diff from GitHub).

### Minimal permission surface
- Chrome extension: `host_permissions: ["https://github.com/*"]` only. No `tabs`, no `<all_urls>`, no cookie access.
- CLI: zero network calls by default.
- PWA: only unauthenticated calls to `api.github.com` for public PRs.

### Vendored dependencies
All runtime dependencies are vendored and pinned:
- `vendor/grammars/*.wasm` — pre-built tree-sitter grammar WASM files
- `vendor/checksums.txt` — SHA256 checksums for all vendored files
- Go modules — locked via `go.sum`

`vendor/fetch-grammars.sh` documents exactly where each file was obtained and verifies checksums on re-run.

### Dependency hygiene
- Direct deps only; no transitive dep is accepted from an unknown maintainer without explicit review.
- Any new dependency requires a comment in the PR explaining: what it does, why it's needed, when it was last updated, who maintains it.
- No dependency that received a significant new release in the 14 days preceding a Astra release will be upgraded to that version without an extended audit window.

### Reproducible builds
CI build artifacts are byte-identical to local builds. Build instructions are published so anyone can verify release artifacts.

### Signed releases
CLI binaries and extension `.zip` files are signed. The signing public key is published in this README.

### Reporting a vulnerability

Please open a GitHub Security Advisory (private) rather than a public issue.
