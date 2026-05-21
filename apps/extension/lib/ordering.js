/**
 * lib/ordering.js — Symbol graph builder and snippet ordering engine
 *
 * Three presets:
 *   "top-down"  — entry-point first, BFS outward by call/import depth.
 *   "bottom-up" — leaves first, ending at entry point.
 *   "clusters"  — group by connected component, top-down within each cluster.
 */

/**
 * Build a lightweight cross-file symbol graph from import statements
 * found in the diff lines.
 *
 * This is a heuristic pass over the unified diff text (no full-file AST
 * available in the PWA for now).  The CLI version will use full tree-sitter
 * query passes over complete file blobs for more accurate results.
 *
 * @param {import('./ast.js').Snippet[]} snippets
 * @returns {Map<string, Set<string>>} snippetId → Set<snippetId it imports/calls>
 */
export function buildSymbolGraph(snippets) {
  const graph = new Map(); // snippetId → Set<snippetId>

  // Index snippets by filePath for cross-reference resolution.
  const byFile = new Map(); // filePath → Snippet[]
  for (const s of snippets) {
    if (!byFile.has(s.filePath)) byFile.set(s.filePath, []);
    byFile.get(s.filePath).push(s);
    graph.set(s.id, new Set());
  }

  // Heuristic: scan diff lines for import/require statements.
  // If a snippet imports a file that also has snippets in the diff, add an edge.
  for (const s of snippets) {
    const imports = extractImportPaths(s.diffLines, s.filePath);
    for (const importPath of imports) {
      const targets = byFile.get(importPath) ?? byFile.get(importPath + ".ts") ??
                      byFile.get(importPath + ".js") ?? byFile.get(importPath + "/index.ts");
      if (targets) {
        for (const t of targets) graph.get(s.id).add(t.id);
      }
    }
  }

  return graph;
}

/**
 * Order snippets according to the selected preset.
 *
 * @param {import('./ast.js').Snippet[]} snippets
 * @param {Map<string, Set<string>>} graph
 * @param {"top-down"|"bottom-up"|"clusters"} preset
 * @returns {import('./ast.js').Snippet[]}
 */
export function orderSnippets(snippets, graph, preset) {
  if (snippets.length === 0) return snippets;

  const byId = new Map(snippets.map((s) => [s.id, s]));

  // Compute in-degree (how many other snippets import this one).
  const inDegree = new Map(snippets.map((s) => [s.id, 0]));
  for (const [, edges] of graph) {
    for (const target of edges) {
      inDegree.set(target, (inDegree.get(target) ?? 0) + 1);
    }
  }

  // Assign depth by BFS from roots (nodes with in-degree 0).
  const depth = new Map(snippets.map((s) => [s.id, 0]));
  const queue = snippets.filter((s) => (inDegree.get(s.id) ?? 0) === 0).map((s) => s.id);
  const visited = new Set(queue);
  let qi = 0;
  while (qi < queue.length) {
    const id = queue[qi++];
    const d = depth.get(id) ?? 0;
    for (const neighbor of graph.get(id) ?? []) {
      if (!visited.has(neighbor)) {
        visited.add(neighbor);
        depth.set(neighbor, d + 1);
        queue.push(neighbor);
      }
    }
  }
  // Unreachable snippets (cycles) get max depth + 1.
  const maxDepth = Math.max(0, ...depth.values());
  for (const s of snippets) {
    if (!visited.has(s.id)) depth.set(s.id, maxDepth + 1);
    s.depth = depth.get(s.id) ?? 0;
  }

  switch (preset) {
    case "top-down":
      return [...snippets].sort((a, b) => a.depth - b.depth || a.filePath.localeCompare(b.filePath));

    case "bottom-up":
      return [...snippets].sort((a, b) => b.depth - a.depth || a.filePath.localeCompare(b.filePath));

    case "clusters": {
      // Group by connected component (undirected), then top-down within each.
      const components = connectedComponents(snippets, graph);
      const ordered = [];
      for (const component of components) {
        const sorted = [...component].sort((a, b) => a.depth - b.depth || a.filePath.localeCompare(b.filePath));
        ordered.push(...sorted);
      }
      return ordered;
    }

    default:
      return snippets;
  }
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Union-find connected components (undirected view of the graph). */
function connectedComponents(snippets, graph) {
  const parent = new Map(snippets.map((s) => [s.id, s.id]));

  function find(x) {
    if (parent.get(x) !== x) parent.set(x, find(parent.get(x)));
    return parent.get(x);
  }

  function union(x, y) {
    parent.set(find(x), find(y));
  }

  for (const [from, edges] of graph) {
    for (const to of edges) union(from, to);
  }

  const groups = new Map();
  for (const s of snippets) {
    const root = find(s.id);
    if (!groups.has(root)) groups.set(root, []);
    groups.get(root).push(s);
  }

  // Sort components largest-first so the main feature cluster comes first.
  return [...groups.values()].sort((a, b) => b.length - a.length);
}

/**
 * Heuristically extract imported file paths from diff lines.
 * Handles JS/TS import, Python from-import, Go import, Java import.
 */
function extractImportPaths(lines, fromFile) {
  const paths = [];
  const dir = fromFile.includes("/") ? fromFile.split("/").slice(0, -1).join("/") : "";

  for (const line of lines) {
    if (line.startsWith("-")) continue; // only look at added/context lines

    // JS/TS: import ... from './foo'  or  require('./foo')
    let m = line.match(/(?:import\s+.*?\s+from|require)\s*\(?['"]([^'"]+)['"]\)?/);
    if (m) {
      paths.push(resolveRelative(m[1], dir));
      continue;
    }

    // Python: from .foo import bar  or  import foo.bar
    m = line.match(/^[ +]from\s+([\w.]+)\s+import/);
    if (m) { paths.push(m[1].replace(/\./g, "/")); continue; }

    // Go: import "foo/bar/baz"
    m = line.match(/^[ +]\s*"([\w/.-]+)"/);
    if (m) { paths.push(m[1]); continue; }
  }

  return paths;
}

function resolveRelative(importPath, dir) {
  if (!importPath.startsWith(".")) return importPath;
  const parts = `${dir}/${importPath}`.split("/");
  const resolved = [];
  for (const p of parts) {
    if (p === "..") resolved.pop();
    else if (p !== ".") resolved.push(p);
  }
  return resolved.join("/");
}
