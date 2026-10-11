// Run: node --test shared/test/*.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { initAstra } from "../lib/astra.js";

const root = new URL("../../", import.meta.url);
const fail = { script: false, wasm: false };

// A head that keeps its <script> tags, so a failed tag left behind is seen.
const scripts = [];
globalThis.document = {
  querySelector: (sel) => scripts.find((s) => sel === `script[src="${s.src}"]`) ?? null,
  createElement: () => {
    const s = { remove: () => scripts.splice(scripts.indexOf(s), 1) };
    return s;
  },
  head: {
    appendChild(s) {
      scripts.push(s);
      if (fail.script) {
        fail.script = false;
        s.onerror();
        return;
      }
      new Function(readFileSync(new URL(s.src, root), "utf8"))();
      s.onload();
    },
  },
};
globalThis.fetch = async (url) => {
  if (url.endsWith("astra-core.wasm") && fail.wasm) {
    fail.wasm = false;
    throw new TypeError("Failed to fetch");
  }
  return new Response(readFileSync(new URL(url, root)), { headers: { "Content-Type": "application/wasm" } });
};

const files = [{ filename: "notes.txt", status: "modified", patch: "@@ -1 +1 @@\n-old\n+new" }];

test("a review after a failed wasm_exec.js load loads it again", async () => {
  const astra = initAstra({ coreBase: "vendor/core", fetchSource: async () => null });
  fail.script = true;
  await assert.rejects(astra.buildReview(files), /Failed to load vendor\/core\/wasm_exec\.js/);
  const review = await astra.buildReview(files);
  assert.equal(review.snippets.length, 1);
});

test("a review after a failed engine download downloads it again", async () => {
  const astra = initAstra({ coreBase: "vendor/core", fetchSource: async () => null });
  fail.wasm = true;
  await assert.rejects(astra.buildReview(files), /Failed to fetch/);
  const review = await astra.buildReview(files);
  assert.equal(review.snippets.length, 1);
});
