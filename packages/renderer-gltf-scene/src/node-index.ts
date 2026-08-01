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

import type { EntityChange } from "./diff-map.js";
import { sceneRootName, type GltfDocument, type GltfNode } from "./gltf-parse.js";

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
  /**
   * Node index → its display key: `byKey` read the other way. The mesh and
   * material maps below resolve to *indices*, and everything that annotates a
   * row or highlights one speaks node names, so the trip back has to exist
   * somewhere; here it is one array built in the pass that already computes it.
   */
  keyByIndex: string[];
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
  /**
   * The name the outline's synthetic root row carries, or null when the file
   * gets no such row (gltf-parse.ts `sceneRootName`). It is deliberately NOT in
   * `byKey`: it names a glTF scene, which has no node index, and putting it
   * there would let a diff label resolve to a node that does not exist.
   */
  sceneRootName: string | null;
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
  const keyByIndex: string[] = new Array<string>(nodes.length);
  for (let i = 0; i < nodes.length; i++) {
    const key = nodeKey(nodes[i], i);
    keyByIndex[i] = key;
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
    keyByIndex,
    nodeCount: nodes.length,
    meshToNodes,
    meshToNodesNormalized,
    materialToPrimitives,
    materialToPrimitivesNormalized,
    sceneRootName: sceneRootName(doc),
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

/**
 * Where a mesh or material change lands on the *node* list, both ways round.
 *
 * A mesh or a material has no place of its own on screen: it is drawn by the
 * nodes instancing it and by the primitives referencing it (#51 paints exactly
 * that geometry). So a change against one is a change to a set of nodes, and
 * every surface keyed on node names — the structure tree's rows and their dots,
 * the highlight the queue moves — needs this translation or it silently reports
 * the geometry as untouched while the picture beside it is painted.
 */
export type IndirectPaint = {
  /**
   * Node display key → the change that reaches it. First in diff order wins, so
   * a node carrying both a changed mesh and a changed material is described by
   * the one the reviewer meets first in the list.
   */
  byNode: Map<string, EntityChange>;
  /**
   * Change key → the node display keys it reaches, in document order. Empty for
   * a change nothing in this file draws (an unreferenced mesh) — those have no
   * row to point at, which `model-overlay` already counts as unpaintable.
   */
  byChange: Map<string, string[]>;
};

/**
 * Resolve mesh and material changes onto this file's nodes.
 *
 * Meshes before materials, matching the order `buildOverlay` paints them in, so
 * the two never disagree about which change a node is described by.
 */
export function indirectNodeChanges(
  index: NameIndex,
  meshes: readonly EntityChange[],
  materials: readonly EntityChange[],
): IndirectPaint {
  const byNode = new Map<string, EntityChange>();
  const byChange = new Map<string, string[]>();

  // `seen` does the membership test, `keys` only carries the order. One change
  // can reach every node in the file — one material named "Steel", one bolt mesh
  // instanced 20 000 times — and nothing upstream bounds that (limits.ts caps
  // blob *bytes*, which says nothing about node count). A `keys.includes` here
  // would be a linear scan per node, and this runs before the canvas exists.
  const record = (change: EntityChange, nodeIndices: Iterable<number>): void => {
    const keys: string[] = [];
    const seen = new Set<string>();
    for (const node of nodeIndices) {
      const key = index.keyByIndex[node];
      if (key === undefined || seen.has(key)) continue;
      seen.add(key);
      keys.push(key);
      if (!byNode.has(key)) byNode.set(key, change);
    }
    if (keys.length > 0) byChange.set(change.name, keys);
  };

  for (const change of meshes) record(change, resolveMeshNodes(index, change.name));
  for (const change of materials) {
    record(
      change,
      resolveMaterialPrimitives(index, change.name).map((ref) => ref.node),
    );
  }
  return { byNode, byChange };
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
