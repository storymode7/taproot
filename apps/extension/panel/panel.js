/**
 * panel.js — Extension side panel
 *
 * Receives diff data from content.js via postMessage, then runs the same
 * AST + ordering + rendering pipeline as the PWA.
 *
 * Vendored WASM files are referenced via chrome.runtime.getURL() so
 * web-tree-sitter can locate them in the extension's resource space.
 */

import { buildReview, reorderReview } from "../lib/ast-ext.js";
import { renderReview } from "../lib/shared/render.js";
import hljs from "../vendor/highlight.min.js";
import { StateStore } from "../lib/state.js";
import { syncFileViewed, submitReview } from "../lib/github-sync.js";
import { GitHubSessionAdapter } from "../lib/github-session-adapter.js";

const store = new StateStore("astra-ext");
let currentReview = null;
let currentSnippets = [];
let adapter = null; // Initialized when we learn owner/repo/prNumber

// ---------------------------------------------------------------------------
// Receive diff data from content.js
// ---------------------------------------------------------------------------
window.addEventListener("message", async (e) => {
  if (e.data?.type === "files") {
    await handleFiles(e.data);
  } else if (e.data?.type === "error") {
    setStatus(`Error: ${e.data.message}`);
  }
});

// The core orders top-down (tree pre-order) or bottom-up (post-order);
// clustering is inherent in the tree, so the clusters preset maps to pre.
function presetToOrder(preset) {
  return preset === "bottom-up" ? "post" : "pre";
}

async function handleFiles({ owner, repo, prNumber, files }) {
  adapter = new GitHubSessionAdapter(owner, repo, prNumber);
  setStatus("Parsing AST & ordering…");
  try {
    const preset = document.getElementById("preset-select").value;
    // The Go core (WASM) builds snippets, call edges, and the review tree.
    currentReview = await buildReview(files, presetToOrder(preset));
    currentSnippets = currentReview.snippets ?? [];
    await store.warmCache(currentSnippets.map((s) => s.id));
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
    onStateChange: onSnippetStateChange,
    commentHint: "Posted to the PR when you press Submit.",
  });
}

function highlight(code, language) {
  if (!language || !hljs.getLanguage(language)) return null;
  return hljs.highlight(code, { language, ignoreIllegals: true }).value;
}

/** Called by renderReview when a snippet's review status changes. */
async function onSnippetStateChange(snippet) {
  if (!adapter) return;
  await syncFileViewed(adapter, currentSnippets, store, snippet.filePath);
}

document.getElementById("preset-select").addEventListener("change", async (e) => {
  if (!currentSnippets.length) return;
  currentReview = await reorderReview(presetToOrder(e.target.value));
  currentSnippets = currentReview.snippets ?? [];
  render();
});

document.getElementById("btn-close").addEventListener("click", () => {
  window.parent.postMessage({ type: "astra-close" }, "*");
});

document.getElementById("btn-submit").addEventListener("click", async () => {
  if (!adapter || !currentSnippets.length) return;
  const btn = document.getElementById("btn-submit");
  btn.disabled = true;
  setStatus("Submitting review…");
  try {
    await submitReview(adapter, currentSnippets, store, "COMMENT");
    setStatus("Review submitted ✓");
    setTimeout(() => setStatus(""), 3000);
  } catch (err) {
    setStatus(`Submit failed: ${err.message}`);
    console.error("[astra]", err);
  } finally {
    btn.disabled = false;
  }
});

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------
function setStatus(msg) {
  const el = document.getElementById("panel-status");
  el.textContent = msg;
  el.hidden = !msg;
}
