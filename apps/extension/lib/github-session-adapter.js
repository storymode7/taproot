/**
 * lib/github-session-adapter.js — Extension DiffSource via existing GH session
 *
 * Used by the Chrome extension content script context, where all fetches to
 * github.com are same-origin and inherit the user's logged-in session.
 * No token is stored or transmitted by Astra.
 *
 * This adapter provides the same interface as the PWA's github.js but:
 *   - uses `credentials: "include"` (piggybacks the user's session cookie)
 *   - can access private repos the user already has access to
 */

const GH_API = "https://api.github.com";

export class GitHubSessionAdapter {
  /**
   * @param {string} owner
   * @param {string} repo
   * @param {number} prNumber
   */
  constructor(owner, repo, prNumber) {
    this.owner = owner;
    this.repo = repo;
    this.prNumber = prNumber;
    this._meta = null;
  }

  async meta() {
    if (this._meta) return this._meta;
    const data = await this._fetch(`/repos/${this.owner}/${this.repo}/pulls/${this.prNumber}`);
    this._meta = {
      owner: this.owner,
      repo: this.repo,
      number: this.prNumber,
      title: data.title,
      headSHA: data.head?.sha ?? "",
      baseSHA: data.base?.sha ?? "",
    };
    return this._meta;
  }

  async files() {
    // Paginate up to 300 files (3 pages × 100).
    const all = [];
    for (let page = 1; page <= 3; page++) {
      const batch = await this._fetch(
        `/repos/${this.owner}/${this.repo}/pulls/${this.prNumber}/files?per_page=100&page=${page}`
      );
      all.push(...batch);
      if (batch.length < 100) break;
    }
    return all;
  }

  async fileBlob(path, sha) {
    const ref = sha || (await this.meta()).headSHA;
    const data = await this._fetch(
      `/repos/${this.owner}/${this.repo}/contents/${encodeURIComponent(path)}?ref=${ref}`
    );
    // GitHub returns content as base64.
    return atob(data.content.replace(/\n/g, ""));
  }

  async postReviewComment({ path, line, body, replyToId = 0 }) {
    const { headSHA } = await this.meta();
    const payload = replyToId
      ? { body, in_reply_to: replyToId }
      : { body, path, line, side: "RIGHT", commit_id: headSHA };

    const resp = await this._rawFetch(
      `/repos/${this.owner}/${this.repo}/pulls/${this.prNumber}/comments`,
      { method: "POST", body: JSON.stringify(payload) }
    );
    if (!resp.ok) throw new Error(`GitHub ${resp.status}: ${resp.statusText}`);
    const data = await resp.json();
    return data.id;
  }

  async markFileViewed(path, viewed) {
    const resp = await this._rawFetch(
      `/repos/${this.owner}/${this.repo}/pulls/${this.prNumber}/viewed`,
      {
        method: viewed ? "PUT" : "DELETE",
        body: JSON.stringify({ file_path: path }),
      }
    );
    if (!resp.ok) throw new Error(`GitHub ${resp.status}: ${resp.statusText}`);
  }

  // -------------------------------------------------------------------------
  // Internal
  // -------------------------------------------------------------------------

  async _fetch(path, options = {}) {
    const resp = await this._rawFetch(path, options);
    if (!resp.ok) {
      const err = new Error(`GitHub API ${resp.status}: ${resp.statusText}`);
      err.status = resp.status;
      throw err;
    }
    return resp.json();
  }

  _rawFetch(path, options = {}) {
    return fetch(`${GH_API}${path}`, {
      ...options,
      credentials: "include", // use the user's existing github.com session
      headers: {
        "Accept": "application/vnd.github+json",
        "Content-Type": "application/json",
        ...(options.headers || {}),
      },
    });
  }
}
