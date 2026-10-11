/**
 * content.js — injected into github.com pages
 *
 * Responsibilities:
 *   1. Detect that we're on a PR files page, including after GitHub's
 *      in-page navigation.
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

  // The page data embedded in the document describes the path it was
  // loaded at; after an in-page navigation it can belong to another page.
  const loadedPath = location.pathname;

  // The PR files page in view: its coordinates, toggle and panel.
  let view = null;

  // GitHub switches pages without a new document (Turbo, React routes), so
  // the script re-checks the URL on every navigation. Turbo can also swap
  // <body> after the URL changes, removing the toggle and the panel.
  function sync() {
    const m = location.pathname.match(/^\/([^/]+)\/([^/]+)\/pull\/(\d+)\/changes/);
    const path = m ? location.pathname : null;
    if (view && (view.path !== path || !view.toggle.isConnected || !view.container.isConnected)) {
      view.toggle.remove();
      view.container.remove();
      view = null;
    }
    if (path && !view) view = mount({ path, owner: m[1], repo: m[2], prNumber: Number(m[3]) });
  }

  function mount(pr) {
    // A restored page snapshot can bring back copies without listeners.
    document.querySelectorAll("#astra-toggle, #astra-panel-root").forEach((el) => el.remove());

    // -------------------------------------------------------------------------
    // Build the side panel container
    // -------------------------------------------------------------------------
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
    const loaded = new Promise((resolve) => iframe.addEventListener("load", resolve, { once: true }));
    iframe.src = chrome.runtime.getURL("panel/panel.html");
    iframe.style.cssText = "width:100%;height:100%;border:none;";
    iframe.setAttribute("sandbox", "allow-scripts allow-same-origin allow-forms");
    container.appendChild(iframe);
    document.body.appendChild(container);

    // -------------------------------------------------------------------------
    // Toggle button
    // -------------------------------------------------------------------------
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
    document.body.appendChild(toggle);

    const v = { ...pr, container, iframe, loaded, toggle, open: false, sent: false };
    toggle.addEventListener("click", () => setOpen(v, !v.open));
    return v;
  }

  function setOpen(v, open) {
    v.open = open;
    v.container.style.display = open ? "block" : "none";
    v.toggle.style.right = open ? "480px" : "0";
    if (open) fetchAndSend(v);
  }

  // Handle close from inside the panel iframe.
  window.addEventListener("message", (e) => {
    if (view && e.data?.type === "astra-close") setOpen(view, false);
  });

  // ---------------------------------------------------------------------------
  // Fetch PR diff from GitHub's same-origin page_data endpoints.
  // These are the exact endpoints GitHub's own UI uses to lazy-load diffs.
  // No CORS issues, no API tokens needed — the user's session cookie is
  // automatically included because these are same-origin requests.
  // ---------------------------------------------------------------------------
  async function fetchAndSend(v) {
    if (v.sent) return;

    try {
      const { files, headOid } = await fetchAllDiffContents(v);
      await attachSources(v, files, headOid);
      await postToPanel(v, { type: "files", owner: v.owner, repo: v.repo, prNumber: v.prNumber, files });
      v.sent = true;
    } catch (err) {
      await postToPanel(v, { type: "error", message: err.message });
    }
  }

  // Extract diff data already embedded in the page, then fetch any remaining
  // files that GitHub lazy-loads via page_data/diff_entries.
  async function fetchAllDiffContents(v) {
    const route = await pageRoute(v.path);
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
      const url = `/${v.owner}/${v.repo}/pull/${v.prNumber}/page_data/diff_entries?paths=${encodedPaths}&ctx=%3A%3A&w=0&range=${commitOid}`;
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

    // Convert GitHub's internal format to the shape Astra's panel expects
    // (mirrors the GitHub REST API response for /pulls/:number/files).
    const files = summaries.map(summary => {
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
    return { files, headOid: commitOid };
  }

  // The live document holds the route data only when it was loaded at this
  // path; otherwise read it from a fresh copy of the page.
  async function pageRoute(path) {
    let doc = document;
    if (path !== loadedPath) {
      const resp = await fetch(path, { credentials: "same-origin" });
      if (!resp.ok) throw new Error(`Failed to load the PR page: ${resp.status}`);
      doc = new DOMParser().parseFromString(await resp.text(), "text/html");
    }
    const script = Array.from(doc.querySelectorAll('script[type="application/json"]'))
      .find(s => s.textContent.includes("pullRequestsChangesRoute"));
    if (!script) throw new Error("Could not find PR data in page. Try reloading.");
    return JSON.parse(script.textContent).payload.pullRequestsChangesRoute;
  }

  // Full post-image source per file, so the core can find entities and call
  // edges. Fetched here (github.com origin, same session) because the raw
  // redirect target does not allow the extension's own origin.
  const SOURCE_EXT = /\.(m?[jt]sx?|cjs|py|go|java|rs)$/i;

  async function attachSources(v, files, headOid) {
    const wanted = files.filter((f) => !isRemoved(f) && SOURCE_EXT.test(f.filename));
    let next = 0;
    async function worker() {
      while (next < wanted.length) {
        const f = wanted[next++];
        const path = f.filename.split("/").map(encodeURIComponent).join("/");
        try {
          const resp = await fetch(`/${v.owner}/${v.repo}/raw/${headOid}/${path}`, { credentials: "same-origin" });
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

  // A message posted before the panel's script runs is lost.
  async function postToPanel(v, msg) {
    await v.loaded;
    v.iframe.contentWindow?.postMessage(msg, "*");
  }

  sync();
  globalThis.navigation?.addEventListener("navigatesuccess", sync);
  document.addEventListener("turbo:load", sync);
})();
