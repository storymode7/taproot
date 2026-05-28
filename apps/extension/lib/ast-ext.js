/**
 * lib/ast-ext.js — Extension-specific tree-sitter loader
 *
 * Identical to the PWA ast.js except WASM file paths are resolved through
 * chrome.runtime.getURL() so Chrome can serve them from the extension package.
 *
 * NOTE: This file is intentionally separate from apps/pwa/lib/ast.js because
 * Chrome extensions cannot import files from outside their own directory tree.
 */

import { Parser, Language } from "../vendor/web-tree-sitter.js";

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

let tsInitialized = false;
const grammarCache = new Map();

export async function loadTreeSitter() {
  if (tsInitialized) return;
  await Parser.init({ locateFile: () => vendorUrl("tree-sitter.wasm") });
  tsInitialized = true;
}

async function getGrammar(lang) {
  if (grammarCache.has(lang)) return grammarCache.get(lang);
  await loadTreeSitter();
  const wasm = LANG_WASM[lang];
  if (!wasm) return null;
  const grammar = await Language.load(vendorUrl(wasm));
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
  await loadTreeSitter();
  const parser = new Parser();
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
  // We must preserve real line numbers so that AST node positions
  // (row numbers) match the newStart/newEnd values from hunksFromPatch.
  // Strategy: pad with blank lines before each hunk so that the first
  // content line of hunk N lands on the correct 1-based row.
  const output = [""]; // index 0 unused; output[i] = line i (1-based)
  let cursor = 1; // next line number we intend to write

  for (const line of patch.split("\n")) {
    const m = line.match(/^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
    if (m) {
      const hunkStart = Number(m[1]);
      // Pad gap between last written line and this hunk with blanks
      while (cursor < hunkStart) { output.push(""); cursor++; }
    } else if (line.startsWith("+")) {
      output.push(line.slice(1));
      cursor++;
    } else if (!line.startsWith("-") && !line.startsWith("\\")) {
      output.push(line.startsWith(" ") ? line.slice(1) : line);
      cursor++;
    }
    // deleted lines (-) don't appear in the post-image
  }
  return output.join("\n");
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
