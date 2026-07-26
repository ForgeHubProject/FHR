// The name→glTF-node-index bridge: the one place that reconciles how a node is
// *named* in a diff with where it *lives* in the file.
//
// glTF has no stable node identity — only array indices, with optional names
// (#40, KhronosGroup/glTF#2337). Everything downstream of the file therefore
// keys on names, and every layer mangles them differently: the handler keeps the
// raw name (falling back to `node[i]`), ForgeHub's ingest strips `[].:/ ` from
// them, and this renderer used to lowercase-and-hyphenate them (`slugify`).
// Matching mangled names against each other is how "Cube.001" silently stops
// being highlighted.
//
// So: resolve a diff label to a node *index* here, once, against the same JSON
// the handler read — exact match first, then a single normalisation that is a
// superset of every mangling above. Downstream code deals only in indices, which
// no layer can rewrite. Pure: no three.js, no DOM.

import type { GltfDocument, GltfNode } from "./gltf-parse.js";

/**
 * The display key the handler uses for a node: its name, or `node[i]` when the
 * name is absent or empty. Mirrors handler-gltf-scene's `nodeName`, and must
 * keep mirroring it — this is the contract the diff's labels are written in.
 */
export function nodeKey(node: Pick<GltfNode, "name"> | undefined, index: number): string {
  const name = node?.name;
  return name !== undefined && name !== "" ? name : `node[${index}]`;
}

/**
 * Collapse every known mangling of a name to one comparable form: case,
 * separators (`. - _ space`), and bracket/path punctuation (`[]():/\`) are all
 * removed, because at least one layer in the stack removes each of them.
 *
 *   "Cube.001" → "cube001"   (raw, as the handler emits it)
 *   "Cube001"  → "cube001"   (ForgeHub ingest's sanitizeNodeName)
 *   "cube-001" → "cube001"   (this renderer's retired slugify)
 *   "node[3]"  → "node3"     (the unnamed-node fallback)
 */
export function normalizeName(name: string): string {
  return name.replace(/[[\]().:/\\ _\-]+/g, "").toLowerCase();
}

export type NameIndex = {
  /** Display key → node indices, document order. */
  byKey: Map<string, number[]>;
  /** Normalised key → node indices, document order. */
  byNormalized: Map<string, number[]>;
  /** Node count, so callers can sanity-check an index. */
  nodeCount: number;
};

/** How a label was resolved to a node index. */
export type Resolution = {
  /** The node the handler would have meant, or null if the name isn't in this file. */
  index: number | null;
  /** Every node index sharing that name, in document order. */
  all: number[];
  /** Exact-key match, normalised match, or no match. */
  via: "key" | "normalized" | "none";
  /**
   * More than one node carries this name. The handler's diff can only speak
   * about the first (its node map keeps the first of a duplicate name), so a
   * change on this name is ambiguous — worth telling the reviewer.
   */
  ambiguous: boolean;
};

const MISS: Resolution = { index: null, all: [], via: "none", ambiguous: false };

/** Index a document's nodes by display key and by normalised key. */
export function buildNameIndex(doc: GltfDocument): NameIndex {
  const nodes = doc.nodes ?? [];
  const byKey = new Map<string, number[]>();
  const byNormalized = new Map<string, number[]>();
  for (let i = 0; i < nodes.length; i++) {
    const key = nodeKey(nodes[i], i);
    push(byKey, key, i);
    push(byNormalized, normalizeName(key), i);
  }
  return { byKey, byNormalized, nodeCount: nodes.length };
}

function push(map: Map<string, number[]>, key: string, value: number): void {
  const existing = map.get(key);
  if (existing) existing.push(value);
  else map.set(key, [value]);
}

/**
 * Resolve a diff label to a node index. Exact match wins; a normalised match is
 * the fallback for labels that were mangled between the handler and here.
 */
export function resolveNodeIndex(index: NameIndex, label: string): Resolution {
  if (label === "") return MISS;
  const exact = index.byKey.get(label);
  if (exact && exact.length > 0) {
    return { index: exact[0]!, all: exact, via: "key", ambiguous: exact.length > 1 };
  }
  const normalized = index.byNormalized.get(normalizeName(label));
  if (normalized && normalized.length > 0) {
    return { index: normalized[0]!, all: normalized, via: "normalized", ambiguous: normalized.length > 1 };
  }
  return MISS;
}

/** The banner shown when a changed name can't be pinned to one node. */
export function ambiguousNameMessage(label: string, count: number): string {
  return (
    `${count} nodes in this file are called "${label}", so a change to that name can't be pinned to one of them — ` +
    `only the first is highlighted.`
  );
}
