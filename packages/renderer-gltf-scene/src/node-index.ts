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

/**
 * One drawable: the node that places it in the world, and which of that node's
 * primitives it is. GLTFLoader emits one `Mesh` per primitive, in primitive
 * order, so the ordinal indexes straight into `meshesIn(nodeObject)`.
 */
export type PrimitiveRef = { node: number; primitive: number };

export type NameIndex = {
  /** Display key → node indices, document order. */
  byKey: Map<string, number[]>;
  /** Normalised key → node indices, document order. */
  byNormalized: Map<string, number[]>;
  /** Node count, so callers can sanity-check an index. */
  nodeCount: number;
  /**
   * Mesh key → the nodes instancing it. One-to-many in the direction that
   * matters: four wheel nodes can share one `WheelMesh`, so a single mesh edit
   * has four places on screen and painting one of them would be a lie.
   */
  meshToNodes: Map<string, number[]>;
  meshToNodesNormalized: Map<string, number[]>;
  /**
   * Material key → the primitives that reference it. Materials reach geometry
   * only through primitives, and a material on one primitive of a multi-primitive
   * mesh must paint that primitive alone — not the whole node containing it.
   */
  materialToPrimitives: Map<string, PrimitiveRef[]>;
  materialToPrimitivesNormalized: Map<string, PrimitiveRef[]>;
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

/**
 * Disambiguate a list of names exactly the way the handler does, so a key the
 * diff emits is a key this file can look up. Duplicates take an ordinal suffix
 * (`Wheel`, `Wheel#1`, …) and the loop keeps going, so a name that literally
 * contains `#1` can't collide its way into someone else's slot.
 */
function uniqueKeys<T>(items: readonly T[], name: (item: T, index: number) => string): string[] {
  const taken = new Set<string>();
  return items.map((item, i) => {
    const base = name(item, i);
    let key = base;
    for (let dup = 1; taken.has(key); dup++) key = `${base}#${dup}`;
    taken.add(key);
    return key;
  });
}

const fallbackName = (kind: string) => (item: { name?: string } | undefined, i: number) =>
  item?.name && item.name !== "" ? item.name : `${kind}[${i}]`;

/** Index a document's nodes, meshes and materials by display and normalised key. */
export function buildNameIndex(doc: GltfDocument): NameIndex {
  const nodes = doc.nodes ?? [];
  const byKey = new Map<string, number[]>();
  const byNormalized = new Map<string, number[]>();
  for (let i = 0; i < nodes.length; i++) {
    const key = nodeKey(nodes[i], i);
    push(byKey, key, i);
    push(byNormalized, normalizeName(key), i);
  }

  const meshes = doc.meshes ?? [];
  const materials = doc.materials ?? [];
  const meshKeys = uniqueKeys(meshes, fallbackName("mesh"));
  const materialKeys = uniqueKeys(materials, fallbackName("material"));

  const meshToNodes = new Map<string, number[]>();
  const meshToNodesNormalized = new Map<string, number[]>();
  const materialToPrimitives = new Map<string, PrimitiveRef[]>();
  const materialToPrimitivesNormalized = new Map<string, PrimitiveRef[]>();

  // Walk nodes, not meshes: a mesh nobody instances has no place on screen, and
  // the whole point of these maps is "where would I paint this".
  for (let node = 0; node < nodes.length; node++) {
    const meshIndex = nodes[node]?.mesh;
    if (meshIndex === undefined) continue;
    const meshKey = meshKeys[meshIndex];
    if (meshKey === undefined) continue; // dangling mesh reference — degrade, don't throw
    push(meshToNodes, meshKey, node);
    push(meshToNodesNormalized, normalizeName(meshKey), node);

    const primitives = meshes[meshIndex]?.primitives ?? [];
    for (let primitive = 0; primitive < primitives.length; primitive++) {
      const materialIndex = primitives[primitive]?.material;
      if (materialIndex === undefined) continue; // glTF's default material has no key
      const materialKey = materialKeys[materialIndex];
      if (materialKey === undefined) continue;
      pushRef(materialToPrimitives, materialKey, { node, primitive });
      pushRef(materialToPrimitivesNormalized, normalizeName(materialKey), { node, primitive });
    }
  }

  return {
    byKey,
    byNormalized,
    nodeCount: nodes.length,
    meshToNodes,
    meshToNodesNormalized,
    materialToPrimitives,
    materialToPrimitivesNormalized,
  };
}

function pushRef(map: Map<string, PrimitiveRef[]>, key: string, value: PrimitiveRef): void {
  const existing = map.get(key);
  if (existing) existing.push(value);
  else map.set(key, [value]);
}

/** Nodes instancing the mesh a diff label names; empty when nothing does. */
export function resolveMeshNodes(index: NameIndex, label: string): number[] {
  if (label === "") return [];
  return index.meshToNodes.get(label) ?? index.meshToNodesNormalized.get(normalizeName(label)) ?? [];
}

/** Primitives referencing the material a diff label names; empty when none do. */
export function resolveMaterialPrimitives(index: NameIndex, label: string): PrimitiveRef[] {
  if (label === "") return [];
  return (
    index.materialToPrimitives.get(label) ??
    index.materialToPrimitivesNormalized.get(normalizeName(label)) ??
    []
  );
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

/**
 * The same, for the name a rename left behind (#47). A rename records the bare
 * old name and nothing more — the previous revision's array index would mean
 * whatever sits at that number *now* — so when that name was shared, which side
 * of the pair moved cannot be recovered from the diff. Said out loud, because the
 * ghost drawn from the wrong twin is otherwise a confident picture of a move that
 * did not happen.
 */
export function ambiguousPreviousNameMessage(label: string, count: number): string {
  return (
    `${count} nodes in the previous version are called "${label}", and a rename records only the old name — ` +
    `so the ghost of where this one stood is drawn from the first of them.`
  );
}
