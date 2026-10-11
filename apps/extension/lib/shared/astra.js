/**
 * shared/lib/astra.js — the single browser-side Astra pipeline.
 *
 * SOURCE OF TRUTH lives in /shared/lib; the copies under
 * apps/pwa/lib/shared/ and apps/extension/lib/shared/ are written by
 * scripts/sync-extension-lib.sh (extensions cannot reference files outside
 * their package, and the PWA must be self-contained for static hosting).
 * Never edit the copies.
 *
 * All review logic (entity mapping, hunk splitting, call edges, review
 * tree, ordering) runs in the Go core compiled to WASM (vendor/core/
 * astra-core.wasm) — the same code the CLI runs natively, parity-tested
 * in core/testdata/parity. This module only:
 *   1. parses GitHub patches into wire hunks,
 *   2. attaches each file's full source and no entities, so the core finds
 *      entities and call edges with the same rules as the CLI,
 *   3. calls astraCore.process() and returns the response.
 *
 * Environment differences are injected through an adapter:
 *   initAstra({
 *     coreBase,            // base URL for vendor/core (wasm_exec.js, astra-core.wasm)
 *     fetchSource(file),   // → full post-image source string or null
 *   })
 */

export function initAstra(adapter) {
  let corePromise = null;
  let lastFiles = null;
  let lastMissing = [];

  async function loadCore() {
    if (corePromise) return corePromise;
    corePromise = (async () => {
      await loadClassicScript(`${adapter.coreBase}/wasm_exec.js`);
      const go = new globalThis.Go();
      // no-cache revalidates, so an upgraded engine never runs against
      // stale cached bytes (a 304 costs nothing when unchanged).
      const resp = await fetch(`${adapter.coreBase}/astra-core.wasm`, { cache: "no-cache" });
      let instance;
      try {
        ({ instance } = await WebAssembly.instantiateStreaming(resp, go.importObject));
      } catch {
        // Some static hosts serve .wasm without the right MIME type.
        const buf = await (await fetch(`${adapter.coreBase}/astra-core.wasm`, { cache: "no-cache" })).arrayBuffer();
        ({ instance } = await WebAssembly.instantiate(buf, go.importObject));
      }
      go.run(instance); // resolves only on exit; astraCore installs at startup
      for (let i = 0; i < 200 && !globalThis.astraCore; i++) {
        await new Promise((r) => setTimeout(r, 10));
      }
      if (!globalThis.astraCore) throw new Error("astra-core.wasm did not install astraCore");
      return globalThis.astraCore;
    })();
    return corePromise;
  }

  /**
   * Build the full review from GitHub REST /pulls/:n/files items.
   * Returns the core response: { snippets, edges, tree, flatOrder }.
   * Snippets arrive in flattened reading order with a `status` field
   * initialised to "pending". `missingSource` lists supported-language
   * files whose full source could not be fetched (they fall back to plain
   * hunks with no call structure).
   */
  async function buildReview(files, order = "pre") {
    const wireFiles = [];
    const missing = [];
    for (const file of files) {
      // REST says "removed"; GitHub's page data says "deleted".
      if (file.status === "removed" || file.status === "deleted") continue;
      const hunks = hunksFromPatch(file.patch ?? "");
      if (hunks.length === 0) continue;
      const lang = detectLanguage(file.filename);

      let source = "";
      if (lang) {
        source = (await adapter.fetchSource(file)) ?? "";
        if (!source) missing.push(file.filename);
      }

      wireFiles.push({
        path: file.filename,
        oldPath: file.previous_filename ?? file.filename,
        language: lang ?? "",
        isNew: file.status === "added",
        isDeleted: false,
        isBinary: false,
        hunks,
        source,
        entities: [],
      });
    }
    lastFiles = wireFiles;
    lastMissing = missing;
    return processFiles(wireFiles, order);
  }

  /** Re-run ordering on the last built review without re-fetching. */
  async function reorderReview(order) {
    if (!lastFiles) throw new Error("no review built yet");
    return processFiles(lastFiles, order);
  }

  async function processFiles(wireFiles, order) {
    const core = await loadCore();
    const resp = JSON.parse(
      core.process(JSON.stringify({ files: wireFiles, options: { order } })),
    );
    if (resp.error) throw new Error(`astra core: ${resp.error}`);
    for (const s of resp.snippets ?? []) s.status = "pending";
    resp.missingSource = lastMissing;
    return resp;
  }

  return { buildReview, reorderReview, loadCore };
}

// ---------------------------------------------------------------------------
// Patch / diff helpers
// ---------------------------------------------------------------------------

/** Parse a GitHub unified patch into wire hunks for the core. */
export function hunksFromPatch(patch) {
  const hunks = [];
  let current = null;
  for (const line of patch.split("\n")) {
    const m = line.match(/^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/);
    if (m) {
      if (current) hunks.push(current);
      current = {
        oldStart: Number(m[1]),
        oldLines: m[2] !== undefined ? Number(m[2]) : 1,
        newStart: Number(m[3]),
        newLines: m[4] !== undefined ? Number(m[4]) : 1,
        lines: [line],
      };
    } else if (current) {
      current.lines.push(line);
    }
  }
  if (current) hunks.push(current);
  return hunks;
}

// ---------------------------------------------------------------------------
// Language detection
// ---------------------------------------------------------------------------

const EXT_LANG = {
  js: "javascript", jsx: "javascript", mjs: "javascript", cjs: "javascript",
  ts: "typescript", tsx: "typescript", mts: "typescript",
  py: "python",
  go: "go",
  java: "java",
  rs: "rust",
};

export function detectLanguage(filename) {
  const ext = filename.split(".").pop()?.toLowerCase();
  return EXT_LANG[ext] ?? null;
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

/** Load a classic (non-module) script once. */
export function loadClassicScript(src) {
  return new Promise((resolve, reject) => {
    if (document.querySelector(`script[src="${src}"]`)) { resolve(); return; }
    const s = document.createElement("script");
    s.src = src;
    s.onload = resolve;
    s.onerror = () => reject(new Error(`Failed to load ${src}`));
    document.head.appendChild(s);
  });
}
