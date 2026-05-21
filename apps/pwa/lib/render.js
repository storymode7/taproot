/**
 * lib/render.js — renders snippet cards into the review list
 *
 * Vanilla JS only. No framework, no virtual DOM.
 * Desktop: vertical list. Mobile (≤768px): swipe-card stack.
 */

/**
 * @param {HTMLElement} container
 * @param {import('./ast.js').Snippet[]} snippets
 * @param {import('./state.js').StateStore} store
 */
export function renderSnippetList(container, snippets, store) {
  container.innerHTML = "";

  const isMobile = window.matchMedia("(max-width: 768px)").matches;

  if (isMobile) {
    renderSwipeStack(container, snippets, store);
  } else {
    renderDesktopList(container, snippets, store);
  }
}

// ---------------------------------------------------------------------------
// Desktop: vertical list
// ---------------------------------------------------------------------------
function renderDesktopList(container, snippets, store) {
  for (const snippet of snippets) {
    container.appendChild(makeDesktopCard(snippet, store));
  }
}

function makeDesktopCard(snippet, store) {
  const state = store.get(snippet.id);
  const card = document.createElement("article");
  card.className = `snippet-card status-${state.status}`;
  card.dataset.snippetId = snippet.id;

  card.innerHTML = `
    <header class="snippet-header">
      <span class="snippet-breadcrumb">
        <span class="snippet-file">${escHtml(snippet.filePath)}</span>
        ${snippet.symbolPath ? `<span class="snippet-symbol"> ∷ ${escHtml(snippet.symbolPath)}</span>` : ""}
      </span>
      <span class="snippet-meta">
        <span class="snippet-lang">${escHtml(snippet.language)}</span>
        <span class="snippet-depth" title="Call-graph depth">depth ${snippet.depth}</span>
      </span>
    </header>
    <pre class="snippet-diff"><code>${renderDiffLines(snippet.diffLines)}</code></pre>
    <footer class="snippet-actions">
      <button class="btn-accept"  data-action="accepted">✓ Accept</button>
      <button class="btn-reject"  data-action="rejected">✗ Reject</button>
      <button class="btn-review"  data-action="reviewed">● Reviewed</button>
      <button class="btn-comment" data-action="comment">💬 Comment</button>
      <span class="snippet-status-label">${statusLabel(state.status)}</span>
    </footer>
  `;

  card.querySelector(".snippet-actions").addEventListener("click", (e) => {
    const action = e.target.dataset.action;
    if (!action || action === "comment") return;
    store.set(snippet.id, { ...state, status: action });
    card.className = `snippet-card status-${action}`;
    card.querySelector(".snippet-status-label").textContent = statusLabel(action);
  });

  return card;
}

// ---------------------------------------------------------------------------
// Mobile: swipe stack
// ---------------------------------------------------------------------------
function renderSwipeStack(container, snippets, store) {
  container.classList.add("swipe-mode");

  let index = 0;

  const progress = document.createElement("div");
  progress.className = "swipe-progress";
  container.appendChild(progress);

  const stack = document.createElement("div");
  stack.className = "swipe-stack";
  container.appendChild(stack);

  const hint = document.createElement("p");
  hint.className = "swipe-hint";
  hint.textContent = "← Reject  |  ↑ Comment  |  ↓ Skip  |  Accept →";
  container.appendChild(hint);

  function updateProgress() {
    progress.textContent = `${index + 1} / ${snippets.length}`;
  }

  function showCard() {
    stack.innerHTML = "";
    if (index >= snippets.length) {
      stack.innerHTML = "<p class='swipe-done'>All snippets reviewed! ✓</p>";
      return;
    }
    const snippet = snippets[index];
    const card = makeDesktopCard(snippet, store);
    card.classList.add("swipe-card");
    stack.appendChild(card);

    // Touch / pointer swipe
    let startX = 0, startY = 0;
    card.addEventListener("pointerdown", (e) => { startX = e.clientX; startY = e.clientY; });
    card.addEventListener("pointerup", (e) => {
      const dx = e.clientX - startX;
      const dy = e.clientY - startY;
      if (Math.abs(dx) > Math.abs(dy)) {
        if (dx > 60)  advance("accepted");
        if (dx < -60) advance("rejected");
      } else {
        if (dy < -60) advance("reviewed"); // swipe up = needs-comment / reviewed
        if (dy > 60)  advance(null);       // swipe down = skip
      }
    });
    updateProgress();
  }

  function advance(action) {
    if (action) store.set(snippets[index].id, { status: action });
    index++;
    showCard();
  }

  showCard();
}

// ---------------------------------------------------------------------------
// Diff line rendering
// ---------------------------------------------------------------------------
function renderDiffLines(lines) {
  return lines
    .map((line) => {
      const cls = line.startsWith("+") ? "diff-add"
                : line.startsWith("-") ? "diff-del"
                : line.startsWith("@") ? "diff-hunk"
                : "diff-ctx";
      return `<span class="${cls}">${escHtml(line)}</span>`;
    })
    .join("\n");
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------
function escHtml(s) {
  return String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function statusLabel(status) {
  return { pending: "", reviewed: "Reviewed", accepted: "Accepted ✓", rejected: "Rejected ✗" }[status] ?? "";
}
