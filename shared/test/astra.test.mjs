// Run: node --test shared/test/*.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { initAstra } from "../lib/astra.js";

const root = new URL("../../", import.meta.url);

// loadCore() adds wasm_exec.js as a <script> and fetches the vendored core.
globalThis.document = {
  querySelector: () => null,
  createElement: () => ({}),
  head: { appendChild(s) { new Function(readFileSync(new URL(s.src, root), "utf8"))(); s.onload(); } },
};
globalThis.fetch = async (url) =>
  new Response(readFileSync(new URL(url, root)), { headers: { "Content-Type": "application/wasm" } });

const source = `package shop;

public class Checkout {
    public int total() {
        return price() + tax() + 1;
    }

    int price() {
        return 12;
    }

    int tax() {
        return 2;
    }
}
`;
const patch = `@@ -3,14 +3,14 @@
 public class Checkout {
     public int total() {
-        return price() + tax();
+        return price() + tax() + 1;
     }

     int price() {
-        return 10;
+        return 12;
     }

     int tax() {
-        return 1;
+        return 2;
     }
 }`;

test("browsers send no entities, so the core finds methods and their calls", async () => {
  const astra = initAstra({ coreBase: "vendor/core", fetchSource: async () => source });
  const core = await astra.loadCore();
  const process = core.process;
  const sent = [];
  core.process = (req) => { sent.push(JSON.parse(req)); return process(req); };

  const review = await astra.buildReview([{ filename: "src/Checkout.java", status: "modified", patch }]);
  core.process = process;

  assert.deepEqual(sent[0].files.map((f) => f.entities), [[]]);
  const name = new Map(review.snippets.map((s) => [s.id, s.symbolPath]));
  const tree = review.tree.roots.map((n) => [name.get(n.id), (n.children ?? []).map((c) => name.get(c.id)).sort()]);
  assert.deepEqual(tree, [["Checkout.total", ["Checkout.price", "Checkout.tax"]]]);
});

const edit = { filename: "src/app.js", status: "modified", patch: "@@ -1,3 +1,3 @@\n function main() {\n-  a();\n+  b();\n }" };
const noDiff = [
  { filename: "assets/logo.png", status: "modified" },
  { filename: "src/new_name.py", status: "renamed", previous_filename: "src/old_name.py" },
  { filename: "run.sh", status: "changed" },
  { filename: "dist/app.min.js", status: "modified", patch: "" },
];
const removed = { filename: "svc/auth.go", status: "removed", patch: "@@ -1,3 +0,0 @@\n-package svc\n-\n-func Authorize() bool { return true }" };

test("files without a diff are listed, deleted files go to the core", async () => {
  const fetched = [];
  const astra = initAstra({ coreBase: "vendor/core", fetchSource: async (f) => (fetched.push(f.filename), "function main() {\n  b();\n}\n") });
  const core = await astra.loadCore();
  const process = core.process;
  const sent = [];
  core.process = (req) => { sent.push(JSON.parse(req)); return process(req); };

  const review = await astra.buildReview([removed, ...noDiff, edit]);
  const reordered = await astra.reorderReview(review, "post");
  core.process = process;

  const skipped = [
    { path: "assets/logo.png", reason: "no-diff" },
    { path: "dist/app.min.js", reason: "no-diff" },
    { path: "run.sh", reason: "no-diff" },
    { path: "src/new_name.py", oldPath: "src/old_name.py", reason: "no-diff" },
  ];
  assert.deepEqual(review.skipped, skipped);
  assert.deepEqual(reordered.skipped, skipped);
  assert.deepEqual(sent[0].files.map((f) => [f.path, f.isDeleted, f.hunks.length]), [["svc/auth.go", true, 1], ["src/app.js", false, 1]]);
  assert.deepEqual(fetched, ["src/app.js"]);
});

test("files the core skips are listed with the ones the browser skips", async () => {
  const astra = initAstra({ coreBase: "vendor/core", fetchSource: async () => null });
  const core = await astra.loadCore();
  const process = core.process;
  core.process = (req) => JSON.stringify({ ...JSON.parse(process(req)), skipped: [{ path: "b.bin", reason: "binary" }] });
  const review = await astra.buildReview([{ filename: "c.png", status: "added" }, edit]);
  core.process = process;
  assert.deepEqual(review.skipped, [{ path: "b.bin", reason: "binary" }, { path: "c.png", reason: "no-diff" }]);
});

test("a reorder uses the files of the review it is given", async () => {
  const astra = initAstra({ coreBase: "vendor/core", fetchSource: async () => null });
  const first = await astra.buildReview([edit]);
  await astra.buildReview([{ filename: "other.txt", status: "modified", patch: "@@ -1 +1 @@\n-a\n+b" }]);
  const reordered = await astra.reorderReview(first, "post");
  assert.deepEqual(reordered.snippets.map((s) => s.filePath), ["src/app.js"]);
});

test("an aborted build stops before the core runs", async () => {
  const ctrl = new AbortController();
  const astra = initAstra({ coreBase: "vendor/core", fetchSource: async () => (ctrl.abort(), null) });
  const core = await astra.loadCore();
  const process = core.process;
  let calls = 0;
  core.process = (req) => (calls++, process(req));
  await assert.rejects(astra.buildReview([edit], "pre", { signal: ctrl.signal }), { name: "AbortError" });
  core.process = process;
  assert.equal(calls, 0);
});

test("sources are fetched six at a time, and the core gets the files in PR order", async () => {
  let inFlight = 0, most = 0;
  const astra = initAstra({
    coreBase: "vendor/core",
    fetchSource: async (f) => {
      most = Math.max(most, ++inFlight);
      await new Promise((r) => setTimeout(r, f.filename === "f00.go" ? 60 : 10));
      inFlight--;
      return `package p\n\nfunc F() int {\n\treturn ${f.filename.slice(1, 3)}\n}\n`;
    },
  });
  const core = await astra.loadCore();
  const process = core.process;
  const sent = [];
  core.process = (req) => { sent.push(JSON.parse(req)); return process(req); };
  const files = Array.from({ length: 20 }, (_, i) => ({
    filename: `f${String(i).padStart(2, "0")}.go`, status: "modified",
    patch: "@@ -1,5 +1,5 @@\n package p\n \n func F() int {\n-\treturn 0\n+\treturn 1\n }",
  }));
  const progress = [];
  await astra.buildReview(files, "pre", { onProgress: (done, total) => progress.push(`${done}/${total}`) });
  core.process = process;

  assert.equal(most, 6);
  assert.deepEqual(sent[0].files.map((f) => f.path), files.map((f) => f.filename));
  assert.ok(sent[0].files.every((f) => f.source.startsWith("package p")));
  assert.deepEqual(progress, Array.from({ length: 21 }, (_, i) => `${i}/20`));
});
