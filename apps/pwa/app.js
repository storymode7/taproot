/**
 * app.js — Astra PWA entry point
 *
 * Vanilla JS ES module. No framework, no build step, no npm.
 * All dependencies are vendored WASM files loaded from /vendor/.
 *
 * Flow:
 *   1. User pastes a public GH PR URL → parsePRUrl()
 *   2. Fetch diff from api.github.com (unauthenticated)
 *   3. Load tree-sitter WASM + grammar WASM for each changed language
 *   4. Build snippets (hunk → AST node) + symbol graph
 *   5. Order snippets by selected preset
 *   6. Render snippet cards
 */

import { parsePRUrl, fetchPRFiles } from "./lib/github.js";
import { loadTreeSitter, buildSnippets } from "./lib/ast.js";
import { buildSymbolGraph, orderSnippets } from "./lib/ordering.js";
import { renderSnippetList } from "./lib/render.js";
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
let currentSnippets = [];
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
async function loadReview({ owner, repo, prNumber }) {
  const { meta, files } = await fetchPRFiles(owner, repo, prNumber);
  currentMeta = meta;

  document.getElementById("review-title").textContent =
    `${owner}/${repo} #${prNumber}`;

  // Load tree-sitter and build snippets for each file
  const snippets = await buildSnippets(files);

  // Build cross-file symbol graph and order by current preset
  const graph = buildSymbolGraph(snippets);
  const preset = document.getElementById("preset-select").value;
  currentSnippets = orderSnippets(snippets, graph, preset);

  renderSnippetList(
    document.getElementById("snippet-list"),
    currentSnippets,
    store,
  );

  showScreen("review");
}

// ---------------------------------------------------------------------------
// Preset change → re-order without re-fetching
// ---------------------------------------------------------------------------
document.getElementById("preset-select").addEventListener("change", (e) => {
  if (!currentSnippets.length) return;
  // Re-order in place; snippets array already has graph metadata attached
  const graph = buildSymbolGraph(currentSnippets);
  const ordered = orderSnippets(currentSnippets, graph, e.target.value);
  renderSnippetList(
    document.getElementById("snippet-list"),
    ordered,
    store,
  );
});

// ---------------------------------------------------------------------------
// Back buttons
// ---------------------------------------------------------------------------
document.getElementById("btn-back-review").addEventListener("click", () => showScreen("input"));
document.getElementById("btn-back-ratelimit").addEventListener("click", () => showScreen("input"));
