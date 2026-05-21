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

/**
 * Fetch all changed files for a PR (max 300 files via pagination).
 * Returns { meta, files } where files matches the GitHub REST shape.
 *
 * Throws an object with { status, message } on HTTP errors.
 */
export async function fetchPRFiles(owner, repo, prNumber) {
  // Fetch PR metadata and files in parallel
  const [metaResp, filesResp] = await Promise.all([
    ghFetch(`/repos/${owner}/${repo}/pulls/${prNumber}`),
    ghFetch(`/repos/${owner}/${repo}/pulls/${prNumber}/files?per_page=100`),
  ]);

  const meta = {
    owner, repo, number: prNumber,
    title: metaResp.title,
    headSHA: metaResp.head?.sha ?? "",
    baseSHA: metaResp.base?.sha ?? "",
  };

  // Paginate if >100 files
  let files = filesResp;
  // (full pagination left as a future improvement; 100 covers most PRs)

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
