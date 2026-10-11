# Self-hosting Astra PWA

The PWA is a **pure static site** — HTML, JS, and vendored WASM. No server, no build step, no database. You can host your own instance in under five minutes.

## Why self-host?

- Full control over what code runs in your browser.
- No dependency on our hosting (which may change or disappear).
- Inspect every file before deploying — there are no minified bundles.

## What you need

- Any static file host (GitHub Pages, Cloudflare Pages, Netlify, nginx, caddy, `python -m http.server`…)
- `git` and `curl` to clone and vendor grammars.

No Node.js. No npm. No Docker.

## Steps

### 1. Clone

```bash
git clone https://github.com/storymode7/astra
cd astra
```

### 2. Vendor grammar WASM files

The grammar files are pre-built WASM from official tree-sitter grammar releases.
They are NOT fetched from npm.

```bash
bash vendor/fetch-grammars.sh
```

This script downloads each `.wasm` file from its grammar's GitHub releases page,
verifies the SHA256 checksum, and writes them to `vendor/grammars/`.
Re-running is idempotent — already-verified files are skipped.

The checksums are in `vendor/checksums.txt`.
The download URLs and release dates are in `vendor/fetch-grammars.sh`.
**Inspect both before running.**

### 3. Verify checksums (optional but recommended)

```bash
cd vendor/grammars && sha256sum -c ../checksums.txt
```

### 4. Deploy

Point your static host at `apps/pwa/`.
The PWA needs access to `../../vendor/grammars/` relative to `apps/pwa/`
(i.e., `vendor/grammars/` from the repo root must be served alongside).

**GitHub Pages example:**

```bash
# Serve from the repo root; GH Pages will serve the whole repo.
# In repository Settings → Pages, set source to "main branch / root".
# Then visit: https://<you>.github.io/<repo>/apps/pwa/
```

**Cloudflare Pages / Netlify:**
Set the **publish directory** to the repository root (not `apps/pwa/`),
so that `vendor/grammars/` is accessible at `/vendor/grammars/` from the PWA.

**Local (for testing):**

```bash
# From repo root:
python3 -m http.server 8080
# Then open: http://localhost:8080/apps/pwa/
```

### 5. Update grammars

To update a grammar WASM:
1. Check the grammar's GitHub releases for a newer `.wasm` artifact.
2. Update the URL and checksum in `vendor/fetch-grammars.sh`.
3. Re-run `bash vendor/fetch-grammars.sh`.
4. Commit the updated `.wasm` and `checksums.txt`.

**Never update a grammar WASM within 14 days of its release.** This gives time
for the broader community to audit the new artifact.

## What the PWA can and cannot do

| Can | Cannot |
|---|---|
| Review public GitHub PRs (unauthenticated) | Access private repos (use the extension) |
| Run fully offline once grammar WASMs are loaded | Post review comments (use the extension) |
| Store review state in your browser's IndexedDB | Sync state across browsers/devices |
| Mobile swipe review (responsive layout) | |

## Trust verification checklist

Before deploying your own instance, you may want to verify:

- [ ] `vendor/checksums.txt` matches the files in `vendor/grammars/`  
      (`cd vendor/grammars && sha256sum -c ../checksums.txt`)
- [ ] Download URLs in `vendor/fetch-grammars.sh` point to the official  
      `github.com/tree-sitter/tree-sitter-*` repos
- [ ] Release dates in `vendor/fetch-grammars.sh` are not suspiciously recent
- [ ] `apps/pwa/lib/github.js` makes no requests outside `api.github.com`
- [ ] `apps/pwa/index.html` loads no external scripts (no CDN links)
