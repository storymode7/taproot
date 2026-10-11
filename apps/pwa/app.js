/**
 * app.js — Astra PWA entry point
 *
 * Vanilla JS ES module. No framework, no build step, no npm.
 * The review engine is vendored WASM in vendor/core/, next to this file.
 *
 * Flow:
 *   1. User pastes a public GH PR URL → parsePRUrl(); the PR goes into the
 *      page URL (?pr=…), so Back, Forward and reload stay in the app
 *   2. Fetch diff from api.github.com (unauthenticated)
 *   3. Fetch each changed file's full source (raw.githubusercontent.com)
 *   4. Build snippets (hunk → AST node) + symbol graph
 *   5. Order snippets by selected preset
 *   6. Render the call-tree sidebar and diff cards
 */

import { parsePRUrl, fetchPRFiles } from "./lib/github.js";
import { buildReview, reorderReview } from "./lib/ast.js";
import { renderReview } from "./lib/shared/render.js";
import { StateStore } from "./lib/shared/state.js";

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
// The review on the review screen: { url, title, scope, review, notice }.
let shown = null;
// The review whose state the store holds, and the navigation in progress.
let storeScope = null;
let navigation = null;
const store = new StateStore("astra-pwa");
const urlInput = document.getElementById("pr-url");
const errEl = document.getElementById("input-error");

// ---------------------------------------------------------------------------
// Navigation: the PR under review lives in the page URL (?pr=…)
// ---------------------------------------------------------------------------
document.getElementById("pr-form").addEventListener("submit", (e) => {
  e.preventDefault();
  let coords;
  try {
    coords = parsePRUrl(urlInput.value.trim());
  } catch (err) {
    showInput(err.message);
    return;
  }
  const { owner, repo, prNumber } = coords;
  navigate(`https://github.com/${owner}/${repo}/pull/${prNumber}`);
});

function navigate(pr) {
  history.pushState(null, "", pr ? `?${new URLSearchParams({ pr })}` : location.pathname);
  route();
}

async function route() {
  navigation?.abort();
  const nav = (navigation = new AbortController());
  const url = new URLSearchParams(location.search).get("pr");
  if (!url) {
    showInput();
    return;
  }
  urlInput.value = url;
  try {
    let next = shown?.url === url ? shown : null;
    if (!next) {
      showScreen("loading");
      next = await loadReview(url, parsePRUrl(url), nav.signal);
      if (navigation !== nav) return;
    }
    // Another PR's load may have switched the store since this one showed.
    if (next !== shown || storeScope !== next.scope) {
      storeScope = next.scope;
      await store.load(next.scope, next.review.snippets ?? []);
      if (navigation !== nav) return;
      shown = next;
      render();
    }
    showScreen("review");
  } catch (err) {
    if (navigation !== nav) return;
    if (err.status === 403 || err.status === 429) {
      showScreen("ratelimit");
    } else {
      showInput(`Error: ${err.message}`);
    }
  }
}

function showInput(error = "") {
  errEl.textContent = error;
  errEl.hidden = !error;
  showScreen("input");
}

// ---------------------------------------------------------------------------
// Core review flow
// ---------------------------------------------------------------------------
// The core orders top-down (tree pre-order) or bottom-up (post-order);
// clustering is inherent in the tree, so the clusters preset maps to pre.
function presetToOrder(preset) {
  return preset === "bottom-up" ? "post" : "pre";
}

async function loadReview(url, { owner, repo, prNumber }, signal) {
  const { meta, files } = await fetchPRFiles(owner, repo, prNumber, { signal });

  // The Go core (WASM) builds snippets, call edges, and the review tree.
  const preset = document.getElementById("preset-select").value;
  const review = await buildReview(files, presetToOrder(preset), { signal });
  return {
    url,
    title: `${owner}/${repo} #${prNumber}`,
    scope: `${owner}/${repo}#${prNumber}`,
    review,
    notice: files.length < meta.changedFiles
      ? `Showing the first ${files.length} of ${meta.changedFiles} changed files, as many as GitHub lists. Review the rest on GitHub.`
      : "",
  };
}

// ---------------------------------------------------------------------------
// Preset change → re-order without re-fetching
// ---------------------------------------------------------------------------
document.getElementById("preset-select").addEventListener("change", async (e) => {
  const was = shown;
  if (!was) return;
  const review = await reorderReview(was.review, presetToOrder(e.target.value));
  if (shown !== was) return;
  was.review = review;
  render();
});

function render() {
  document.getElementById("review-title").textContent = shown.title;
  renderReview(document.getElementById("snippet-list"), shown.review, store, {
    commentHint: "Saved in this browser only — the PWA can't post to GitHub.",
    notice: shown.notice,
  });
}

// ---------------------------------------------------------------------------
// Back buttons
// ---------------------------------------------------------------------------
document.getElementById("btn-back-review").addEventListener("click", () => navigate(null));
document.getElementById("btn-back-ratelimit").addEventListener("click", () => navigate(null));

window.addEventListener("popstate", route);
route();
