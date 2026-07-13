/**
 * lib/ast.js — tree-sitter WASM loader and entity-aware snippet builder
 *
 * Algorithm (inverted from the old hunk-centric approach):
 *
 *  OLD: for each hunk → find smallest enclosing AST node
 *       Problem: partial source from patch → broken AST; node often = whole file.
 *
 *  NEW: for each file:
 *    1. Parse the FULL post-image source into an AST.
 *    2. Walk the AST to extract top-level entity spans (functions, classes, etc.)
 *       — these are the "interesting" units a reviewer cares about.
 *    3. Parse the patch into hunks. For each hunk, find which entity it overlaps
 *       (prefer exact containment; fall back to maximum overlap).
 *    4. Merge all hunks that share the same entity into one Snippet.
 *    5. Hunks that fall outside any entity land in a shared "preamble" snippet.
 *
 * Full source is fetched from GitHub's raw_url / contents_url so AST positions
 * are accurate. Falls back to heuristic regex entity extraction when tree-sitter
 * is unavailable or the language is unsupported.
 */

const VENDOR = "../../vendor/grammars";

const LANG_WASM = {
  javascript:  "tree-sitter-javascript.wasm",
  typescript:  "tree-sitter-typescript.wasm",
  python:      "tree-sitter-python.wasm",
  go:          "tree-sitter-go.wasm",
  java:        "tree-sitter-java.wasm",
  rust:        "tree-sitter-rust.wasm",
};

let TreeSitter = null;
const grammarCache = new Map();

export async function loadTreeSitter() {
  if (TreeSitter) return TreeSitter;
  await loadScript(`${VENDOR}/web-tree-sitter.js`);
  await window.TreeSitter.init({ locateFile: () => `${VENDOR}/tree-sitter.wasm` });
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
  const grammar = await ts.Language.load(`${VENDOR}/${wasm}`);
  grammarCache.set(lang, grammar);
  return grammar;
}

// ---------------------------------------------------------------------------
// Snippet model (unchanged)
// ---------------------------------------------------------------------------
/**
 * @typedef {Object} Snippet
 * @property {string}   id
 * @property {string}   filePath
 * @property {string}   language
 * @property {string}   symbolPath   — entity name, e.g. "MyClass.myMethod" or ""
 * @property {string}   nodeType     — "function" | "class" | "type" | "imports" | "hunk" | …
 * @property {number}   startLine    — 1-indexed in post-image
 * @property {number}   endLine
 * @property {string[]} diffLines    — raw unified-diff lines for this snippet
 * @property {number}   depth        — call-graph depth (set by ordering engine)
 * @property {string}   status       — "pending" | "reviewed" | "accepted" | "rejected"
 */

/**
 * Build snippets from the GitHub API files array.
 *
 * For supported languages, fetches the full post-image source and extracts
 * entity boundaries using tree-sitter. Hunks are then mapped to entities and
 * merged. Falls back to heuristic regex extraction, then to whole-file snippet.
 *
 * @param {Object[]} files — GitHub REST /pulls/:n/files items (with raw_url)
 * @returns {Promise<Snippet[]>}
 */
export async function buildSnippets(files) {
  const snippets = [];
  for (const file of files) {
    if (file.status === "removed") continue;
    const lang = detectLanguage(file.filename);
    const fileSnippets = await buildFileSnippets(file, lang);
    snippets.push(...fileSnippets);
  }
  return snippets;
}

async function buildFileSnippets(file, lang) {
  const hunks = hunksFromPatch(file.patch ?? "");
  if (hunks.length === 0) return [];

  // Try to get full source for accurate entity boundaries.
  let fullSource = null;
  if (file.raw_url) {
    try {
      const resp = await fetch(file.raw_url);
      if (resp.ok) fullSource = await resp.text();
    } catch { /* ignore fetch errors */ }
  }

  // Extract entity spans from full source.
  let entities = null;
  if (fullSource && lang) {
    // Try tree-sitter first (most accurate).
    entities = await extractEntitiesAST(fullSource, lang);
    // Fall back to regex heuristics.
    if (!entities) entities = extractEntitiesHeuristic(fullSource, lang);
  }

  if (entities && entities.length > 0) {
    return mapHunksToEntities(file.filename, lang, hunks, entities);
  }

  // Last resort: one snippet per hunk, no entity grouping.
  return hunks.map((hunk) => makeSnippet(file.filename, lang, hunk, null, "hunk"));
}

