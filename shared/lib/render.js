/**
 * shared/lib/render.js — the review view for the browser surfaces.
 *
 * Laid out like GitHub's "Files changed" page: a call-tree sidebar (the
 * review order) next to one diff card per snippet, with old/new line
 * numbers, +/- markers, and a collapsed state once a snippet is judged.
 *
 * Keyboard (when focus is not in a text field):
 *   j / k  next / previous snippet     y  accept     n  reject
 *   r      mark reviewed               c  comment    x  fold / unfold
 *
 * Vanilla JS, no dependencies. Syntax highlighting is optional and injected
 * (the extension passes highlight.js; the PWA renders plain code).
 */

const STATUS_LABEL = {
  pending: "Pending", reviewed: "Reviewed", accepted: "Accepted", rejected: "Rejected",
};

const SKIP_REASON = {
  binary: "binary", renamed: "renamed", mode: "mode change", empty: "empty file",
  conflict: "conflict", "no-diff": "no diff",
};

/**
 * @param {HTMLElement} container
 * @param {object} review — core response: { snippets, tree, missingSource?, skipped? }
 * @param {import('./state.js').StateStore} store
 * @param {object} [opts]
 * @param {(code: string, language: string) => string|null} [opts.highlight]
 *        returns HTML for one line of code, or null to render it plain
 * @param {string} [opts.commentHint] — shown under the comment box
 * @param {string} [opts.notice] — shown above the cards, e.g. that the file
 *        list is incomplete
 */
