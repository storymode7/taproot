/**
 * lib/github-public-adapter.js — unauthenticated GitHub API adapter (PWA)
 *
 * NEVER accepts or stores a token. Public repos only. Rate limit: 60 req/hr.
 * No input field for tokens — if we never accept one, we can never leak one.
 */

const GH_API = "https://api.github.com";

export class GitHubPublicAdapter {
  constructor(owner, repo, prNumber) {
    this.owner = owner;
    this.repo = repo;
    this.prNumber = prNumber;
    this._meta = null;
  }

  async meta() {
    if (this._meta) return this._meta;
    const data = await ghFetch(`/repos/${this.owner}/${this.repo}/pulls/${this.prNumber}`);
    this._meta = {
      owner: this.owner, repo: this.repo, number: this.prNumber,
      title: data.title,
      headSHA: data.head?.sha ?? "",
      baseSHA: data.base?.sha ?? "",
    };
    return this._meta;
  }

  async files() {
    const all = [];
    for (let page = 1; page <= 3; page++) {
      const batch = await ghFetch(
        `/repos/${this.owner}/${this.repo}/pulls/${this.prNumber}/files?per_page=100&page=${page}`
      );
      all.push(...batch);
      if (batch.length < 100) break;
    }
    return all;
  }

  async fileBlob(path, sha) {
    const ref = sha || (await this.meta()).headSHA;
    const data = await ghFetch(
      `/repos/${this.owner}/${this.repo}/contents/${encodeURIComponent(path)}?ref=${ref}`
    );
    return atob(data.content.replace(/\n/g, ""));
  }

  // Read-only adapter — no write methods.
  async postReviewComment() {
    throw Object.assign(new Error("Read-only: use the extension to post comments"), { readOnly: true });
  }
  async markFileViewed() {
    throw Object.assign(new Error("Read-only: use the extension to mark files viewed"), { readOnly: true });
  }
}

async function ghFetch(path) {
  const resp = await fetch(`${GH_API}${path}`, {
    headers: { Accept: "application/vnd.github+json" },
    // No credentials — unauthenticated by design.
  });
  if (!resp.ok) {
    const err = new Error(`GitHub ${resp.status}: ${resp.statusText}`);
    err.status = resp.status;
    throw err;
  }
  return resp.json();
}
