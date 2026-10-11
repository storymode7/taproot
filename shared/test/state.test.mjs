// Run: node --test shared/test/*.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { StateStore, stateKey } from "../lib/state.js";

const snippet = (hunk, ...lines) => ({ id: "eaeed2db87666e6f", diffLines: [hunk, ...lines] });
const main = snippet("@@ -15,4 +15,5 @@", " export function main() {", "   checkAuth();", '+  console.log("ready");', "   serve();");

test("state does not follow a snippet into another PR", async () => {
  const store = new StateStore("astra-test");
  await store.load("alice/shop#1", [main]);
  store.set(main.id, { status: "accepted", comment: "alice/shop#1 only" });
  assert.equal(store.get(main.id).status, "accepted");

  await store.load("bob/bank#7", [main]);
  assert.deepEqual(store.get(main.id), { id: main.id, status: "pending", comment: "" });
});

test("keys are scoped per PR, ignoring the case of owner and repo", async () => {
  const k = await stateKey("alice/shop#1", main);
  assert.notEqual(k, await stateKey("bob/bank#1", main));
  assert.notEqual(k, await stateKey("alice/shop#2", main));
  assert.equal(k, await stateKey("Alice/Shop#1", main));
});

test("keys follow the changed lines, not their position or context", async () => {
  const k = await stateKey("alice/shop#1", main);
  const moved = snippet("@@ -17,4 +17,5 @@", " export function main() {", "   checkAuth(); // auth", '+  console.log("ready");', "   serve();");
  const altered = snippet("@@ -15,4 +15,4 @@", " export function main() {", "-  checkAuth();", '+  console.log("ready");', "   serve();");
  assert.equal(k, await stateKey("alice/shop#1", moved));
  assert.notEqual(k, await stateKey("alice/shop#1", altered));
});
