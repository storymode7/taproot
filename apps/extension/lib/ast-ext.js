/**
 * lib/ast-ext.js — Extension-specific tree-sitter loader + entity-aware snippet builder
 *
 * Identical algorithm to apps/pwa/lib/ast.js but WASM paths are resolved through
 * chrome.runtime.getURL() so Chrome can serve them from the extension package.
 *
 * Full source content is fetched using the GitHub API token from the extension's
 * auth context (injected page session) so AST positions are accurate.
 *
 * Algorithm: entity-first (see ast.js for details).
 */

import { Parser, Language } from "../vendor/web-tree-sitter.js";

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
// Public API
// ---------------------------------------------------------------------------

/**
 * Build entity-level snippets from the GitHub API files array.
 *
 * @param {Object[]} files — /pulls/:n/files items, each with .patch, .raw_url
 * @returns {Promise<Snippet[]>}
 */
export async function buildSnippets(files) {
  const snippets = [];
  for (const file of files) {
    if (file.status === "removed") continue;
    const lang = detectLanguage(file.filename);
    snippets.push(...await buildFileSnippets(file, lang));
  }
  return snippets;
}

async function buildFileSnippets(file, lang) {
  const hunks = hunksFromPatch(file.patch ?? "");
  if (hunks.length === 0) return [];

  // Fetch full post-image source for accurate entity boundaries.
  let fullSource = null;
  if (file.raw_url) {
    try {
      const resp = await fetch(file.raw_url, { credentials: "include" });
      if (resp.ok) fullSource = await resp.text();
    } catch { /* ignore */ }
  }

  let entities = null;
  if (fullSource && lang) {
    entities = await extractEntitiesAST(fullSource, lang)
      ?? extractEntitiesHeuristic(fullSource, lang);
  }

  if (entities && entities.length > 0) {
    return mapHunksToEntities(file.filename, lang, hunks, entities);
  }

  return hunks.map((hunk) => makeSnippet(file.filename, lang, hunk, null, "hunk", hunk.lines));
}

// ---------------------------------------------------------------------------
// Entity extraction — tree-sitter
// ---------------------------------------------------------------------------

async function extractEntitiesAST(fullSource, lang) {
  try {
    await loadTreeSitter();
    const grammar = await getGrammar(lang);
    if (!grammar) return null;

    const parser = new Parser();
    parser.setLanguage(grammar);
    const tree = parser.parse(fullSource);

    const entities = [];
    collectTopLevelEntities(tree.rootNode, entities);
    return entities.length > 0 ? entities : null;
  } catch {
    return null;
  }
}

function collectTopLevelEntities(rootNode, entities) {
  const interestingTypes = new Set([
    "function_declaration", "function_definition", "function_item",
    "method_declaration", "method_definition",
    "class_declaration", "class_definition",
    "interface_declaration", "type_alias_declaration",
    "enum_declaration", "struct_item", "impl_item", "trait_item",
    "export_statement", "lexical_declaration", "variable_declaration",
    "type_declaration", "var_declaration", "const_declaration",
    "decorated_definition",
    "import_statement", "import_declaration", "use_declaration",
  ]);

  function visit(node, depth) {
    if (depth > 2) return;

    if (interestingTypes.has(node.type)) {
      const nameNode = node.childForFieldName?.("name")
        ?? (node.type === "export_statement" ? getExportName(node) : null);
      const name = nameNode?.text ?? "";
      const kind = nodeKind(node);

      if (kind === "imports") {
        const last = entities[entities.length - 1];
        const sl = node.startPosition.row + 1;
        const el = node.endPosition.row + 1;
        if (last?.kind === "imports" && sl <= last.endLine + 2) {
          last.endLine = Math.max(last.endLine, el);
          return;
        }
        entities.push({ name: "imports", kind: "imports", startLine: sl, endLine: el });
        return;
      }

      if (name || kind === "function" || kind === "class") {
        entities.push({
          name:      name || kind,
          kind,
          startLine: node.startPosition.row + 1,
          endLine:   node.endPosition.row + 1,
        });
        if (node.type !== "class_declaration" && node.type !== "class_definition") return;
      }
    }

    for (let i = 0; i < node.childCount; i++) {
      visit(node.child(i), depth + 1);
    }
  }

  visit(rootNode, 0);
}

