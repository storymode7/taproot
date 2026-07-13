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
  if (!file.raw_url) return null;
  try {
    const resp = await fetch(file.raw_url);
    return resp.ok ? await resp.text() : null;
  } catch {
    return null;
  }
}

export const { buildReview, reorderReview } = initAstra({
  coreBase: CORE,
  grammarURL: (wasm) => `${GRAMMARS}/${wasm}`,
  initParser,
  fetchSource,
});
