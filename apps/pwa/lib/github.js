/**
 * lib/github.js — GitHub API helpers (unauthenticated, public repos only)
 *
 * The PWA never accepts or stores tokens. Rate limit: 60 req/hr per IP.
 */

const GH_API = "https://api.github.com";

/**
 * Parse a GitHub PR URL into { owner, repo, prNumber }; "https://" may be
 * left out. Throws if the URL is not a recognisable GitHub PR URL.
 */
export function parsePRUrl(raw) {
  if (!raw) throw new Error("Paste the URL of a GitHub pull request.");
  let url;
  try {
    url = new URL(/^[a-z][a-z0-9+.-]*:\/\//i.test(raw) ? raw : `https://${raw}`);
  } catch {
    throw new Error("Not a valid URL.");
  }
  if (url.hostname !== "github.com") {
    throw new Error("URL must be a github.com URL.");
  }
  // pathname: /owner/repo/pull/123[/files][?...]
  const match = url.pathname.match(/^\/([^/]+)\/([^/]+)\/pull\/(\d+)/);
  if (!match) {
    throw new Error("URL doesn't look like a GitHub PR (expected /owner/repo/pull/N).");
  }
  return { owner: match[1], repo: match[2], prNumber: Number(match[3]) };
}

// GitHub's files endpoint serves 100 files per page and lists no more than
// 3000 files in all.
const PER_PAGE = 100;
const MAX_FILES = 3000;

/**
 * Fetch the changed files of a PR, page by page (one API call per page).
 * Returns { meta, files } where files matches the GitHub REST shape and
 * meta.changedFiles is the PR's own count, which exceeds files.length when
 * GitHub's list stops short.
 *
 * `onPage(n, pages)` runs before each page after the first.
 *
 * Throws an Error with `status` on HTTP errors, plus `rateLimited` and
 * `retryAt` (ms since the epoch, or null when GitHub did not say); with
 * `network` set when GitHub could not be reached; and an AbortError once
 * `signal` aborts.
 */
export async function fetchPRFiles(owner, repo, prNumber, { signal, onPage } = {}) {
  const pr = `/repos/${owner}/${repo}/pulls/${prNumber}`;
  const page = (n) => ghFetch(`${pr}/files?per_page=${PER_PAGE}&page=${n}`, signal);
  const [metaResp, first] = await Promise.all([ghFetch(pr, signal), page(1)]);

  const meta = {
    owner, repo, number: prNumber,
    title: metaResp.title,
    headSHA: metaResp.head?.sha ?? "",
    baseSHA: metaResp.base?.sha ?? "",
    changedFiles: metaResp.changed_files ?? first.length,
  };

  const files = [...first];
  const pages = Math.ceil(Math.min(meta.changedFiles, MAX_FILES) / PER_PAGE);
  for (let n = 2; n <= pages && files.length === (n - 1) * PER_PAGE; n++) {
    onPage?.(n, pages);
    files.push(...(await page(n)));
  }
  return { meta, files };
}

async function ghFetch(path, signal) {
  let resp;
  try {
    resp = await fetch(`${GH_API}${path}`, {
      headers: { Accept: "application/vnd.github+json" },
      signal,
    });
  } catch (e) {
    if (signal?.aborted) throw e;
    throw Object.assign(new Error("Couldn't reach GitHub. Check your connection and try again."), { network: true });
  }
  if (!resp.ok) {
    // statusText is empty over HTTP/2; GitHub's own message says more.
    const body = await resp.json().catch(() => ({}));
    const err = new Error(`GitHub API error: ${resp.status} ${body.message ?? resp.statusText}`);
    err.status = resp.status;
    // A 403 is a rate limit only when the quota is spent or GitHub asks to
    // wait; when the headers are unreadable, assume it is one.
    const left = resp.headers.get("x-ratelimit-remaining");
    const wait = resp.headers.get("retry-after");
    err.rateLimited = resp.status === 429 || (resp.status === 403 && (left === null || left === "0" || wait !== null));
    const reset = Number(resp.headers.get("x-ratelimit-reset"));
    err.retryAt = wait !== null ? Date.now() + Number(wait) * 1000 : left === "0" && reset ? reset * 1000 : null;
    throw err;
  }
  return resp.json();
}
