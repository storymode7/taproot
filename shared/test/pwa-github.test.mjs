// Run: node --test shared/test/*.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { fetchPRFiles, parsePRUrl } from "../../apps/pwa/lib/github.js";

// A PR with `count` changed files, served like GitHub: 100 per page at
// most, and no file past the 3000th.
function serve(count) {
  const calls = [];
  globalThis.fetch = async (url) => {
    const u = new URL(url);
    calls.push(u.pathname.replace("/repos/o/r/pulls/1", "") + u.search);
    if (u.pathname.endsWith("/files")) {
      const per = Number(u.searchParams.get("per_page") ?? 30);
      const page = Number(u.searchParams.get("page") ?? 1);
      const listed = Math.min(count, 3000);
      const from = Math.min((page - 1) * per, listed);
      const files = Array.from({ length: Math.min(per, listed - from) }, (_, i) => ({ filename: `f${from + i + 1}.txt` }));
      return Response.json(files);
    }
    return Response.json({ title: "t", head: { sha: "h" }, base: { sha: "b" }, changed_files: count });
  };
  return calls;
}

test("every changed file is fetched, page by page", async () => {
  const calls = serve(130);
  const { meta, files } = await fetchPRFiles("o", "r", 1);
  assert.equal(files.length, 130);
  assert.equal(files.at(-1).filename, "f130.txt");
  assert.equal(meta.changedFiles, 130);
  assert.deepEqual(calls, ["", "/files?per_page=100&page=1", "/files?per_page=100&page=2"]);
});

test("a file count that fills its last page costs no extra request", async () => {
  const calls = serve(200);
  const { files } = await fetchPRFiles("o", "r", 1);
  assert.equal(files.length, 200);
  assert.equal(calls.length, 3);
});

test("GitHub lists at most 3000 files, and meta says how many changed", async () => {
  const calls = serve(3100);
  const { meta, files } = await fetchPRFiles("o", "r", 1);
  assert.equal(files.length, 3000);
  assert.equal(meta.changedFiles, 3100);
  assert.equal(calls.length, 31);
});

test("a PR URL without https:// is accepted", () => {
  assert.deepEqual(parsePRUrl("github.com/acme/widgets/pull/7"), { owner: "acme", repo: "widgets", prNumber: 7 });
  assert.throws(() => parsePRUrl(""), /Paste the URL/);
});

// Every request answers with this status, body and headers.
function fail(status, message, headers = {}) {
  globalThis.fetch = async () => Response.json({ message }, { status, headers });
}

test("errors carry GitHub's message and whether a rate limit caused them", async () => {
  fail(404, "Not Found");
  await assert.rejects(fetchPRFiles("o", "r", 1), { status: 404, message: "GitHub API error: 404 Not Found", rateLimited: false });

  fail(403, "Repository access blocked", { "x-ratelimit-remaining": "55" });
  await assert.rejects(fetchPRFiles("o", "r", 1), { status: 403, rateLimited: false });

  const reset = Math.floor(Date.now() / 1000) + 41 * 60;
  fail(403, "API rate limit exceeded", { "x-ratelimit-remaining": "0", "x-ratelimit-reset": String(reset) });
  await assert.rejects(fetchPRFiles("o", "r", 1), { status: 403, rateLimited: true, retryAt: reset * 1000 });

  fail(429, "You have exceeded a secondary rate limit", { "x-ratelimit-remaining": "40", "retry-after": "90" });
  const before = Date.now();
  await assert.rejects(fetchPRFiles("o", "r", 1), (err) =>
    err.rateLimited && err.retryAt >= before + 90000 && err.retryAt <= Date.now() + 90000);

  fail(403, "Forbidden");
  await assert.rejects(fetchPRFiles("o", "r", 1), { rateLimited: true, retryAt: null });
});

test("a network failure says GitHub could not be reached", async () => {
  globalThis.fetch = async () => { throw new TypeError("Failed to fetch"); };
  await assert.rejects(fetchPRFiles("o", "r", 1), { network: true, message: /Couldn't reach GitHub/ });

  const ctrl = new AbortController();
  ctrl.abort();
  globalThis.fetch = async (url, init) => { init.signal.throwIfAborted(); };
  await assert.rejects(fetchPRFiles("o", "r", 1, { signal: ctrl.signal }), { name: "AbortError" });
});