function getExportName(exportNode) {
  for (let i = 0; i < exportNode.childCount; i++) {
    const child = exportNode.child(i);
    const n = child.childForFieldName?.("name");
    if (n) return n;
  }
  return null;
}

function nodeKind(node) {
  const t = node.type;
  if (t.includes("function") || t.includes("method") || t === "function_item") return "function";
  if (t.includes("class")) return "class";
  if (t.includes("interface") || t.includes("type_alias") || t.includes("struct") || t.includes("trait") || t.includes("enum") || t.includes("impl")) return "type";
  if (t.includes("import") || t === "use_declaration") return "imports";
  if (t.includes("const") || t.includes("var") || t.includes("lexical") || t.includes("variable")) return "var";
  return "other";
}

// ---------------------------------------------------------------------------
// Entity extraction — heuristic regex (same as ast.js)
// ---------------------------------------------------------------------------

function extractEntitiesHeuristic(source, lang) {
  const lines = source.split("\n");
  switch (lang) {
    case "go":         return extractGo(lines);
    case "javascript":
    case "typescript": return extractJS(lines);
    case "python":     return extractPython(lines);
    case "java":       return extractJava(lines);
    case "rust":       return extractRust(lines);
    default:           return null;
  }
}

function extractGo(lines) {
  const entities = [];
  let i = 0, n = lines.length, importStart = -1;
  const flush = (end) => { if (importStart >= 0) { entities.push({ name: "imports", kind: "imports", startLine: importStart + 1, endLine: end }); importStart = -1; } };
  while (i < n) {
    const line = lines[i].trimStart();
    if (/^import\s/.test(line)) { if (importStart < 0) importStart = i; if (line.includes("(")) { while (i < n && !lines[i].trimStart().startsWith(")")) i++; i++; } else i++; continue; }
    flush(i);
    const fm = line.match(/^func\s+(?:\([^)]+\)\s+)?(\w+)/); if (fm) { const e = goBraceEnd(lines, i); entities.push({ name: fm[1], kind: "function", startLine: i + 1, endLine: e }); i = e; continue; }
    const tm = line.match(/^type\s+(\w+)/); if (tm) { const e = goBraceEnd(lines, i); entities.push({ name: tm[1], kind: "type", startLine: i + 1, endLine: e }); i = e; continue; }
    const vm = line.match(/^(var|const)\s+(\w+)/); if (vm) { const s = i + 1; if (line.includes("(")) { while (i < n && !lines[i].trimStart().startsWith(")")) i++; i++; } else i++; entities.push({ name: vm[2], kind: vm[1], startLine: s, endLine: i }); continue; }
    i++;
  }
  flush(n);
  return entities;
}

