/**
 * lib/ast-ext.js — extension adapter for the shared Astra pipeline.
 *
 * All review logic (entity mapping, hunk splitting, call edges, review
 * tree, ordering) runs in the Go core (vendor/core/astra-core.wasm) —
 * identical to the CLI and PWA. This file only wires extension-specific
 * paths (chrome.runtime.getURL) and session fetching into
 * shared/astra.js (a synced copy of /shared/lib/astra.js).
 */

import { Parser, Language } from "../vendor/web-tree-sitter.js";
import { initAstra } from "./shared/astra.js";

function vendorUrl(path) {
  return chrome.runtime.getURL(`vendor/${path}`);
}

let parserInit = null;

async function initParser() {
  if (!parserInit) {
    parserInit = Parser.init({
      locateFile: () => vendorUrl("tree-sitter.wasm"),
    });
  }
  await parserInit;
  return {
    newParser: () => new Parser(),
    loadLanguage: (url) => Language.load(url),
  };
}

async function fetchSource(file) {
  if (!file.raw_url) return null;
  try {
    // Same-origin fetch rides the user's existing GitHub session.
    const resp = await fetch(file.raw_url, { credentials: "include" });
    return resp.ok ? await resp.text() : null;
  } catch {
    return null;
  }
}

export const { buildReview, reorderReview } = initAstra({
  coreBase: vendorUrl("core"),
  grammarURL: (wasm) => vendorUrl(wasm),
  initParser,
  fetchSource,
});