export function renderReview(container, review, store, opts = {}) {
  container.innerHTML = "";
  container.classList.add("astra-review");
  if (container._astraKeys) document.removeEventListener("keydown", container._astraKeys);

  const snippets = review.snippets ?? [];
  const byId = new Map(snippets.map((s) => [s.id, s]));
  const roots = review.tree?.roots ?? snippets.map((s) => ({ id: s.id }));

  // Cards follow the core's reading order (top-down or bottom-up); the
  // tree supplies each snippet's node and parent (for breadcrumbs).
  const order = snippets.map((s) => s.id);
  const parentOf = new Map();
  const nodeOf = new Map();
  (function walk(nodes, parent) {
    for (const n of nodes) {
      if (n.ref || !byId.has(n.id)) continue;
      nodeOf.set(n.id, n);
      if (parent) parentOf.set(n.id, parent);
      walk(n.children ?? [], n.id);
    }
  })(roots, null);

  const nav = el("nav", "astra-tree");
  const progress = el("div", "astra-progress");
  nav.appendChild(progress);
  nav.appendChild(renderTree(roots, byId, store, focusSnippet));

  const main = el("div", "astra-files");
  if (opts.notice) main.appendChild(el("div", "astra-banner", opts.notice));
  if (review.missingSource?.length) {
    const warn = el("div", "astra-banner");
    warn.textContent =
      `Couldn't load the full source of ${review.missingSource.length} file(s), ` +
      `so they show as plain hunks without call structure: ${review.missingSource.join(", ")}`;
    main.appendChild(warn);
  }
  if (review.skipped?.length) {
    const names = review.skipped.map((f) =>
      `${f.oldPath ? `${f.oldPath} → ` : ""}${f.path} (${SKIP_REASON[f.reason] ?? f.reason})`);
    main.appendChild(el("div", "astra-banner", `Not shown here, review on GitHub: ${names.join(", ")}`));
  }
  if (!order.length) {
    main.appendChild(el("p", "astra-empty", review.skipped?.length
      ? "No snippets to review: the changed files listed above have no diff to show here."
      : "No snippets to review."));
  }

  const cards = new Map();
  for (const id of order) {
    const snippet = byId.get(id);
    const card = makeCard(snippet, {
      store, opts, byId,
      node: nodeOf.get(id),
      crumbs: breadcrumbs(id, parentOf, byId),
      onJudge: (status) => {
        setStatus(snippet, status);
        const next = status !== "pending" && nextPending(id);
        if (next) focusSnippet(next);
      },
    });
    cards.set(id, card);
    main.appendChild(card);
  }

  container.append(nav, main);
  updateProgress();

  function setStatus(snippet, status) {
    store.set(snippet.id, { status });
    const card = cards.get(snippet.id);
    card.classList.remove("status-pending", "status-accepted", "status-rejected", "status-reviewed");
    card.classList.add(`status-${status}`);
    card.classList.toggle("folded", status !== "pending");
    card.querySelector(".astra-status").textContent = STATUS_LABEL[status];
    nav.querySelectorAll(`[data-id="${snippet.id}"] > .astra-tree-row:not(.ref) .astra-dot`)
      .forEach((d) => { d.className = `astra-dot status-${status}`; });
    updateProgress();
  }

  function updateProgress() {
    if (!order.length) {
      progress.textContent = "Nothing to review";
      return;
    }
    const done = order.filter((id) => store.get(id).status !== "pending").length;
    const pct = Math.round((done * 100) / order.length);
    progress.innerHTML =
      `<span>${done} / ${order.length} reviewed</span>` +
      `<span class="astra-bar"><span style="width:${pct}%"></span></span>`;
  }

  function nextPending(fromId) {
    const i = order.indexOf(fromId);
    for (let k = 1; k <= order.length; k++) {
      const id = order[(i + k) % order.length];
      if (store.get(id).status === "pending") return id;
    }
    return null;
  }

  let current = order.find((id) => store.get(id).status === "pending") ?? order[0] ?? null;
  function focusSnippet(id) {
    const card = cards.get(id);
    if (!card) return;
    cards.get(current)?.classList.remove("current");
    nav.querySelector(".astra-tree-row.current")?.classList.remove("current");
    current = id;
    card.classList.add("current");
    card.classList.remove("folded");
    const row = nav.querySelector(`[data-id="${id}"] > .astra-tree-row`);
    if (row) {
      row.classList.add("current");
      keepInView(nav, row);
    }
    card.scrollIntoView({ block: "start", behavior: "smooth" });
  }
  if (current) {
    cards.get(current).classList.add("current");
    nav.querySelector(`[data-id="${current}"] > .astra-tree-row`)?.classList.add("current");
  }

  const keys = (e) => {
    // A hidden or removed review has no boxes; its keys must not judge it.
    if (!container.getClientRects().length || e.ctrlKey || e.metaKey || e.altKey) return;
    if (e.target.closest?.("textarea, input, select")) return;
    const i = order.indexOf(current);
    const snippet = byId.get(current);
    switch (e.key) {
      case "j": focusSnippet(order[Math.min(i + 1, order.length - 1)]); break;
      case "k": focusSnippet(order[Math.max(i - 1, 0)]); break;
      case "y": if (snippet) cards.get(current)._judge("accepted"); break;
      case "n": if (snippet) cards.get(current)._judge("rejected"); break;
      case "r": if (snippet) cards.get(current)._judge("reviewed"); break;
      case "c": cards.get(current)?._comment(); break;
      case "x": cards.get(current)?.classList.toggle("folded"); break;
      default: return;
    }
    e.preventDefault();
  };
  container._astraKeys = keys;
  document.addEventListener("keydown", keys);
}

// ---------------------------------------------------------------------------
// Sidebar tree
// ---------------------------------------------------------------------------
function renderTree(nodes, byId, store, onPick) {
  const ul = el("ul");
  for (const n of nodes) {
    const s = byId.get(n.id);
    if (!s) continue;
    const li = el("li");
    li.dataset.id = n.id;
    const row = el("div", "astra-tree-row");
    const kids = n.ref ? [] : (n.children ?? []);
    const twisty = el("button", "astra-twisty", kids.length ? "▾" : "");
    twisty.tabIndex = -1;
    twisty.disabled = !kids.length;
    const dot = el("span", `astra-dot status-${n.ref ? "ref" : store.get(n.id).status}`);
    const name = el("span", "astra-tree-name", label(s));
    name.title = `${s.filePath}${s.symbolPath ? " :: " + s.symbolPath : ""}`;
    row.append(twisty, dot, name);
    if (n.ref) {
      row.classList.add("ref");
      name.title += " (shown elsewhere in the tree)";
    }
    row.addEventListener("click", (e) => {
      if (e.target === twisty) return;
      onPick(n.id);
    });
    li.appendChild(row);
    if (kids.length) {
      const sub = renderTree(kids, byId, store, onPick);
      li.appendChild(sub);
      twisty.addEventListener("click", () => {
        sub.hidden = !sub.hidden;
        twisty.textContent = sub.hidden ? "▸" : "▾";
      });
    }
    ul.appendChild(li);
  }
  return ul;
}

