/**
 * panel.js — Extension side panel
 *
 * Receives diff data from content.js via postMessage, then runs the same
 * AST + ordering + rendering pipeline as the PWA.
 *
 * Vendored WASM files are referenced via chrome.runtime.getURL() so
 * web-tree-sitter can locate them in the extension's resource space.
 */

import { buildSnippets } from "../lib/ast-ext.js";
import { buildSymbolGraph, orderSnippets } from "../lib/ordering.js";
import { renderSnippetList } from "../lib/render.js";
import { StateStore } from "../lib/state.js";
import { syncFileViewed, submitReview } from "../lib/github-sync.js";
import { GitHubSessionAdapter } from "../lib/github-session-adapter.js";

const store = new StateStore("astra-ext");
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

async function handleFiles({ owner, repo, prNumber, files }) {
  adapter = new GitHubSessionAdapter(owner, repo, prNumber);
  setStatus("Parsing AST & ordering…");
  try {
    const snippets = await buildSnippets(files);
    const graph = buildSymbolGraph(snippets);
    const preset = document.getElementById("preset-select").value;
    currentSnippets = orderSnippets(snippets, graph, preset);
    await store.warmCache(currentSnippets.map((s) => s.id));
    render();
    setStatus("");
  } catch (err) {
    setStatus(`Parse error: ${err.message}`);
    console.error("[astra]", err);
  }
}

function render() {
  const preset = document.getElementById("preset-select").value;
  const graph = buildSymbolGraph(currentSnippets);
  const ordered = orderSnippets(currentSnippets, graph, preset);
  renderSnippetList(document.getElementById("snippet-list"), ordered, store, onSnippetStateChange);
}

/** Called by renderSnippetList when a snippet's state changes. */
async function onSnippetStateChange(snippet) {
  if (!adapter) return;
  await syncFileViewed(adapter, currentSnippets, store, snippet.filePath);
}

document.getElementById("preset-select").addEventListener("change", () => {
  if (currentSnippets.length) render();
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
