/**
 * lib/ast.js — PWA adapter for the shared Astra pipeline.
 *
 * All review logic (entity mapping, hunk splitting, call edges, review
 * tree, ordering) runs in the Go core (vendor/core/astra-core.wasm) —
 * identical to the CLI. This file only wires PWA-specific paths and
 * fetching into shared/astra.js (a synced copy of /shared/lib/astra.js).
 */

import { initAstra, loadClassicScript } from "./shared/astra.js";

const GRAMMARS = "../../vendor/grammars";
const CORE = "../../vendor/core";

async function initParser() {
  // web-tree-sitter is vendored as a classic script exposing window.TreeSitter.
  await loadClassicScript(`${GRAMMARS}/web-tree-sitter.js`);
  await window.TreeSitter.init({ locateFile: () => `${GRAMMARS}/tree-sitter.wasm` });
  const ts = window.TreeSitter;
  return {
    newParser: () => new ts.Parser(),
    loadLanguage: (url) => ts.Language.load(url),
  };
}

async function fetchSource(file) {
  try {
    const url = rawContentURL(file.raw_url);
    if (!url) return null;
    const resp = await fetch(url);
    return resp.ok ? await resp.text() : null;
  } catch {
    return null;
  }
}

// GitHub's raw_url (github.com/o/r/raw/<sha>/<path>) redirects without CORS
// headers, so browsers block it. raw.githubusercontent.com serves the same
// file with CORS open, and does not count against the API rate limit.
export function rawContentURL(rawURL) {
  const m = rawURL?.match(/^https:\/\/github\.com\/([^/]+)\/([^/]+)\/raw\/([0-9a-f]+)\/(.+)$/);
  if (!m) return null;
  const path = decodeURIComponent(m[4]).split("/").map(encodeURIComponent).join("/");
  return `https://raw.githubusercontent.com/${m[1]}/${m[2]}/${m[3]}/${path}`;
}

export const { buildReview, reorderReview } = initAstra({
  coreBase: CORE,
  grammarURL: (wasm) => `${GRAMMARS}/${wasm}`,
  initParser,
  fetchSource,
});