function label(s) {
  const base = s.filePath.split("/").pop();
  if (s.isDeleted) return `${base} (deleted)`;
  if (s.symbolPath) return s.symbolPath;
  return s.nodeType === "imports" ? `${base} (imports)` : base;
}

function breadcrumbs(id, parentOf, byId) {
  const chain = [];
  for (let p = parentOf.get(id); p; p = parentOf.get(p)) chain.unshift(label(byId.get(p)));
  return chain;
}

// ---------------------------------------------------------------------------
// Diff card
// ---------------------------------------------------------------------------
function makeCard(snippet, { store, opts, byId, node, crumbs, onJudge }) {
  const state = store.get(snippet.id);
  const card = el("article", `astra-card status-${state.status}`);
  card.id = `astra-${snippet.id}`;
  if (state.status !== "pending") card.classList.add("folded");

  const { adds, dels } = countChanges(snippet.diffLines);
  const head = el("header", "astra-card-head");
  head.innerHTML = `
    <button class="astra-fold" title="Fold (x)" tabindex="-1">▾</button>
    <div class="astra-title">
      ${crumbs.length ? `<span class="astra-crumbs">${crumbs.map(esc).join(" › ")} ›</span>` : ""}
      <strong class="astra-symbol">${esc(label(snippet))}</strong>
      <span class="astra-path">${esc(snippet.filePath)}</span>
    </div>
    <span class="astra-stat"><span class="add">+${adds}</span> <span class="del">−${dels}</span></span>
    <span class="astra-status">${STATUS_LABEL[state.status]}</span>
    <div class="astra-actions">
      <button data-status="accepted" title="Accept (y)">Accept</button>
      <button data-status="rejected" title="Reject (n)">Reject</button>
      <button data-status="reviewed" title="Mark reviewed (r)">Reviewed</button>
      <button data-comment title="Comment (c)">Comment</button>
    </div>`;
  card.appendChild(head);

  const notes = [];
  const also = (node?.alsoCalledBy ?? []).map((id) => byId.get(id)).filter(Boolean).map(label);
  if (also.length) notes.push(["Also called by", also]);
  if (snippet.unchangedCallers?.length) notes.push(["Called by unchanged", snippet.unchangedCallers]);
  if (snippet.testedBy?.length) notes.push(["Tested by", snippet.testedBy]);
  if (notes.length) {
    const box = el("div", "astra-notes");
    box.innerHTML = notes.map(([k, v]) => {
      const shown = v.slice(0, 4).map((x) => `<code>${esc(x)}</code>`).join(", ");
      const more = v.length > 4 ? ` <span title="${esc(v.slice(4).join("\n"))}">+${v.length - 4} more</span>` : "";
      return `<span><b>${esc(k)}</b> ${shown}${more}</span>`;
    }).join("");
    card.appendChild(box);
  }

  const body = el("div", "astra-card-body");
  body.appendChild(diffTable(snippet, opts.highlight));
  card.appendChild(body);

  const comment = el("div", "astra-comment");
  const saved = el("div", "astra-comment-saved");
  const form = el("form", "astra-comment-form");
  form.hidden = true;
  form.innerHTML = `
    <textarea rows="3" placeholder="Leave a comment on this snippet"></textarea>
    <div class="astra-comment-bar">
      <span class="astra-hint">${esc(opts.commentHint ?? "")}</span>
      <button type="button" data-cancel>Cancel</button>
      <button type="submit" class="primary">Save comment</button>
    </div>`;
  const textarea = form.querySelector("textarea");
  textarea.value = state.comment ?? "";
  comment.append(saved, form);
  body.appendChild(comment);

  const showSaved = () => {
    const text = store.get(snippet.id).comment;
    saved.hidden = !text;
    saved.innerHTML = text ? `<b>Your comment</b><p>${esc(text)}</p>` : "";
  };
  showSaved();

  // Reopening keeps a draft that Escape hid; only Cancel drops it.
  card._comment = () => {
    card.classList.remove("folded");
    form.hidden = false;
    textarea.focus();
  };
  card._judge = (status) => onJudge(status);

  saved.addEventListener("click", card._comment);
  form.querySelector("[data-cancel]").addEventListener("click", () => {
    textarea.value = store.get(snippet.id).comment ?? "";
    form.hidden = true;
  });
  form.addEventListener("submit", (e) => {
    e.preventDefault();
    store.set(snippet.id, { comment: textarea.value.trim() });
    form.hidden = true;
    showSaved();
  });
  textarea.addEventListener("keydown", (e) => {
    if (e.key === "Escape") form.hidden = true;
    if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) form.requestSubmit();
  });

  head.addEventListener("click", (e) => {
    const btn = e.target.closest("button");
    if (btn?.dataset.status) {
      const now = store.get(snippet.id).status;
      onJudge(now === btn.dataset.status ? "pending" : btn.dataset.status);
    } else if (btn?.hasAttribute("data-comment")) {
      card._comment();
    } else if (btn || (!e.target.closest(".astra-actions, .astra-crumbs, .astra-symbol, .astra-path") && getSelection().isCollapsed)) {
      // The chevron or empty header space folds; the names stay selectable.
      card.classList.toggle("folded");
    }
  });

  return card;
}