// ---------------------------------------------------------------------------
// Entity extraction — tree-sitter (accurate, requires WASM)
// ---------------------------------------------------------------------------

/**
 * Walk the AST of fullSource and return all top-level named entity spans.
 * Returns null on error or unsupported language.
 *
 * @returns {Promise<Array<{name, kind, startLine, endLine}> | null>}
 */
async function extractEntitiesAST(fullSource, lang) {
  try {
    await loadTreeSitter();
    const grammar = await getGrammar(lang);
    if (!grammar) return null;

    const ts = await loadTreeSitter();
    const parser = new ts.Parser();
    parser.setLanguage(grammar);
    const tree = parser.parse(fullSource);

    const entities = [];
    collectTopLevelEntities(tree.rootNode, entities);
    return entities.length > 0 ? entities : null;
  } catch {
    return null;
  }
}

/**
 * Walk the AST and collect top-level named declarations as entities.
 * "Top-level" means depth 0 or 1 (class body members are included).
 */
function collectTopLevelEntities(rootNode, entities) {
  const interestingTypes = new Set([
    // Generic
    "function_declaration", "function_definition", "function_item",
    "method_declaration", "method_definition",
    "class_declaration", "class_definition",
    "interface_declaration", "type_alias_declaration",
    "enum_declaration", "struct_item", "impl_item", "trait_item",
    // TS / JS
    "export_statement", "lexical_declaration", "variable_declaration",
    // Go
    "function_declaration", "method_declaration", "type_declaration",
    "var_declaration", "const_declaration",
    // Python
    "decorated_definition",
    // Imports (grouped)
    "import_statement", "import_declaration", "use_declaration",
  ]);

  // Single-pass: collect at depth <= 2.
  function visit(node, depth) {
    if (depth > 2) return;

    if (interestingTypes.has(node.type)) {
      const nameNode = getNameNode(node);
      const name = nameNode ? nameNode.text : "";
      const kind = nodeKind(node);

      if (kind === "imports") {
        // Merge consecutive import lines into one entity.
        const last = entities[entities.length - 1];
        const sl = node.startPosition.row + 1;
        const el = node.endPosition.row + 1;
        if (last && last.kind === "imports" && sl <= last.endLine + 2) {
          last.endLine = Math.max(last.endLine, el);
          return;
        }
        entities.push({ name: "imports", kind: "imports", startLine: sl, endLine: el });
        return;
      }

      if (name || kind === "function" || kind === "class") {
        entities.push({
          name: name || kind,
          kind,
          startLine: node.startPosition.row + 1,
          endLine:   node.endPosition.row + 1,
        });
        // Don't recurse into class body — method definitions are handled by depth=2 below.
        if (node.type !== "class_declaration" && node.type !== "class_definition") return;
      }
    }

    for (let i = 0; i < node.childCount; i++) {
      visit(node.child(i), depth + 1);
    }
  }

  visit(rootNode, 0);
}

function getNameNode(node) {
  // Try the standard "name" field first.
  const byField = node.childForFieldName?.("name");
  if (byField) return byField;
  // For export_statement wrapping a declaration, recurse one level.
  if (node.type === "export_statement") {
    for (let i = 0; i < node.childCount; i++) {
      const child = node.child(i);
      const n = child.childForFieldName?.("name");
      if (n) return n;
    }
  }
  return null;
}

function nodeKind(node) {
  const t = node.type;
  if (t.includes("function") || t.includes("method") || t === "function_item") return "function";
  if (t.includes("class")) return "class";
  if (t.includes("interface") || t.includes("type_alias") || t.includes("struct") || t.includes("trait") || t.includes("enum") || t.includes("impl")) return "type";
  if (t.includes("import") || t.includes("use_declaration")) return "imports";
  if (t.includes("const") || t.includes("var") || t.includes("lexical") || t.includes("variable")) return "var";
  return "other";
}

