/**
 * content.js — injected into github.com/.../pull/.../files pages
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
  if (!location.pathname.match(/\/pull\/\d+\/changes/)) return;

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
  iframe.setAttribute("sandbox", "allow-scripts allow-same-origin allow-forms");
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
  // Fetch PR diff from GitHub's same-origin page_data endpoints.
  // These are the exact endpoints GitHub's own UI uses to lazy-load diffs.
  // No CORS issues, no API tokens needed — the user's session cookie is
  // automatically included because these are same-origin requests.
  // ---------------------------------------------------------------------------
  let sent = false;

  async function fetchAndSend() {
    if (sent) return;

    try {
      const files = await fetchAllDiffContents();
      await attachSources(files);
      postToPanel({ type: "files", owner, repo, prNumber: Number(prNumber), files });
      sent = true;
    } catch (err) {
      postToPanel({ type: "error", message: err.message });
    }
  }

  // Extract diff data already embedded in the page, then fetch any remaining
  // files that GitHub lazy-loads via page_data/diff_entries.
  async function fetchAllDiffContents() {
    const script = Array.from(document.querySelectorAll('script[type="application/json"]'))
      .find(s => s.textContent.includes("pullRequestsChangesRoute"));
    if (!script) throw new Error("Could not find PR data in page. Try reloading.");

    const route = JSON.parse(script.textContent).payload.pullRequestsChangesRoute;
    const summaries = route.diffSummaries || [];
    const loadedContents = route.diffContents || [];
    const commitOid = loadedContents[0]?.newCommitOid || "";

    // Index already-loaded contents by path.
    const contentsByPath = new Map(loadedContents.map(d => [d.path, d]));

    // Find paths not yet loaded.
    const missingPaths = summaries
      .map(s => s.path)
      .filter(p => !contentsByPath.has(p));

    // Fetch missing paths in batches (GitHub uses ~4 per request).
    const BATCH = 4;
    for (let i = 0; i < missingPaths.length; i += BATCH) {
      const batch = missingPaths.slice(i, i + BATCH);
      const encodedPaths = batch.map(p => encodeURIComponent(encodeURIComponent(p))).join(",");
      const url = `/${owner}/${repo}/pull/${prNumber}/page_data/diff_entries?paths=${encodedPaths}&ctx=%3A%3A&w=0&range=${commitOid}`;
      const resp = await fetch(url, {
        headers: {
          Accept: "application/json",
          "X-Requested-With": "XMLHttpRequest",
          "github-verified-fetch": "true",
          "github-is-react": "true",
        },
        credentials: "same-origin",
      });
      if (!resp.ok) throw new Error(`Failed to load diff entries: ${resp.status}`);
      const entries = await resp.json();
      for (const entry of entries) {
        contentsByPath.set(entry.path, entry);
      }
    }

    headOid = commitOid;

    // Convert GitHub's internal format to the shape Astra's panel expects
    // (mirrors the GitHub REST API response for /pulls/:number/files).
    return summaries.map(summary => {
      const content = contentsByPath.get(summary.path);
      const patch = content
        ? diffLinesToPatch(content.diffLines || [])
        : "";
      return {
        filename: summary.path,
        status: summary.changeType?.toLowerCase() ?? "modified",
        additions: summary.linesAdded ?? 0,
        deletions: summary.linesDeleted ?? 0,
        changes: summary.linesChanged ?? 0,
        patch,
      };
    });
  }

  // Full post-image source per file, so the core can find entities and call
  // edges. Fetched here (github.com origin, same session) because the raw
  // redirect target does not allow the extension's own origin.
  let headOid = "";
  const SOURCE_EXT = /\.(m?[jt]sx?|cjs|py|go|java|rs)$/i;

  async function attachSources(files) {
    const wanted = files.filter((f) => !isRemoved(f) && SOURCE_EXT.test(f.filename));
    let next = 0;
    async function worker() {
      while (next < wanted.length) {
        const f = wanted[next++];
        const path = f.filename.split("/").map(encodeURIComponent).join("/");
        try {
          const resp = await fetch(`/${owner}/${repo}/raw/${headOid}/${path}`, { credentials: "same-origin" });
          if (resp.ok) f.source = await resp.text();
        } catch {
          // Left without source: the panel reports it and shows plain hunks.
        }
      }
    }
    if (headOid) await Promise.all(Array.from({ length: 6 }, worker));
  }

  // Convert GitHub's diffLines array into a unified diff patch string,
  // matching the format the GitHub REST API returns in the `patch` field.
  function diffLinesToPatch(diffLines) {
    return diffLines.map(line => {
      if (line.type === "ADDITION") return `+${line.text.slice(1)}`;
      if (line.type === "DELETION") return `-${line.text.slice(1)}`;
      if (line.type === "HUNK")     return line.text;
      // CONTEXT lines have a leading space in line.text already
      return line.text;
    }).join("\n");
  }

  function isRemoved(f) {
    return f.status === "removed" || f.status === "deleted";
  }

  function postToPanel(msg) {
    iframe.contentWindow?.postMessage(msg, "*");
  }
})();
