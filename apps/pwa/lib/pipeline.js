/**
 * lib/pipeline.js — PWA adapter for the shared Astra pipeline.
 *
 * All review logic (entity mapping, hunk splitting, call edges, review
 * tree, ordering) runs in the Go core (vendor/core/astra-core.wasm) —
 * identical to the CLI. This file only wires PWA-specific paths and
 * fetching into shared/astra.js (a synced copy of /shared/lib/astra.js).
 */

import { initAstra } from "./shared/astra.js";

// Relative to the page: the PWA ships its own copy (scripts/sync-extension-lib.sh).
const CORE = "vendor/core";

// A source that takes longer is left out, so one stalled request cannot
// hold up the review; the file then shows as plain hunks.
const SOURCE_TIMEOUT_MS = 15000;

async function fetchSource(file, signal) {
  try {
    const url = rawContentURL(file.raw_url);
    if (!url) return null;
    const timeout = AbortSignal.timeout(SOURCE_TIMEOUT_MS);
    const resp = await fetch(url, { signal: signal ? AbortSignal.any([signal, timeout]) : timeout });
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
  fetchSource,
});
