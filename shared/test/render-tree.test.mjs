// Run: node --test shared/test/*.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderReview } from "../lib/render.js";

// A DOM that keeps the element tree, so a test can read the sidebar and the
// cards in the order the review shows them.
class Element {
  children = [];
  dataset = {};
  hidden = false;
  className = "";
  textContent = "";
  classes = new Set();
  classList = {
    add: (...c) => c.forEach((x) => this.classes.add(x)),
    remove: (...c) => c.forEach((x) => this.classes.delete(x)),
    toggle: (c, on = !this.classes.has(c)) => (on ? this.classes.add(c) : this.classes.delete(c)),
  };
  constructor(tag) { this.tag = tag; }
  set innerHTML(v) { this.html = v; }
  get innerHTML() { return this.html ?? ""; }
  appendChild(c) { this.children.push(c); return c; }
  append(...cs) { this.children.push(...cs); }
  setAttribute() {}
  addEventListener() {}
  querySelector() { return new Element("div"); }
  querySelectorAll() { return []; }
  getClientRects() { return [{}]; }
  scrollIntoView() {}
  getBoundingClientRect() { return { top: 0, bottom: 0 }; }
  get text() { return [this.textContent, ...this.children.map((c) => c.text)].filter(Boolean).join(" "); }
}
const keydown = [];
globalThis.document = {
  createElement: (tag) => new Element(tag),
  addEventListener: (type, f) => keydown.push(f),
  removeEventListener() {},
};

const statuses = new Map();
const store = {
  get: (id) => ({ id, status: statuses.get(id) ?? "pending", comment: "" }),
  set: (id, { status }) => statuses.set(id, status),
};

const diff = ["@@ -1 +1 @@", "-a", "+b"];
const snippets = [
  { id: "fn", filePath: "src/c.go", language: "go", symbolPath: "run", nodeType: "function", diffLines: diff },
  { id: "imp", filePath: "src/b.go", language: "go", symbolPath: "imports", nodeType: "imports", diffLines: diff },
  { id: "imp2", filePath: "lib/d.go", language: "go", symbolPath: "imports", nodeType: "imports", diffLines: diff },
  { id: "txt", filePath: "docs/a.txt", language: "", symbolPath: "", nodeType: "hunk", diffLines: diff },
  { id: "yml", filePath: "ci.yml", language: "", symbolPath: "", nodeType: "hunk", diffLines: diff },
];
const review = { snippets, tree: { roots: snippets.map(({ id }) => ({ id })) } };

function render() {
  statuses.clear();
  keydown.length = 0;
  const container = new Element("div");
  renderReview(container, review, store);
  const [nav, main] = container.children;
  return { nav, main, press: (key) => keydown.at(-1)({ key, target: {}, preventDefault() {} }) };
}

// What a list of elements shows: a snippet's id, or the heading's text.
const rows = (els) => els.map((e) => e.dataset.id ?? (e.className === "taproot-group" ? `[${e.text}]` : null)).filter(Boolean);
const cardId = (e) => e.id?.replace(/^taproot-/, "");

const heading = "[Other files Plain hunks, no call info. Taproot reads Go, JavaScript, TypeScript, Python, Java and Rust.]";

test("files in other languages sit under one heading in the tree and among the cards", () => {
  const { nav, main } = render();
  const tree = nav.children[1];
  assert.deepEqual(rows(tree.children), ["fn", "imp", "imp2", heading, "txt", "yml"]);
  const cards = main.children.map((e) => cardId(e) ?? (e.className === "taproot-group" ? `[${e.text}]` : null));
  assert.deepEqual(cards, ["fn", "imp", "imp2", heading, "txt", "yml"]);
});

test("the heading is not a snippet: progress, j/k and verdicts skip it", () => {
  const { nav, main, press } = render();
  assert.match(nav.children[0].html, /0 \/ 5 reviewed/);

  const current = () => main.children.filter((e) => e.classes.has("current")).map(cardId);
  const seen = [];
  for (const key of ["j", "j", "j", "j", "j", "k", "k"]) {
    press(key);
    seen.push(...current());
  }
  assert.deepEqual(seen, ["imp", "imp2", "txt", "yml", "yml", "txt", "imp2"]);

  press("y");
  assert.equal(statuses.get("imp2"), "accepted");
  assert.match(nav.children[0].html, /1 \/ 5 reviewed/);
});


test("imports rows name their file", () => {
  const { nav, main } = render();
  const names = nav.children[1].children.filter((li) => li.dataset.id).map((li) => li.children[0].children[2].textContent);
  assert.deepEqual(names, ["run", "imports · b.go", "imports · d.go", "a.txt", "ci.yml"]);
  assert.match(main.children.find((e) => cardId(e) === "imp2").children[0].html, /<strong class="taproot-symbol">imports · d\.go<\/strong>/);
});
