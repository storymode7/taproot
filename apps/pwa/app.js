/**
 * app.js — Astra PWA entry point
 *
 * Vanilla JS ES module. No framework, no build step, no npm.
 * All dependencies are vendored WASM files loaded from /vendor/.
 *
 * Flow:
 *   1. User pastes a public GH PR URL → parsePRUrl()
 *   2. Fetch diff from api.github.com (unauthenticated)
 *   3. Fetch each changed file's full source (raw.githubusercontent.com)
 *   4. Build snippets (hunk → AST node) + symbol graph
 *   5. Order snippets by selected preset
 *   6. Render the call-tree sidebar and diff cards
 */

import { parsePRUrl, fetchPRFiles } from "./lib/github.js";
import { buildReview, reorderReview } from "./lib/ast.js";
import { renderReview } from "./lib/shared/render.js";
import { StateStore } from "./lib/state.js";

// ---------------------------------------------------------------------------
// Screen management
// ---------------------------------------------------------------------------
const screens = {
  input:     document.getElementById("screen-input"),
  loading:   document.getElementById("screen-loading"),
  ratelimit: document.getElementById("screen-ratelimit"),
  review:    document.getElementById("screen-review"),
};

function showScreen(name) {
  for (const [k, el] of Object.entries(screens)) {
    el.classList.toggle("active", k === name);
  }
}

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------
let currentReview = null;
let currentMeta = null;
const store = new StateStore("astra-pwa");

// ---------------------------------------------------------------------------
// PR form submit
// ---------------------------------------------------------------------------
document.getElementById("pr-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const raw = document.getElementById("pr-url").value.trim();
  const errEl = document.getElementById("input-error");
  errEl.hidden = true;

  let coords;
  try {
    coords = parsePRUrl(raw);
  } catch (err) {
    errEl.textContent = err.message;
    errEl.hidden = false;
    return;
  }

  showScreen("loading");
  try {
    await loadReview(coords);
  } catch (err) {
    if (err.status === 403 || err.status === 429) {
      showScreen("ratelimit");
    } else {
      errEl.textContent = `Error: ${err.message}`;
      errEl.hidden = false;
      showScreen("input");
    }
  }
});

// ---------------------------------------------------------------------------
// Core review flow
// ---------------------------------------------------------------------------
// The core orders top-down (tree pre-order) or bottom-up (post-order);
// clustering is inherent in the tree, so the clusters preset maps to pre.
function presetToOrder(preset) {
  return preset === "bottom-up" ? "post" : "pre";
}

async function loadReview({ owner, repo, prNumber }) {
  const { meta, files } = await fetchPRFiles(owner, repo, prNumber);
  currentMeta = meta;

  document.getElementById("review-title").textContent =
    `${owner}/${repo} #${prNumber}`;

  // The Go core (WASM) builds snippets, call edges, and the review tree.
  const preset = document.getElementById("preset-select").value;
  currentReview = await buildReview(files, presetToOrder(preset));
  await store.warmCache((currentReview.snippets ?? []).map((s) => s.id));
  render();
  showScreen("review");
}

// ---------------------------------------------------------------------------
// Preset change → re-order without re-fetching
// ---------------------------------------------------------------------------
document.getElementById("preset-select").addEventListener("change", async (e) => {
  if (!currentReview) return;
  currentReview = await reorderReview(presetToOrder(e.target.value));
  render();
});

function render() {
  renderReview(document.getElementById("snippet-list"), currentReview, store, {
    commentHint: "Saved in this browser only — the PWA can't post to GitHub.",
  });
}

// ---------------------------------------------------------------------------
// Back buttons
// ---------------------------------------------------------------------------
document.getElementById("btn-back-review").addEventListener("click", () => showScreen("input"));
document.getElementById("btn-back-ratelimit").addEventListener("click", () => showScreen("input"));
