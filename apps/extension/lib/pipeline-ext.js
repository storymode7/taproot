/**
 * lib/pipeline-ext.js — extension adapter for the shared Astra pipeline.
 *
 * All review logic (entity mapping, hunk splitting, call edges, review
 * tree, ordering) runs in the Go core (vendor/core/astra-core.wasm) —
 * identical to the CLI and PWA. This file only wires extension-specific
 * paths (chrome.runtime.getURL) and session fetching into
 * shared/astra.js (a synced copy of /shared/lib/astra.js).
 */

import { initAstra } from "./shared/astra.js";

function vendorUrl(path) {
  return chrome.runtime.getURL(`vendor/${path}`);
}

// content.js fetches each file's source on github.com (the user's session)
// and sends it along with the diff.
async function fetchSource(file) {
  return file.source ?? null;
}

export const { buildReview, reorderReview } = initAstra({
  coreBase: vendorUrl("core"),
  fetchSource,
});
