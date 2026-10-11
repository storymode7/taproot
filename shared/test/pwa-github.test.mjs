// Run: node --test shared/test/*.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { fetchPRFiles } from "../../apps/pwa/lib/github.js";

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
