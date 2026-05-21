/**
 * panel.js — Extension side panel
 *
 * Receives diff data from content.js via postMessage, then runs the same
 * AST + ordering + rendering pipeline as the PWA.
 *
 * Vendored WASM files are referenced via chrome.runtime.getURL() so
 * web-tree-sitter can locate them in the extension's resource space.
 */

import { buildSnippets, setVendorBase } from "../../vendor/grammars/ast-ext.js";
import { buildSymbolGraph, orderSnippets } from "../../apps/pwa/lib/ordering.js";
import { renderSnippetList } from "../../apps/pwa/lib/render.js";
import { StateStore } from "../../apps/pwa/lib/state.js";

// Tell the AST module where to find WASM files (extension URLs differ from PWA).
setVendorBase(chrome.runtime.getURL("vendor/"));

const store = new StateStore("astra-ext");
let currentSnippets = [];

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
  renderSnippetList(document.getElementById("snippet-list"), ordered, store);
}

document.getElementById("preset-select").addEventListener("change", () => {
  if (currentSnippets.length) render();
});

document.getElementById("btn-close").addEventListener("click", () => {
  window.parent.postMessage({ type: "astra-close" }, "*");
});

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------
function setStatus(msg) {
  const el = document.getElementById("panel-status");
  el.textContent = msg;
  el.hidden = !msg;
}