function extractJS(lines) {
  const entities = [];
  let i = 0, n = lines.length, importStart = -1;
  const flush = (end) => { if (importStart >= 0) { entities.push({ name: "imports", kind: "imports", startLine: importStart + 1, endLine: end }); importStart = -1; } };
  while (i < n) {
    const line = lines[i].trimStart();
    if (/^import\s/.test(line) || /\brequire\s*\(/.test(line)) { if (importStart < 0) importStart = i; i++; continue; }
    flush(i);
    let m;
    if ((m = line.match(/^(?:export\s+(?:default\s+)?)?class\s+(\w+)/))) { const e = jsBraceEnd(lines, i); entities.push({ name: m[1], kind: "class", startLine: i + 1, endLine: e }); i = e; continue; }
    if ((m = line.match(/^(?:export\s+)?interface\s+(\w+)/))) { const e = jsBraceEnd(lines, i); entities.push({ name: m[1], kind: "type", startLine: i + 1, endLine: e }); i = e; continue; }
    if ((m = line.match(/^(?:export\s+)?(?:const\s+)?enum\s+(\w+)/))) { const e = jsBraceEnd(lines, i); entities.push({ name: m[1], kind: "const", startLine: i + 1, endLine: e }); i = e; continue; }
    if ((m = line.match(/^(?:export\s+(?:default\s+)?)?(?:async\s+)?function\s*\*?\s*(\w+)/))) { const e = line.includes("{") ? jsBraceEnd(lines, i) : i + 2; entities.push({ name: m[1], kind: "function", startLine: i + 1, endLine: e }); i = e; continue; }
    if ((m = line.match(/^(?:export\s+)?(?:const|let|var)\s+(\w+)/))) { const e = jsVarEnd(lines, i); entities.push({ name: m[1], kind: "var", startLine: i + 1, endLine: e }); i = e; continue; }
    i++;
  }
  flush(n);
  return entities;
}

function extractPython(lines) {
  const entities = [];
  let i = 0, n = lines.length, importStart = -1;
  const flush = (end) => { if (importStart >= 0) { entities.push({ name: "imports", kind: "imports", startLine: importStart + 1, endLine: end }); importStart = -1; } };
  while (i < n) {
    const line = lines[i];
    if (/^(?:import|from)\s/.test(line.trimStart())) { if (importStart < 0) importStart = i; i++; continue; }
    flush(i);
    let m;
    if ((m = line.match(/^class\s+(\w+)/))) { const e = pyBlockEnd(lines, i); entities.push({ name: m[1], kind: "class", startLine: i + 1, endLine: e }); i = e; continue; }
    if ((m = line.match(/^(?:async\s+)?def\s+(\w+)/))) { const e = pyBlockEnd(lines, i); entities.push({ name: m[1], kind: "function", startLine: i + 1, endLine: e }); i = e; continue; }
    i++;
  }
  flush(n);
  return entities;
}

function extractJava(lines) {
  const entities = [];
  let i = 0, n = lines.length, importStart = -1;
  const flush = (end) => { if (importStart >= 0) { entities.push({ name: "imports", kind: "imports", startLine: importStart + 1, endLine: end }); importStart = -1; } };
  while (i < n) {
    const line = lines[i].trimStart();
    if (/^import\s/.test(line)) { if (importStart < 0) importStart = i; i++; continue; }
    flush(i);
    const m = line.match(/^(?:(?:public|private|protected|abstract|final|static)\s+)*(?:class|interface|enum|record)\s+(\w+)/);
    if (m) { const e = goBraceEnd(lines, i); entities.push({ name: m[1], kind: "class", startLine: i + 1, endLine: e }); i = e; continue; }
    i++;
  }
  flush(n);
  return entities;
}

function extractRust(lines) {
  const entities = [];
  let i = 0, n = lines.length, importStart = -1;
  const flush = (end) => { if (importStart >= 0) { entities.push({ name: "imports", kind: "imports", startLine: importStart + 1, endLine: end }); importStart = -1; } };
  while (i < n) {
    const line = lines[i].trimStart();
    if (/^use\s/.test(line)) { if (importStart < 0) importStart = i; i++; continue; }
    flush(i);
    let m;
    if ((m = line.match(/^(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?fn\s+(\w+)/))) { const e = goBraceEnd(lines, i); entities.push({ name: m[1], kind: "function", startLine: i + 1, endLine: e }); i = e; continue; }
    if ((m = line.match(/^(?:pub(?:\([^)]*\))?\s+)?(?:struct|enum|trait|impl|type)\s+(\w+)/))) { const e = goBraceEnd(lines, i); entities.push({ name: m[1], kind: "type", startLine: i + 1, endLine: e }); i = e; continue; }
    i++;
  }
  flush(n);
  return entities;
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

function goBraceEnd(lines, startIdx) {
  let depth = 0, opened = false;
  for (let i = startIdx; i < lines.length; i++) {
    for (const ch of lines[i]) {
      if (ch === "{") { depth++; opened = true; }
      else if (ch === "}") depth--;
    }
    if (opened && depth === 0) return i + 1;
  }
  return lines.length;
}

function jsBraceEnd(lines, startIdx) {
  return goBraceEnd(lines, startIdx);
}

function jsVarEnd(lines, startIdx) {
  let depth = 0;
  for (const ch of lines[startIdx]) {
    if ("{[(".includes(ch)) depth++;
    else if ("}])".includes(ch)) depth--;
  }
  if (depth === 0) return startIdx + 1;
  for (let i = startIdx + 1; i < lines.length; i++) {
    for (const ch of lines[i]) {
      if ("{[(".includes(ch)) depth++;
      else if ("}])".includes(ch)) depth--;
    }
    if (depth <= 0) return i + 1;
  }
  return lines.length;
}

function pyBlockEnd(lines, startIdx) {
  const n = lines.length;
  const baseIndent = indentLen(lines[startIdx]);
  let bodyStart = startIdx;
  while (bodyStart < n) {
    if (lines[bodyStart].trimEnd().endsWith(":")) { bodyStart++; break; }
    bodyStart++;
  }
  for (let i = bodyStart; i < n; i++) {
    const l = lines[i], stripped = l.trim();
    if (stripped === "" || stripped.startsWith("#")) continue;
    if (indentLen(l) <= baseIndent) return i;
  }
  return n;
}

function mapHunksToEntities(filename, lang, hunks, entities) {
  const groups = new Map();
  const order = [];
  for (const hunk of hunks) {
    const entity = findEnclosingEntity(entities, hunk.newStart, hunk.newEnd);
    const key = entity ? `${entity.kind}::${entity.name}` : "__preamble__";
    if (!groups.has(key)) { order.push(key); groups.set(key, { entity, diffLines: [] }); }
    groups.get(key).diffLines.push(...hunk.lines);
  }
  return order.map((key) => {
    const { entity, diffLines } = groups.get(key);
    return makeSnippet(filename, lang, null, entity, entity?.kind ?? "hunk", diffLines);
  });
}

function findEnclosingEntity(entities, hStart, hEnd) {
  let best = null, bestSpan = Infinity;
  for (const e of entities) {
    if (e.startLine <= hStart && e.endLine >= hEnd) {
      const span = e.endLine - e.startLine;
      if (span < bestSpan) { best = e; bestSpan = span; }
    }
  }
  if (best) return best;
  let bestOverlap = 0;
  for (const e of entities) {
    const overlap = Math.min(e.endLine, hEnd) - Math.max(e.startLine, hStart) + 1;
    if (overlap > bestOverlap) { bestOverlap = overlap; best = e; }
  }
  return best;
}

function makeSnippet(filePath, lang, hunk, entity, nodeType, diffLines) {
  const lines = diffLines ?? hunk?.lines ?? [];
  const symbolPath = entity ? entity.name : "";
  const startLine  = entity ? entity.startLine : (hunk?.newStart ?? 1);
  const endLine    = entity ? entity.endLine   : (hunk?.newEnd   ?? 1);
  return {
    id:        hashString(`${filePath}::${symbolPath}::${startLine}`),
    filePath,
    language:  lang ?? "unknown",
    symbolPath,
    nodeType,
    startLine,
    endLine,
    diffLines: lines,
    depth:     0,
    status:    "pending",
  };
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

const EXT_LANG = {
  js: "javascript", jsx: "javascript", mjs: "javascript", cjs: "javascript",
  ts: "typescript", tsx: "typescript", mts: "typescript",
  py: "python", go: "go", java: "java", rs: "rust",
};

function detectLanguage(filename) {
  const ext = filename.split(".").pop()?.toLowerCase();
  return EXT_LANG[ext] ?? null;
}

function indentLen(line) {
  let i = 0;
  while (i < line.length && (line[i] === " " || line[i] === "\t")) i++;
  return i;
}

function hashString(s) {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = Math.imul(31, h) + s.charCodeAt(i) | 0;
  return (h >>> 0).toString(16);
}
