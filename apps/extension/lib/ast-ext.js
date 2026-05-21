/**
 * lib/ast-ext.js — Extension-specific tree-sitter loader
 *
 * Identical to the PWA ast.js except WASM file paths are resolved through
 * chrome.runtime.getURL() so Chrome can serve them from the extension package.
 *
 * NOTE: This file is intentionally separate from apps/pwa/lib/ast.js because
 * Chrome extensions cannot import files from outside their own directory tree.
 */

// Resolve vendored WASM paths through the extension's own resource space.
function vendorUrl(filename) {
  return chrome.runtime.getURL(`vendor/${filename}`);
}

const LANG_WASM = {
  javascript: "tree-sitter-javascript.wasm",
  typescript: "tree-sitter-typescript.wasm",
  python:     "tree-sitter-python.wasm",
  go:         "tree-sitter-go.wasm",
  java:       "tree-sitter-java.wasm",
  rust:       "tree-sitter-rust.wasm",
};

let TreeSitter = null;
const grammarCache = new Map();

export async function loadTreeSitter() {
  if (TreeSitter) return TreeSitter;
  await loadScript(vendorUrl("web-tree-sitter.js"));
  await window.TreeSitter.init({ locateFile: () => vendorUrl("tree-sitter.wasm") });
  TreeSitter = window.TreeSitter;
  return TreeSitter;
}

async function loadScript(src) {
  return new Promise((resolve, reject) => {
    if (document.querySelector(`script[src="${src}"]`)) { resolve(); return; }
    const s = document.createElement("script");
    s.src = src;
    s.onload = resolve;
    s.onerror = () => reject(new Error(`Failed to load ${src}`));
    document.head.appendChild(s);
  });
}

async function getGrammar(lang) {
  if (grammarCache.has(lang)) return grammarCache.get(lang);
  const ts = await loadTreeSitter();
  const wasm = LANG_WASM[lang];
  if (!wasm) return null;
  const grammar = await ts.Language.load(vendorUrl(wasm));
  grammarCache.set(lang, grammar);
  return grammar;
}

// ---------------------------------------------------------------------------
// Snippet builder — identical logic to apps/pwa/lib/ast.js
// ---------------------------------------------------------------------------

export async function buildSnippets(files) {
  await loadTreeSitter();
  const snippets = [];
  for (const file of files) {
    if (file.status === "removed") continue;
    const lang = detectLanguage(file.filename);
    const grammar = lang ? await getGrammar(lang) : null;
    const fileSnippets = grammar
      ? await snippetsWithAST(file, lang, grammar)
      : [wholeFileSnippet(file, lang)];
    snippets.push(...fileSnippets);
  }
  return snippets;
}

async function snippetsWithAST(file, lang, grammar) {
  const ts = await loadTreeSitter();
  const parser = new ts.Parser();
  parser.setLanguage(grammar);
  const source = patchToPostImage(file.patch ?? "");
  const tree = parser.parse(source);
  return hunksFromPatch(file.patch ?? "").map((hunk) => {
    const node = smallestEnclosingNamedNode(tree.rootNode, hunk.newStart, hunk.newEnd);
    return makeSnippet(file.filename, lang, hunk, node);
  });
}

function wholeFileSnippet(file, lang) {
  const lines = (file.patch ?? "").split("\n");
  return {
    id: hashString(`${file.filename}::FILE`),
    filePath: file.filename,
    language: lang ?? "unknown",
    symbolPath: "",
    nodeType: "file",
    startLine: 1,
    endLine: lines.length,
    diffLines: lines,
    depth: 0,
    status: "pending",
  };
}

function makeSnippet(filePath, lang, hunk, node) {
  const symbolPath = node ? nodeSymbolPath(node) : "";
  return {
    id: hashString(`${filePath}::${symbolPath}::${hunk.newStart}`),
    filePath,
    language: lang,
    symbolPath,
    nodeType: node?.type ?? "unknown",
    startLine: hunk.newStart,
    endLine: hunk.newEnd,
    diffLines: hunk.lines,
    depth: 0,
    status: "pending",
  };
}

function smallestEnclosingNamedNode(rootNode, startLine, endLine) {
  let best = null;
  function visit(node) {
    if (!node.isNamed) return;
    const ns = node.startPosition.row + 1;
    const ne = node.endPosition.row + 1;
    if (ns <= startLine && ne >= endLine) {
      if (!best || (ne - ns) < (best.endPosition.row - best.startPosition.row)) {
        best = node;
      }
      for (let i = 0; i < node.childCount; i++) visit(node.child(i));
    }
  }
  visit(rootNode);
  return best;
}

function nodeSymbolPath(node) {
  const parts = [];
  let cur = node;
  while (cur) {
    const nameNode = cur.childForFieldName?.("name");
    if (nameNode) parts.unshift(nameNode.text);
    cur = cur.parent;
  }
  return parts.join(".");
}

function hunksFromPatch(patch) {
  const hunks = [];
  let current = null;
  for (const line of patch.split("\n")) {
    const m = line.match(/^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@/);
    if (m) {
      if (current) hunks.push(current);
      const start = Number(m[1]);
      const count = m[2] !== undefined ? Number(m[2]) : 1;
      current = { newStart: start, newEnd: start + count - 1, lines: [line] };
    } else if (current) {
      current.lines.push(line);
    }
  }
  if (current) hunks.push(current);
  return hunks;
}

function patchToPostImage(patch) {
  const lines = [];
  for (const line of patch.split("\n")) {
    if (line.startsWith("+")) lines.push(line.slice(1));
    else if (!line.startsWith("-") && !line.startsWith("\\")) {
      lines.push(line.startsWith(" ") ? line.slice(1) : line);
    }
  }
  return lines.join("\n");
}

const EXT_LANG = {
  js: "javascript", jsx: "javascript", mjs: "javascript", cjs: "javascript",
  ts: "typescript", tsx: "typescript", mts: "typescript",
  py: "python",
  go: "go",
  java: "java",
  rs: "rust",
};

function detectLanguage(filename) {
  const ext = filename.split(".").pop()?.toLowerCase();
  return EXT_LANG[ext] ?? null;
}

function hashString(s) {
  let h = 0;
  for (let i = 0; i < s.length; i++) {
    h = Math.imul(31, h) + s.charCodeAt(i) | 0;
  }
  return (h >>> 0).toString(16);
}