// ---------------------------------------------------------------------------
// Entity extraction — heuristic regex (fast, no WASM)
// ---------------------------------------------------------------------------

/**
 * Extract entity spans using language-specific regexes.
 * Mirrors the Go engine/entities.go logic.
 *
 * @returns {Array<{name, kind, startLine, endLine}> | null}
 */
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

// --- Go ---
function extractGo(lines) {
  const entities = [];
  let i = 0, n = lines.length;
  let importStart = -1;

  const flushImports = (end) => {
    if (importStart >= 0) {
      entities.push({ name: "imports", kind: "imports", startLine: importStart + 1, endLine: end });
      importStart = -1;
    }
  };

  while (i < n) {
    const line = lines[i].trimStart();
    if (/^import\s/.test(line)) {
      if (importStart < 0) importStart = i;
      if (line.includes("(")) { while (i < n && !lines[i].trimStart().startsWith(")")) i++; i++; }
      else i++;
      continue;
    }
    flushImports(i);

    const funcM = line.match(/^func\s+(?:\([^)]+\)\s+)?(\w+)/);
    if (funcM) {
      const start = i + 1, end = goBraceEnd(lines, i);
      entities.push({ name: funcM[1], kind: "function", startLine: start, endLine: end });
      i = end; continue;
    }
    const typeM = line.match(/^type\s+(\w+)/);
    if (typeM) {
      const start = i + 1, end = goBraceEnd(lines, i);
      entities.push({ name: typeM[1], kind: "type", startLine: start, endLine: end });
      i = end; continue;
    }
    const varM = line.match(/^(var|const)\s+(\w+)/);
    if (varM) {
      const kind = varM[1], name = varM[2], start = i + 1;
      if (line.includes("(")) { while (i < n && !lines[i].trimStart().startsWith(")")) i++; i++; }
      else i++;
      entities.push({ name, kind, startLine: start, endLine: i });
      continue;
    }
    i++;
  }
  flushImports(n);
  return entities;
}

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

