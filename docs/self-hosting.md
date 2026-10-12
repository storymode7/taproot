# Self-hosting the web app

The web app in `apps/pwa/` is a static site: HTML, CSS, plain JavaScript
modules and the engine compiled to WebAssembly. There is no build step, no
server code and no database. The folder holds everything the app loads:
`scripts/sync-extension-lib.sh` copies the shared browser code into
`apps/pwa/lib/shared/` and the engine into `apps/pwa/vendor/core/`. It
also copies `LICENSE` and `THIRD_PARTY_NOTICES` into `apps/pwa/`: the
engine contains the Go runtime, and Go's license asks that its notice
ship with it.

## What it can and cannot do

It can:

- review public GitHub pull requests, without a login;
- load up to 3000 changed files, the most that GitHub's API lists for a
  pull request. When a pull request changes more, a banner says how many
  of them the review shows;
- save verdicts and comments in your browser (IndexedDB, per origin).

It cannot:

- read private repositories (use the Chrome extension);
- post comments or reviews to GitHub; comments stay in your browser;
- sync verdicts and comments between browsers or devices;
- work offline or install as an app: it has no service worker and no web
  manifest.

Requests and limits:

- It calls `api.github.com` without credentials: one request for the pull
  request and one for each 100 changed files (2 requests for up to 100
  files, 31 for 3000). GitHub allows 60 unauthenticated API requests an
  hour from one IP address. When the limit is reached, the app says when
  it resets.
- It fetches the source of each changed file in a supported language from
  `raw.githubusercontent.com`. A file whose source fails to load, or takes
  longer than 15 seconds, shows as plain hunks without call structure.
- It contacts no other host than these two and the host that serves it.

## 1. Get the files

```sh
git clone https://github.com/storymode7/astra
cd astra
```

## 2. Check the vendored engine

```sh
(cd apps/pwa/vendor/core && sha256sum -c checksums.txt)
(cd vendor/core && sha256sum -c checksums.txt)
sh scripts/sync-extension-lib.sh --check
```

On macOS, use `shasum -a 256 -c checksums.txt` in place of `sha256sum -c
checksums.txt`. The sync check confirms that `apps/pwa/` holds the same
engine, shared code and license files as `vendor/core/`, `shared/lib/`
and the repository root.

The committed `astra-core.wasm` is built from `core/` by `make core-wasm`.
To run your own build instead, run these commands (`make core-wasm` needs
Go and `sha256sum`):

```sh
make core-wasm
sh scripts/sync-extension-lib.sh
make parity
```

Your build can differ byte for byte from the committed file, because the
bytes depend on the Go toolchain. `make parity` (it needs Node.js) checks
that the WASM gives the same output as the Go code on the test fixtures.

## 3. Publish `apps/pwa/`

Publish the contents of `apps/pwa/` as the web root of any static host.
All its URLs are relative, so a sub-path works too. There is no server
configuration to do.

Serve it over HTTPS. When the page is not a secure context (plain HTTP
from a host other than localhost or 127.0.0.1), the browser does not give
it the crypto API that keys the saved state. Verdicts and comments then
stay in memory, and a reload or another pull request clears them.

To try it on your machine:

```sh
cd apps/pwa
python3 -m http.server --bind 127.0.0.1 8080
```

Then open <http://127.0.0.1:8080/>.

GitHub Pages publishes a branch from its root or its `docs/` folder. To
use it, publish the repository root and open
`https://<user>.github.io/<repo>/apps/pwa/`.

Prefer an origin that serves nothing but the app, such as a domain or
subdomain of its own. The browser keeps verdicts and comments in
IndexedDB for the whole origin, so any page on the same origin can read
your comments and mark snippets as accepted. On GitHub Pages, every
project site of an account shares the origin `https://<user>.github.io`
unless its repository sets a custom domain.

To update, pull the new commits, run the checks in step 2 again and
publish `apps/pwa/` again.

## Trust checklist

- [ ] The checksum and sync checks in step 2 pass.
- [ ] The app has an origin of its own, as step 3 explains.
- [ ] `apps/pwa/index.html` loads only local scripts and stylesheets, with
      no CDN.
- [ ] The only hosts the app's code fetches from are `api.github.com` and
      `raw.githubusercontent.com`. The command below prints every line in
      `apps/pwa/` that holds a URL with any scheme (`https:`, `http:`,
      `wss:` and so on) or a `//host` URL, in any text file. `github.com`
      should appear only in links, comments and pull request URLs, and
      other hosts only in `THIRD_PARTY_NOTICES`. Where a line builds a URL
      from variables, check where they come from. No search can find a
      host that code assembles from pieces at run time.
- [ ] Optional: you rebuilt the engine yourself, as in step 2.

```sh
grep -rniIE '[a-z][a-z0-9+.-]*://|//[a-z0-9.-]+\.[a-z]{2,}' apps/pwa
```
