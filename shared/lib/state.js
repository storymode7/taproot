/**
 * shared/lib/state.js — local review state (IndexedDB)
 *
 * Stores per-snippet review status (pending/reviewed/accepted/rejected)
 * and comment drafts. Records are keyed by the PR, the snippet ID and a
 * SHA-256 of the snippet's changed (+/-) lines, so state never carries
 * over to another PR, and a snippet whose change differs after a push
 * starts again as pending.
 *
 * No server, no sync. State is per-browser, per-origin.
 */

// Version 1 keyed records by snippet ID alone, which leaked across PRs.
const DB_VERSION = 2;
const STORE = "reviews";

export class StateStore {
  /**
   * @param {string} namespace — "astra-pwa" or "astra-ext"
   */
  constructor(namespace) {
    this._ns = namespace;
    this._db = null;
    this._keys = new Map(); // snippet ID -> record key for the review in view
    this._mem = new Map(); // snippet ID -> state; the only copy without IndexedDB
    this._ready = this._open();
  }

  async _open() {
    if (typeof indexedDB === "undefined") return;
    return new Promise((resolve) => {
      const req = indexedDB.open(this._ns, DB_VERSION);
      req.onupgradeneeded = (e) => {
        const db = e.target.result;
        if (db.objectStoreNames.contains("snippets")) db.deleteObjectStore("snippets");
        if (!db.objectStoreNames.contains(STORE)) db.createObjectStore(STORE, { keyPath: "key" });
      };
      req.onsuccess = (e) => { this._db = e.target.result; resolve(); };
      req.onerror = () => { resolve(); }; // fallback to in-memory
      req.onblocked = () => { resolve(); }; // an older tab still has version 1 open
    });
  }

  /**
   * Switch to one review and load its saved state into the memory cache.
   * Call after receiving a diff, before rendering it.
   * @param {string} scope — "owner/repo#number"
   * @param {{ id: string, diffLines: string[] }[]} snippets
   */
  async load(scope, snippets) {
    const keys = new Map();
    const mem = new Map();
    this._keys = keys;
    this._mem = mem;
    if (!globalThis.crypto?.subtle) return; // not a secure context: memory only
    for (const s of snippets) keys.set(s.id, await stateKey(scope, s));
    await this._ready;
    if (!this._db) return;
    const store = this._db.transaction(STORE, "readonly").objectStore(STORE);
    await Promise.all([...keys].map(([id, key]) => new Promise((resolve) => {
      const req = store.get(key);
      req.onsuccess = () => {
        const rec = req.result;
        if (rec) mem.set(id, { id, status: rec.status, comment: rec.comment });
        resolve();
      };
      req.onerror = () => resolve();
    })));
  }

  /**
   * Get snippet state. Returns default (pending) if not yet stored.
   * @param {string} snippetId
   * @returns {{ id: string, status: string, comment: string }}
   */
  get(snippetId) {
    return this._mem.get(snippetId) ?? { id: snippetId, status: "pending", comment: "" };
  }

  /**
   * Persist snippet state (async, non-blocking).
   * @param {string} snippetId
   * @param {{ status?: string, comment?: string }} patch
   */
  set(snippetId, patch) {
    const next = { ...this.get(snippetId), ...patch, id: snippetId };
    this._mem.set(snippetId, next);
    const key = this._keys.get(snippetId);
    if (key) this._persist({ key, status: next.status, comment: next.comment });
  }

  async _persist(record) {
    await this._ready;
    if (!this._db) return;
    this._db.transaction(STORE, "readwrite").objectStore(STORE).put(record);
  }
}

/**
 * Record key for a snippet in a review: the PR (case-insensitive, as on
 * GitHub), the snippet ID and a SHA-256 of its +/- lines only, so a push
 * that merely moves the change keeps its state.
 * @param {string} scope — "owner/repo#number"
 * @param {{ id: string, diffLines: string[] }} snippet
 */
export async function stateKey(scope, snippet) {
  const changed = snippet.diffLines.filter((l) => l[0] === "+" || l[0] === "-").join("\n");
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(changed));
  const hex = Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, "0")).join("");
  return `${scope.toLowerCase()}:${snippet.id}:${hex}`;
}
