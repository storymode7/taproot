/**
 * lib/github.js — GitHub API helpers (unauthenticated, public repos only)
 *
 * The PWA never accepts or stores tokens. Rate limit: 60 req/hr per IP.
 */

const GH_API = "https://api.github.com";

/**
 * Parse a GitHub PR URL into { owner, repo, prNumber }.
 * Throws if the URL is not a recognisable GitHub PR URL.
 */
export function parsePRUrl(raw) {
  let url;
  try {
    url = new URL(raw);
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
 * Throws an object with { status, message } on HTTP errors.
 */
export async function fetchPRFiles(owner, repo, prNumber) {
  const pr = `/repos/${owner}/${repo}/pulls/${prNumber}`;
  const page = (n) => ghFetch(`${pr}/files?per_page=${PER_PAGE}&page=${n}`);
  const [metaResp, first] = await Promise.all([ghFetch(pr), page(1)]);

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
    files.push(...(await page(n)));
  }
  return { meta, files };
}

async function ghFetch(path) {
  const resp = await fetch(`${GH_API}${path}`, {
    headers: { Accept: "application/vnd.github+json" },
  });
  if (!resp.ok) {
    const err = new Error(`GitHub API error: ${resp.status} ${resp.statusText}`);
    err.status = resp.status;
    throw err;
  }
  return resp.json();
}
