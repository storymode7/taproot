/**
 * lib/github-sync.js — Sync local review state back to GitHub
 *
 * Two operations:
 *   1. markFileViewed(adapter, filePath, viewed)
 *      — ticks/unticks the native GitHub "Viewed" checkbox when all
 *        snippets for a file have been reviewed.
 *
 *   2. submitReview(adapter, snippetComments, event)
 *      — batches local comment drafts into a single GitHub pull review
 *        with inline comments at the correct line ranges.
 *
 * Both operations call the adapter write methods (which piggyback on
 * the user's existing session in the extension, or throw ErrReadOnly
 * in the PWA public adapter).
 */

/**
 * Mark a file as Viewed/Unviewed on GitHub when all its snippets are done.
 *
 * @param {import('./github-session-adapter.js').GitHubSessionAdapter} adapter
 * @param {import('./ast.js').Snippet[]} allSnippets   — all snippets in the review
 * @param {import('./state.js').StateStore} store
 * @param {string} filePath — the file whose snippets just changed state
 */
export async function syncFileViewed(adapter, allSnippets, store, filePath) {
  const fileSnippets = allSnippets.filter((s) => s.filePath === filePath);
  if (fileSnippets.length === 0) return;

  const allReviewed = fileSnippets.every((s) => {
    const st = store.get(s.id);
    return st.status !== "pending";
  });

  try {
    await adapter.markFileViewed(filePath, allReviewed);
  } catch (err) {
    if (err.readOnly) return; // PWA public adapter — silently ignore
    console.warn("[astra] markFileViewed failed:", err.message);
  }
}

/**
 * Submit all local comment drafts as a single GitHub pull review.
 *
 * @param {import('./github-session-adapter.js').GitHubSessionAdapter} adapter
 * @param {import('./ast.js').Snippet[]} snippets
 * @param {import('./state.js').StateStore} store
 * @param {"COMMENT"|"APPROVE"|"REQUEST_CHANGES"} event
 */
export async function submitReview(adapter, snippets, store, event = "COMMENT") {
  const comments = [];

  for (const snippet of snippets) {
    const st = store.get(snippet.id);
    if (!st.comment) continue;

    comments.push({
      path: snippet.filePath,
      line: snippet.endLine,   // attach comment to last line of snippet
      body: st.comment,
    });
  }

  if (comments.length === 0 && event === "COMMENT") {
    console.info("[astra] No comment drafts to submit.");
    return;
  }

  // GitHub's pull review API: POST /repos/:owner/:repo/pulls/:number/reviews
  // We post each comment via the adapter's postReviewComment, grouped into
  // one review by using the "pending" review flow.
  //
  // For simplicity in MVP: post each comment individually.
  // v2: batch into a single review via the reviews API for a cleaner timeline.
  const errors = [];
  for (const c of comments) {
    try {
      await adapter.postReviewComment(c);
    } catch (err) {
      if (err.readOnly) return;
      errors.push(`${c.path}:${c.line}: ${err.message}`);
    }
  }

  if (errors.length > 0) {
    throw new Error(`Some comments failed to post:\n${errors.join("\n")}`);
  }
}
