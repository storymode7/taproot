/**
 * shared/lib/astra.js — the single browser-side Astra pipeline.
 *
 * SOURCE OF TRUTH lives in /shared/lib; the copies under
 * apps/pwa/lib/shared/ and apps/extension/lib/shared/ are written by
 * scripts/sync-extension-lib.sh (extensions cannot reference files outside
 * their package, and the PWA must be self-contained for static hosting).
 * Never edit the copies.
 *
 * All review logic (entity mapping, hunk splitting, call edges, review
 * tree, ordering) runs in the Go core compiled to WASM (vendor/core/
 * astra-core.wasm) — the same code the CLI runs natively, parity-tested
 * in core/testdata/parity. This module only:
 *   1. parses GitHub patches into wire hunks,
 *   2. extracts entity spans with web-tree-sitter (best-effort; the core
 *      falls back to its own heuristics when none are supplied),
 *   3. calls astraCore.process() and returns the response.
 *
 * Environment differences are injected through an adapter:
 *   initAstra({
 *     coreBase,            // base URL for vendor/core (wasm_exec.js, astra-core.wasm)
 *     grammarURL(file),    // absolute/relative URL for a grammar .wasm
 *     initParser(),        // → { newParser(), loadLanguage(url) } or null
 *     fetchSource(file),   // → full post-image source string or null
 *   })
 */

export function initAstra(adapter) {
  let corePromise = null;
  let lastFiles = null;

  async function loadCore() {
    if (corePromise) return corePromise;
    corePromise = (async () => {
      await loadClassicScript(`${adapter.coreBase}/wasm_exec.js`);
      const go = new globalThis.Go();
      const resp = await fetch(`${adapter.coreBase}/astra-core.wasm`);
      let instance;
      try {
        ({ instance } = await WebAssembly.instantiateStreaming(resp, go.importObject));
      } catch {
        // Some static hosts serve .wasm without the right MIME type.
        const buf = await (await fetch(`${adapter.coreBase}/astra-core.wasm`)).arrayBuffer();
        ({ instance } = await WebAssembly.instantiate(buf, go.importObject));
      }
      go.run(instance); // resolves only on exit; astraCore installs at startup
      for (let i = 0; i < 200 && !globalThis.astraCore; i++) {
        await new Promise((r) => setTimeout(r, 10));
      }
      if (!globalThis.astraCore) throw new Error("astra-core.wasm did not install astraCore");
      return globalThis.astraCore;
    })();
    return corePromise;
  }

  /**
   * Build the full review from GitHub REST /pulls/:n/files items.
   * Returns the core response: { snippets, edges, tree, flatOrder }.
   * Snippets arrive in flattened reading order with a `status` field
   * initialised to "pending".
   */
  async function buildReview(files, order = "pre") {
    const wireFiles = [];
    for (const file of files) {
      if (file.status === "removed") continue;
      const hunks = hunksFromPatch(file.patch ?? "");
      if (hunks.length === 0) continue;
      const lang = detectLanguage(file.filename);

      let source = "";
      let entities = [];
      if (lang) {
        source = (await adapter.fetchSource(file)) ?? "";
        if (source) {
          entities = (await extractEntitiesAST(adapter, source, lang)) ?? [];
        }
      }

      wireFiles.push({
        path: file.filename,
        oldPath: file.previous_filename ?? file.filename,
        language: lang ?? "",
        isNew: file.status === "added",
        isDeleted: false,
        isBinary: false,
        hunks,
        source,
        entities,
      });
    }
    lastFiles = wireFiles;
    return processFiles(wireFiles, order);
  }

  /** Re-run ordering on the last built review without re-fetching. */
  async function reorderReview(order) {
    if (!lastFiles) throw new Error("no review built yet");
    return processFiles(lastFiles, order);
  }

  async function processFiles(wireFiles, order) {
    const core = await loadCore();
    const resp = JSON.parse(
      core.process(JSON.stringify({ files: wireFiles, options: { order } })),
    );
    if (resp.error) throw new Error(`astra core: ${resp.error}`);
    for (const s of resp.snippets ?? []) s.status = "pending";
    return resp;
  }

  return { buildReview, reorderReview, loadCore };
}

// ---------------------------------------------------------------------------
// Entity extraction via web-tree-sitter (best effort — null on any failure;
// the core's heuristics take over)
// ---------------------------------------------------------------------------

const LANG_WASM = {
  javascript: "tree-sitter-javascript.wasm",
  typescript: "tree-sitter-typescript.wasm",
  python: "tree-sitter-python.wasm",
  go: "tree-sitter-go.wasm",
  java: "tree-sitter-java.wasm",
  rust: "tree-sitter-rust.wasm",
};

const grammarCache = new Map();

async function extractEntitiesAST(adapter, fullSource, lang) {
  try {
    const wasm = LANG_WASM[lang];
    if (!wasm || !adapter.initParser) return null;
    const ts = await adapter.initParser();
    if (!ts) return null;

    let grammar = grammarCache.get(lang);
    if (!grammar) {
      grammar = await ts.loadLanguage(adapter.grammarURL(wasm));
      grammarCache.set(lang, grammar);
    }
    const parser = ts.newParser();
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
    "type_declaration", "var_declaration", "const_declaration",
    // Python
    "decorated_definition",
    // Imports (grouped)
    "import_statement", "import_declaration", "use_declaration",
  ]);

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
          endLine: node.endPosition.row + 1,
        });
        // Don't recurse into non-class bodies.
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
// Patch / diff helpers
// ---------------------------------------------------------------------------

/** Parse a GitHub unified patch into wire hunks for the core. */
export function hunksFromPatch(patch) {
  const hunks = [];
  let current = null;
  for (const line of patch.split("\n")) {
    const m = line.match(/^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/);
    if (m) {
      if (current) hunks.push(current);
      current = {
        oldStart: Number(m[1]),
        oldLines: m[2] !== undefined ? Number(m[2]) : 1,
        newStart: Number(m[3]),
        newLines: m[4] !== undefined ? Number(m[4]) : 1,
        lines: [line],
      };
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

export function detectLanguage(filename) {
  const ext = filename.split(".").pop()?.toLowerCase();
  return EXT_LANG[ext] ?? null;
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

/** Load a classic (non-module) script once. */
export function loadClassicScript(src) {
  return new Promise((resolve, reject) => {
    if (document.querySelector(`script[src="${src}"]`)) { resolve(); return; }
    const s = document.createElement("script");
    s.src = src;
    s.onload = resolve;
    s.onerror = () => reject(new Error(`Failed to load ${src}`));
    document.head.appendChild(s);
  });
}
