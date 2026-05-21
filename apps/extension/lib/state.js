/**
 * lib/state.js — local review state (IndexedDB)
 *
 * Stores per-snippet review status (pending/reviewed/accepted/rejected)
 * and comment drafts. Keyed by snippet ID (which encodes file+symbol+line).
 *
 * No server, no sync. State is per-browser, per-origin.
 */

const DB_VERSION = 1;

export class StateStore {
  /**
   * @param {string} namespace — "astra-pwa" or "astra-ext"
   */
  constructor(namespace) {
    this._ns = namespace;
    this._db = null;
    this._mem = new Map(); // in-memory fallback if IndexedDB unavailable
    this._ready = this._open();
  }

  async _open() {
    if (typeof indexedDB === "undefined") return;
    return new Promise((resolve, reject) => {
      const req = indexedDB.open(this._ns, DB_VERSION);
      req.onupgradeneeded = (e) => {
        const db = e.target.result;
        if (!db.objectStoreNames.contains("snippets")) {
          db.createObjectStore("snippets", { keyPath: "id" });
        }
      };
      req.onsuccess = (e) => { this._db = e.target.result; resolve(); };
      req.onerror = () => { resolve(); }; // fallback to in-memory
    });
  }

  /**
   * Get snippet state. Returns default (pending) if not yet stored.
   * @param {string} snippetId
   * @returns {{ id: string, status: string, comment: string }}
   */
  get(snippetId) {
    // Sync read from in-memory cache (async writes keep it warm).
    return this._mem.get(snippetId) ?? { id: snippetId, status: "pending", comment: "" };
  }

  /**
   * Persist snippet state (async, non-blocking).
   * @param {string} snippetId
   * @param {{ status?: string, comment?: string }} patch
   */
  set(snippetId, patch) {
    const current = this.get(snippetId);
    const next = { ...current, ...patch, id: snippetId };
    this._mem.set(snippetId, next);
    this._persist(next);
  }

  async _persist(record) {
    await this._ready;
    if (!this._db) return;
    const tx = this._db.transaction("snippets", "readwrite");
    tx.objectStore("snippets").put(record);
  }

  /**
   * Load all saved states for a list of snippet IDs into the memory cache.
   * Call once after receiving a diff to warm the cache.
   * @param {string[]} ids
   */
  async warmCache(ids) {
    await this._ready;
    if (!this._db) return;
    return new Promise((resolve) => {
      const tx = this._db.transaction("snippets", "readonly");
      const store = tx.objectStore("snippets");
      let pending = ids.length;
      if (!pending) { resolve(); return; }
      for (const id of ids) {
        const req = store.get(id);
        req.onsuccess = (e) => {
          if (e.target.result) this._mem.set(id, e.target.result);
          if (--pending === 0) resolve();
        };
        req.onerror = () => { if (--pending === 0) resolve(); };
      }
    });
  }
}
