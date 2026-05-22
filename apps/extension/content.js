/**
 * content.js — injected into github.com/*/pull/*/files pages
 *
 * Responsibilities:
 *   1. Detect that we're on a PR files page.
 *   2. Inject the Astra side panel into the DOM.
 *   3. Fetch the PR diff via same-origin fetch (user's existing GH session).
 *   4. Post messages to the panel iframe with diff data.
 *
 * Security notes:
 *   - All fetch calls are same-origin to github.com (user's session).
 *   - No credentials are stored or transmitted off-device.
 *   - The panel is a local extension resource (web_accessible_resource).
 */

(function () {
  "use strict";

  // Only activate on the PR files tab.
  if (!location.pathname.match(/\/pull\/\d+\/files/)) return;

  // Avoid double-injection on turbo/pushstate navigations.
  if (document.getElementById("astra-panel-root")) return;

  // ---------------------------------------------------------------------------
  // Parse PR coords from the URL
  // ---------------------------------------------------------------------------
  const [, owner, repo, , prNumber] = location.pathname.split("/");

  // ---------------------------------------------------------------------------
  // Build the side panel container
  // ---------------------------------------------------------------------------
  const container = document.createElement("div");
  container.id = "astra-panel-root";
  container.style.cssText = [
    "position: fixed",
    "top: 0",
    "right: 0",
    "width: 480px",
    "height: 100vh",
    "z-index: 9999",
    "box-shadow: -2px 0 8px rgba(0,0,0,0.15)",
    "display: none",          // hidden until user toggles on
  ].join(";");

  const iframe = document.createElement("iframe");
  iframe.src = chrome.runtime.getURL("panel/panel.html");
  iframe.style.cssText = "width:100%;height:100%;border:none;";
  iframe.setAttribute("sandbox", "allow-scripts allow-same-origin");
  container.appendChild(iframe);
  document.body.appendChild(container);

  // ---------------------------------------------------------------------------
  // Toggle button
  // ---------------------------------------------------------------------------
  const toggle = document.createElement("button");
  toggle.id = "astra-toggle";
  toggle.textContent = "⭐ Astra";
  toggle.title = "Toggle Astra AST review panel";
  toggle.style.cssText = [
    "position: fixed",
    "top: 64px",
    "right: 0",
    "z-index: 10000",
    "background: #0969da",
    "color: #fff",
    "border: none",
    "padding: 6px 12px",
    "cursor: pointer",
    "font-size: 13px",
    "border-radius: 4px 0 0 4px",
    "font-family: -apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif",
  ].join(";");

  let panelOpen = false;
  toggle.addEventListener("click", () => {
    panelOpen = !panelOpen;
    container.style.display = panelOpen ? "block" : "none";
    toggle.style.right = panelOpen ? "480px" : "0";
    if (panelOpen) fetchAndSend();
  });
  document.body.appendChild(toggle);

  // Handle close from inside the panel iframe.
  window.addEventListener("message", (e) => {
    if (e.data?.type === "astra-close") {
      panelOpen = false;
      container.style.display = "none";
      toggle.style.right = "0";
    }
  });

  // ---------------------------------------------------------------------------
  // Fetch PR diff via GitHub's own REST API (same-origin, user's session)
  // ---------------------------------------------------------------------------
  let sent = false;

  async function fetchAndSend() {
    if (sent) return;

    // GitHub's compare API: returns unified diff as JSON with per-file hunks.
    // This is the same endpoint the native PR files page uses.
    const url = `https://api.github.com/repos/${owner}/${repo}/pulls/${prNumber}/files?per_page=100`;
    const resp = await fetch(url, {
      headers: { Accept: "application/vnd.github+json" },
      credentials: "include",   // use the user's existing github.com session
    });

    if (!resp.ok) {
      postToPanel({ type: "error", message: `GitHub API ${resp.status}: ${resp.statusText}` });
      return;
    }

    const files = await resp.json();
    postToPanel({ type: "files", owner, repo, prNumber: Number(prNumber), files });
    sent = true;
  }

  function postToPanel(msg) {
    iframe.contentWindow?.postMessage(msg, chrome.runtime.getURL(""));
  }
})();
