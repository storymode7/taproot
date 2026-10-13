// Run: node --test shared/test/*.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderReview } from "../lib/render.js";

// Just enough DOM for renderReview. Every element records the text and
// HTML it is given, so a test can read all that the review shows.
let shown;
class Element {
  dataset = {};
  classList = { add() {}, remove() {}, toggle() {} };
  #title = "";
  set innerHTML(v) { shown.push(["html", v]); }
  set textContent(v) { shown.push(["text", v]); }
  get title() { return this.#title; }
  set title(v) { this.#title = v; shown.push(["text", v]); }
  setAttribute(_, v) { shown.push(["text", v]); }
  appendChild() {}
  append() {}
  addEventListener() {}
  querySelector() { return new Element(); }
}
globalThis.document = { createElement: () => new Element(), addEventListener() {}, removeEventListener() {} };

const store = { get: (id) => ({ id, status: "pending", comment: "" }), set() {} };

function render(review, opts) {
  shown = [];
  renderReview(new Element(), review, store, opts);
  return shown.map(([, v]) => v).join("\n");
}

const RLO = "\u202e", LRI = "\u2066", PDI = "\u2069";
// Trojan Source: this line runs as a compare against a long string, but
// shown raw it reads as `"user") { // Check if admin`.
const trojan = `    if (accessLevel != "user${RLO} ${LRI}// Check if admin${PDI} ${LRI}") {`;
const snippet = {
  id: "0123456789abcdef", filePath: `src/auth${RLO}sj.js`, symbolPath: `isAdmin${LRI}`,
  nodeType: "function", language: "javascript",
  diffLines: ["@@ -1,2 +1,2 @@", " function isAdmin(accessLevel) {", '-    if (accessLevel != "user") {', `+${trojan}`],
  unchangedCallers: [`main.js::ma${RLO}in`], testedBy: [`test${LRI}Admin`],
};

test("bidi controls show as markers wherever the review shows repo text", () => {
  const highlighted = [];
  const all = render({
    snippets: [snippet],
    tree: { roots: [{ id: snippet.id }] },
    missingSource: [`lib/${RLO}x.js`],
    skipped: [{ path: `img/${RLO}a.png`, oldPath: `img/${LRI}b.png`, reason: "binary" }],
  }, { highlight: (code) => (highlighted.push(code), null) });

  assert.doesNotMatch(all, /[\u061c\u200e\u200f\u202a-\u202e\u2066-\u2069]/);
  const line = '    if (accessLevel != "user\ufffd \ufffd// Check if admin\ufffd \ufffd") {';
  assert.ok(highlighted.includes(line), "the highlighter gets the line with markers");
  assert.ok(all.includes(line.replaceAll('"', "&quot;")), "the diff shows the line with markers");
  for (const name of ["src/auth\ufffdsj.js", "isAdmin\ufffd", "main.js::ma\ufffdin", "test\ufffdAdmin", "lib/\ufffdx.js", "img/\ufffda.png", "img/\ufffdb.png"]) {
    assert.ok(all.includes(name), `${name} is shown`);
  }
});

test("other control characters show as glyphs, and a CRLF line end is dropped", () => {
  const all = render({ snippets: [{
    ...snippet, filePath: "a\tb\nc.go", symbolPath: "", nodeType: "hunk", unchangedCallers: [], testedBy: [],
    diffLines: ["@@ -1 +1 @@", "-x\r", "+a\x1bb\x7fc\x85d\x00e\u200ff\u061cg\u200eh\ti\r"],
  }] });

  assert.doesNotMatch(all, /[\0-\x08\x0b-\x1f\x7f-\x9f]/);
  assert.ok(all.includes('<span class="marker">-</span>x</td>'));
  assert.ok(all.includes('<span class="marker">+</span>a\u241bb\u2421c\ufffdd\u2400e\ufffdf\ufffdg\ufffdh\ti</td>'));
  assert.ok(all.includes("a\tb\u240ac.go"));
});

test("names and code stay escaped", () => {
  render({ snippets: [{
    ...snippet, filePath: "src/<img src=x onerror=alert(1)>.js", symbolPath: '"><svg onload=alert(1)>',
    unchangedCallers: ["<script>"], testedBy: [], diffLines: ["@@ -1 +1 @@", '+<script>alert("x")</script>'],
  }] });
  const html = shown.filter(([sink]) => sink === "html").map(([, v]) => v).join("\n");

  assert.doesNotMatch(html, /<(img|svg|script)\b/);
  assert.ok(html.includes("&lt;script&gt;alert(&quot;x&quot;)&lt;/script&gt;"));
  assert.ok(html.includes("src/&lt;img src=x onerror=alert(1)&gt;.js"));
});

test("files GitHub sends no diff for say so", () => {
  const all = render({ snippets: [], skipped: [
    { path: "logo.png", reason: "no-patch" },
    { path: "b.go", oldPath: "a.go", reason: "renamed" },
  ] });
  assert.ok(all.includes("logo.png (no diff from GitHub)"), all);
  assert.ok(all.includes("a.go → b.go (renamed)"), all);
});
