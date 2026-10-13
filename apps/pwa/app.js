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
 *   5. Order snippets in the selected reading order
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
const loadingMsg = document.querySelector(".loading-msg");

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
      const say = (text) => { if (navigation === nav) loadingMsg.textContent = text; };
      say("Fetching the PR…");
      showScreen("loading");
      next = await loadReview(url, parsePRUrl(url), nav.signal, say);
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
    if (err.rateLimited) {
      showRateLimit(err.retryAt);
    } else if (err.status === 404) {
      showInput("PR not found. If the repository is private, use the Chrome extension: the PWA reads public repositories only.");
    } else {
      showInput(err.network ? err.message : `Error: ${err.message}`);
    }
  }
}

function showInput(error = "") {
  errEl.textContent = error;
  errEl.hidden = !error;
  showScreen("input");
  urlInput.focus();
}

function showRateLimit(retryAt) {
  const mins = retryAt && Math.ceil((retryAt - Date.now()) / 60000);
  const at = retryAt && new Date(retryAt).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  document.getElementById("ratelimit-when").textContent = mins > 0
    ? `Try again after ${at}, in ${mins} minute${mins === 1 ? "" : "s"}.`
    : "The limit resets within an hour.";
  showScreen("ratelimit");
  document.getElementById("btn-back-ratelimit").focus();
}

// ---------------------------------------------------------------------------
// Core review flow
// ---------------------------------------------------------------------------
// The core orders callers first (tree pre-order) or callees first (post-order).
function coreOrder(order) {
  return order === "callees-first" ? "post" : "pre";
}

async function loadReview(url, { owner, repo, prNumber }, signal, say) {
  const { meta, files } = await fetchPRFiles(owner, repo, prNumber, {
    signal,
    onPage: (n, pages) => say(`Fetching the file list (page ${n} of ${pages})…`),
  });

  // The Go core (WASM) builds snippets, call edges, and the review tree.
  const order = document.getElementById("order-select").value;
  const review = await buildReview(files, coreOrder(order), {
    signal,
    onProgress: (done, total) =>
      say(done < total ? `Fetching sources (${done} of ${total})…` : "Building the review…"),
  });
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
// Order change → re-order without re-fetching
// ---------------------------------------------------------------------------
document.getElementById("order-select").addEventListener("change", async (e) => {
  const was = shown;
  if (!was) return;
  const review = await reorderReview(was.review, coreOrder(e.target.value));
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
// Back and Cancel buttons
// ---------------------------------------------------------------------------
document.getElementById("btn-back-review").addEventListener("click", () => navigate(null));
document.getElementById("btn-back-ratelimit").addEventListener("click", () => navigate(null));
document.getElementById("btn-cancel-loading").addEventListener("click", () => navigate(null));

window.addEventListener("popstate", route);
route();
