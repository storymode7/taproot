# Security

## Reporting a vulnerability

Report it privately through GitHub: on the repository's **Security** tab,
click **Report a vulnerability**
(<https://github.com/storymode7/astra/security/advisories/new>). Please do
not open a public issue.

## Trust model

- **No backend.** Astra runs no server. The CLI runs on your machine, and
  the extension and the web app run in your browser. No part of Astra
  sends your code to a server; the browser surfaces only fetch from
  GitHub, with the requests listed below.
- **No OAuth and no tokens.** Astra never asks for, stores or sends a
  GitHub token. The extension's requests carry your github.com session
  because they are same-origin requests from the GitHub page. The web app
  makes only unauthenticated requests.
- **No telemetry.** No analytics, no error reports and no update checks.
- **Read-only toward GitHub.** No part of Astra writes to GitHub. Verdicts
  and comments made in a browser stay in that browser.

## What each part can reach

### CLI

- It makes no network requests of its own. It runs `git`, `stty`, your
  `$VISUAL` / `$EDITOR` for the `o` key, and `delta` or `bat` when one is
  installed.
- It writes `.git/astra/verdicts.json` in your repository. In the default
  review, `y`, `x` and `r` also stage or unstage snippets in the git index;
  `--no-apply`, `--staged`, `--ref` and `--base` turn that off. It never
  changes the files in your working tree.
- For `o` in `--base` and `--staged` reviews, it can write read-only
  temporary copies of reviewed files. It deletes them when it exits.

### Chrome extension

`apps/extension/manifest.json` declares:

- one content script, `content.js`, that matches `https://github.com/*`;
- no `permissions` and no `host_permissions`;
- one web-accessible resource, `panel/panel.html`, for
  `https://github.com/*`;
- a content security policy for its own pages:
  `script-src 'self' 'wasm-unsafe-eval'; object-src 'self'`.

Chrome still shows "Read and change your data on github.com" when you
install it, because the content script runs on github.com.

The content script acts only on the Files changed page of a pull request
(`/<owner>/<repo>/pull/<n>/changes`). There it makes same-origin GET
requests to github.com, so the browser sends your github.com cookies with
them:

- the pull request page, when GitHub switched to it without a reload;
- the diff entries that the page has not loaded yet
  (`/<owner>/<repo>/pull/<n>/page_data/diff_entries`);
- the raw source of each changed file in a supported language
  (`/<owner>/<repo>/raw/<commit>/<path>`). GitHub answers with a redirect
  to `raw.githubusercontent.com`, and the browser fetches the file from
  there without your cookies. For a private repository, the redirect URL
  carries a short-lived token from GitHub.

It sends the result to the panel, an extension page in an iframe, which
runs the engine. The panel and the content script accept messages only
from each other's origin. Verdicts and comments go to the extension's
IndexedDB.

### Web app

The web app in `apps/pwa/` makes only these requests, all without
credentials:

- `https://api.github.com/repos/<owner>/<repo>/pulls/<n>` and
  `…/pulls/<n>/files?per_page=100&page=<k>`, for the pull request and its
  file list;
- `https://raw.githubusercontent.com/<owner>/<repo>/<commit>/<path>`, for
  the source of each changed file in a supported language;
- its own files, from the host that serves it.

Verdicts and comments go to IndexedDB for the app's origin. When the page
is not a secure context (plain HTTP from a host other than localhost or
127.0.0.1), they stay in memory only.

## Dependencies and vendored files

The Go module has no third-party dependencies (`go.sum` is empty). The web
code is plain JavaScript modules, with no npm packages and no build step.
These files are vendored and pinned by SHA-256:

| File | What it is | Pinned in |
|---|---|---|
| `vendor/core/astra-core.wasm` | The engine, built from `core/` by `make core-wasm` | `vendor/core/checksums.txt` |
| `vendor/core/wasm_exec.js` | Go's WebAssembly loader, copied from the Go toolchain by `make core-wasm` | `vendor/core/checksums.txt` |
| `apps/pwa/vendor/core/`, `apps/extension/vendor/core/` | Copies of `vendor/core/`, written by `scripts/sync-extension-lib.sh` | the `checksums.txt` in each copy |
| `apps/extension/vendor/highlight.min.js` | highlight.js 11.11.1 with its Go, Java, JavaScript, Python, Rust and TypeScript languages, bundled and minified | `apps/extension/vendor/checksums.txt` |

To check them:

```sh
(cd vendor/core && sha256sum -c checksums.txt)
(cd apps/extension/vendor && sha256sum -c checksums.txt)
sh scripts/sync-extension-lib.sh --check
make parity
```

The sync check confirms that the copies in both apps match `vendor/core/`
and `shared/lib/`. `make parity` runs the committed WASM on the fixtures in
`core/testdata/parity/` and compares its output with the Go golden files.
CI runs the `vendor/core` checksum check, the sync check and the parity
check on every push and pull request.

Limits of these checks:

- Parity shows that the WASM gives the Go code's output on the fixtures.
  It does not prove that the WASM was built from this source. If you do
  not want to trust the committed file, run `make core-wasm` and then
  `sh scripts/sync-extension-lib.sh`, and both web surfaces use your
  build. The bytes depend on your Go toolchain, so they can differ from
  the committed file.
- The highlight.js bundle was built once with esbuild, from an entry file
  that is not in this repository, so it cannot be rebuilt from here. Its
  checksum pins the exact bytes that ship.

## Releases

- Releases are built by hand with `make dist` and uploaded to GitHub
  Releases. No workflow publishes them; CI only checks that the CLI
  builds.
- Release files are not signed. `SHA256SUMS` detects a damaged download,
  but it comes from the same place as the files, so it does not prove who
  built them.
- There is no reproducible-build guarantee. The bytes depend on the Go
  toolchain and the build settings.
- `go version -m astra` prints the Go version, the build settings and the
  commit (`vcs.revision`) that a binary says it was built from.
- To avoid trusting a binary, build the CLI from source with `make cli`.