function countChanges(lines) {
  let adds = 0, dels = 0;
  for (const l of lines) {
    if (l.startsWith("+")) adds++;
    else if (l.startsWith("-")) dels++;
  }
  return { adds, dels };
}

/** GitHub-style unified diff table: old no. | new no. | marker + code. */
function diffTable(snippet, highlight) {
  const table = el("table", "astra-diff");
  const rows = [];
  let oldN = 0, newN = 0;
  for (const line of snippet.diffLines) {
    const m = line.match(/^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@(.*)$/);
    if (m) {
      oldN = Number(m[1]);
      newN = Number(m[2]);
      rows.push(`<tr class="hunk"><td class="num" colspan="2"></td><td class="code">${esc(line)}</td></tr>`);
      continue;
    }
    if (line.startsWith("\\")) {
      rows.push(`<tr class="eof"><td class="num" colspan="2"></td><td class="code">${esc(line.slice(2))}</td></tr>`);
      continue;
    }
    const kind = line[0] === "+" ? "add" : line[0] === "-" ? "del" : "ctx";
    const marker = kind === "add" ? "+" : kind === "del" ? "-" : " ";
    const code = line.length ? line.slice(1) : "";
    let html = null;
    try { html = highlight?.(code, snippet.language) ?? null; } catch { html = null; }
    const o = kind === "add" ? "" : oldN++;
    const n = kind === "del" ? "" : newN++;
    rows.push(
      `<tr class="${kind}"><td class="num">${o}</td><td class="num">${n}</td>` +
      `<td class="code"><span class="marker">${marker}</span>${html ?? esc(code)}</td></tr>`,
    );
  }
  table.innerHTML = `<tbody>${rows.join("")}</tbody>`;
  return table;
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------
// Scrolls only `box`; scrollIntoView would scroll the page as well.
function keepInView(box, item) {
  const b = box.getBoundingClientRect();
  const r = item.getBoundingClientRect();
  if (r.top < b.top) box.scrollTop -= b.top - r.top;
  else if (r.bottom > b.bottom) box.scrollTop += r.bottom - b.bottom;
}

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function esc(s) {
  return String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}
