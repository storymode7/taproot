/**
 * panel.js — Extension side panel
 *
 * Receives diff data from content.js via postMessage, then runs the same
 * review engine and rendering as the PWA.
 */

import { buildReview, reorderReview } from "../lib/pipeline-ext.js";
import { renderReview } from "../lib/shared/render.js";
import hljs from "../vendor/highlight.min.js";
import { StateStore } from "../lib/shared/state.js";

const store = new StateStore("astra-ext");
let currentReview = null;
let currentSnippets = [];

// ---------------------------------------------------------------------------
// Receive diff data from content.js
// ---------------------------------------------------------------------------
window.addEventListener("message", async (e) => {
  if (e.source !== window.parent || e.origin !== "https://github.com") return;
  if (e.data?.type === "files") {
    await handleFiles(e.data);
  } else if (e.data?.type === "error") {
    setStatus(`Error: ${e.data.message}`);
  }
});

// The core orders callers first (tree pre-order) or callees first (post-order).
function coreOrder(order) {
  return order === "callees-first" ? "post" : "pre";
}

async function handleFiles({ owner, repo, prNumber, files }) {
  setStatus("Building the review…");
  try {
    const order = document.getElementById("order-select").value;
    // The Go core (WASM) builds snippets, call edges, and the review tree.
    currentReview = await buildReview(files, coreOrder(order));
    currentSnippets = currentReview.snippets ?? [];
    await store.load(`${owner}/${repo}#${prNumber}`, currentSnippets);
    render();
    setStatus("");
  } catch (err) {
    setStatus(`Parse error: ${err.message}`);
    console.error("[astra]", err);
  }
}

function render() {
  renderReview(document.getElementById("snippet-list"), currentReview, store, {
    highlight,
    commentHint: "Saved in this browser only — the extension can't post to GitHub.",
  });
}

function highlight(code, language) {
  if (!language || !hljs.getLanguage(language)) return null;
  return hljs.highlight(code, { language, ignoreIllegals: true }).value;
}

document.getElementById("order-select").addEventListener("change", async (e) => {
  if (!currentSnippets.length) return;
  currentReview = await reorderReview(currentReview, coreOrder(e.target.value));
  currentSnippets = currentReview.snippets ?? [];
  render();
});

document.getElementById("btn-close").addEventListener("click", () => {
  window.parent.postMessage({ type: "astra-close" }, "https://github.com");
});

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------
// The status region stays in the page, empty when idle, so screen
// readers announce what is put in it.
function setStatus(msg) {
  document.getElementById("panel-status").textContent = msg;
}