// --- JS / TS ---
function extractJS(lines) {
  const entities = [];
  let i = 0, n = lines.length;
  let importStart = -1;

  const flushImports = (end) => {
    if (importStart >= 0) {
      entities.push({ name: "imports", kind: "imports", startLine: importStart + 1, endLine: end });
      importStart = -1;
    }
  };

  while (i < n) {
    const line = lines[i].trimStart();
    if (/^import\s/.test(line) || /\brequire\s*\(/.test(line)) {
      if (importStart < 0) importStart = i;
      i++; continue;
    }
    flushImports(i);

    const classM = line.match(/^(?:export\s+(?:default\s+)?)?class\s+(\w+)/);
    if (classM) {
      const end = jsBraceEnd(lines, i);
      entities.push({ name: classM[1], kind: "class", startLine: i + 1, endLine: end });
      i = end; continue;
    }
    const ifaceM = line.match(/^(?:export\s+)?interface\s+(\w+)/);
    if (ifaceM) {
      const end = jsBraceEnd(lines, i);
      entities.push({ name: ifaceM[1], kind: "type", startLine: i + 1, endLine: end });
      i = end; continue;
    }
    const enumM = line.match(/^(?:export\s+)?(?:const\s+)?enum\s+(\w+)/);
    if (enumM) {
      const end = jsBraceEnd(lines, i);
      entities.push({ name: enumM[1], kind: "const", startLine: i + 1, endLine: end });
      i = end; continue;
    }
    const funcM = line.match(/^(?:export\s+(?:default\s+)?)?(?:async\s+)?function\s*\*?\s*(\w+)/);
    if (funcM) {
      const end = line.includes("{") ? jsBraceEnd(lines, i) : i + 2;
      entities.push({ name: funcM[1], kind: "function", startLine: i + 1, endLine: end });
      i = end; continue;
    }
    const varM = line.match(/^(?:export\s+)?(?:const|let|var)\s+(\w+)/);
    if (varM) {
      const end = jsVarEnd(lines, i);
      entities.push({ name: varM[1], kind: "var", startLine: i + 1, endLine: end });
      i = end; continue;
    }
    i++;
  }
  flushImports(n);
  return entities;
}

function jsBraceEnd(lines, startIdx) {
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

function jsVarEnd(lines, startIdx) {
  let depth = 0;
  for (const ch of lines[startIdx]) {
    if (ch === "{" || ch === "[" || ch === "(") depth++;
    else if (ch === "}" || ch === "]" || ch === ")") depth--;
  }
  if (depth === 0) return startIdx + 1;
  for (let i = startIdx + 1; i < lines.length; i++) {
    for (const ch of lines[i]) {
      if (ch === "{" || ch === "[" || ch === "(") depth++;
      else if (ch === "}" || ch === "]" || ch === ")") depth--;
    }
    if (depth <= 0) return i + 1;
  }
  return lines.length;
}

// --- Python ---
function extractPython(lines) {
  const entities = [];
  let i = 0, n = lines.length;
  let importStart = -1;

  const flushImports = (end) => {
    if (importStart >= 0) {
      entities.push({ name: "imports", kind: "imports", startLine: importStart + 1, endLine: end });
      importStart = -1;
    }
  };

  while (i < n) {
    const line = lines[i];
    const stripped = line.trimStart();
    if (/^(?:import|from)\s/.test(stripped)) {
      if (importStart < 0) importStart = i;
      i++; continue;
    }
    flushImports(i);
    const classM = line.match(/^class\s+(\w+)/);
    if (classM) {
      const end = pyBlockEnd(lines, i);
      entities.push({ name: classM[1], kind: "class", startLine: i + 1, endLine: end });
      i = end; continue;
    }
    const funcM = line.match(/^(?:async\s+)?def\s+(\w+)/);
    if (funcM) {
      const end = pyBlockEnd(lines, i);
      entities.push({ name: funcM[1], kind: "function", startLine: i + 1, endLine: end });
      i = end; continue;
    }
    i++;
  }
  flushImports(n);
  return entities;
}

function pyBlockEnd(lines, startIdx) {
  const n = lines.length;
  const baseIndent = indentLen(lines[startIdx]);

  // Skip past the signature (multi-line def/class) until we see the opening colon.
  let bodyStart = startIdx;
  while (bodyStart < n) {
    const stripped = lines[bodyStart].trimEnd();
    if (stripped.endsWith(":")) { bodyStart++; break; }
    bodyStart++;
  }

  for (let i = bodyStart; i < n; i++) {
    const l = lines[i];
    const stripped = l.trim();
    if (stripped === "" || stripped.startsWith("#")) continue;
    if (indentLen(l) <= baseIndent) return i;
  }
  return n;
}

// --- Java ---
function extractJava(lines) {
  const entities = [];
  let i = 0, n = lines.length;
  let importStart = -1;

  const flushImports = (end) => {
    if (importStart >= 0) {
      entities.push({ name: "imports", kind: "imports", startLine: importStart + 1, endLine: end });
      importStart = -1;
    }
  };

  while (i < n) {
    const line = lines[i].trimStart();
    if (/^import\s/.test(line)) {
      if (importStart < 0) importStart = i;
      i++; continue;
    }
    flushImports(i);
    const classM = line.match(/^(?:(?:public|private|protected|abstract|final|static)\s+)*(?:class|interface|enum|record)\s+(\w+)/);
    if (classM) {
      const end = goBraceEnd(lines, i);
      entities.push({ name: classM[1], kind: "class", startLine: i + 1, endLine: end });
      i = end; continue;
    }
    i++;
  }
  flushImports(n);
  return entities;
}

// --- Rust ---
function extractRust(lines) {
  const entities = [];
  let i = 0, n = lines.length;
  let importStart = -1;

  const flushImports = (end) => {
    if (importStart >= 0) {
      entities.push({ name: "imports", kind: "imports", startLine: importStart + 1, endLine: end });
      importStart = -1;
    }
  };

  while (i < n) {
    const line = lines[i].trimStart();
    if (/^use\s/.test(line)) {
      if (importStart < 0) importStart = i;
      i++; continue;
    }
    flushImports(i);
    const fnM = line.match(/^(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?fn\s+(\w+)/);
    if (fnM) {
      const end = goBraceEnd(lines, i);
      entities.push({ name: fnM[1], kind: "function", startLine: i + 1, endLine: end });
      i = end; continue;
    }
    const structM = line.match(/^(?:pub(?:\([^)]*\))?\s+)?(?:struct|enum|trait|impl|type)\s+(\w+)/);
    if (structM) {
      const end = goBraceEnd(lines, i);
      entities.push({ name: structM[1], kind: "type", startLine: i + 1, endLine: end });
      i = end; continue;
    }
    i++;
  }
  flushImports(n);
  return entities;
}

// ---------------------------------------------------------------------------
// Hunk-to-entity mapping
// ---------------------------------------------------------------------------

/**
 * Map hunks to their enclosing entities and merge hunks sharing an entity.
 *
 * @param {string}   filename
 * @param {string}   lang
 * @param {Object[]} hunks    — { newStart, newEnd, lines }
 * @param {Object[]} entities — { name, kind, startLine, endLine }
 * @returns {Snippet[]}
 */
function mapHunksToEntities(filename, lang, hunks, entities) {
  // key → { entity, diffLines[] }
  const groups = new Map();
  const order = [];

  for (const hunk of hunks) {
    const entity = findEnclosingEntity(entities, hunk.newStart, hunk.newEnd);
    const key = entity ? `${entity.kind}::${entity.name}` : "__preamble__";

    if (!groups.has(key)) {
      order.push(key);
      groups.set(key, { entity, diffLines: [] });
    }
    groups.get(key).diffLines.push(...hunk.lines);
  }

  return order.map((key) => {
    const { entity, diffLines } = groups.get(key);
    return makeSnippet(filename, lang, null, entity, entity?.kind ?? "hunk", diffLines);
  });
}

/**
 * Find the best entity for a hunk [hStart, hEnd].
 * Prefers exact containment; falls back to maximum line overlap.
 */
function findEnclosingEntity(entities, hStart, hEnd) {
  // Pass 1: exact containment, smallest span wins.
  let best = null, bestSpan = Infinity;
  for (const e of entities) {
    if (e.startLine <= hStart && e.endLine >= hEnd) {
      const span = e.endLine - e.startLine;
      if (span < bestSpan) { best = e; bestSpan = span; }
    }
  }
  if (best) return best;

  // Pass 2: maximum overlap (hunk extends past entity end, or straddles boundary).
  let bestOverlap = 0;
  for (const e of entities) {
    const overlapStart = Math.max(e.startLine, hStart);
    const overlapEnd   = Math.min(e.endLine, hEnd);
    const overlap = overlapEnd - overlapStart + 1;
    if (overlap > bestOverlap) { bestOverlap = overlap; best = e; }
  }
  return best;
}

// ---------------------------------------------------------------------------
// Snippet construction
// ---------------------------------------------------------------------------

function makeSnippet(filePath, lang, hunk, entity, nodeType, diffLines) {
  // diffLines may be passed directly (merged) or derived from a single hunk.
  const lines = diffLines ?? hunk?.lines ?? [];
  const symbolPath = entity ? entity.name : "";
  const startLine  = entity ? entity.startLine : (hunk?.newStart ?? 1);
  const endLine    = entity ? entity.endLine   : (hunk?.newEnd   ?? 1);

  return {
    id:         hashString(`${filePath}::${symbolPath}::${startLine}`),
    filePath,
    language:   lang ?? "unknown",
    symbolPath,
    nodeType,
    startLine,
    endLine,
    diffLines:  lines,
    depth:      0,
    status:     "pending",
  };
}

// ---------------------------------------------------------------------------
// Patch / diff helpers
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Language detection
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

function indentLen(line) {
  let i = 0;
  while (i < line.length && (line[i] === " " || line[i] === "\t")) i++;
  return i;
}

function hashString(s) {
  let h = 0;
  for (let i = 0; i < s.length; i++) {
    h = Math.imul(31, h) + s.charCodeAt(i) | 0;
  }
  return (h >>> 0).toString(16);
}
