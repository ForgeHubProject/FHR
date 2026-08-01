// Pure scene-graph derivation for the gltf-scene renderer: entities + a per-node
// change map → node descriptors annotated with what changed. No three.js and no
// DOM here, so the mapping (transform defaults, change colouring, nesting) is
// unit-testable on its own.
//
// Two views read this, which is why the annotation lives here rather than in
// either of them:
//
//   * the structure tree (#56) — the left region of the 3D chrome, the whole
//     model including the parts that did *not* change, so a reviewer can reach
//     one for context. This is the reason `depth` exists.
//   * the box-scene fallback (#44) — a unit box per node, shown when the real
//     model can't be decoded (Draco/KTX2, a sibling .bin, unreadable bytes),
//     always with a banner saying so. It ignores the nesting and draws by
//     transform.

import type { ChangeKind } from "@fhr/types";
import type { Entity } from "./gltf-parse.js";
import { normalizeName } from "./node-index.js";
import { KIND_COLOR, NEUTRAL } from "./palette.js";

// Change colours come from the shared palette, so this view, the real-model view
// and the lite change tree all name a change with the same colour.
export { KIND_COLOR, NEUTRAL };

export type SceneNode = {
  /** The entity id this node was parsed under — unique within one file. */
  id: string;
  name: string;
  /** Ancestor count, so a flat list can be rendered as an indented tree. */
  depth: number;
  position: [number, number, number];
  rotationEulerDeg: [number, number, number];
  scale: [number, number, number];
  color: number;
  kind: ChangeKind | "unchanged";
};

/**
 * The change map re-keyed by normalised name, first key wins. First-wins is what
 * makes this a drop-in for the scan it replaces: that scan walked the map in
 * insertion order and returned the earliest key that normalised to the same
 * form, so two change keys that collide under normalisation still resolve to the
 * same kind they always did.
 *
 * Built once per graph, because the normalised lookup is the *common* path, not
 * the exception: only a changed node takes the exact hit, so scanning here cost
 * one pass over every change for every unchanged node — quadratic on the
 * ordinary file. Since #56 put this on every 3D mount rather than only the
 * box-scene fallback, a 20 000-node assembly with 200 changes spent ~0.75 s
 * blocking the main thread before the canvas existed.
 */
function normalizedKinds(changeMap: Map<string, ChangeKind>): Map<string, ChangeKind> {
  const acc = new Map<string, ChangeKind>();
  for (const [key, kind] of changeMap) {
    const norm = normalizeName(key);
    if (!acc.has(norm)) acc.set(norm, kind);
  }
  return acc;
}

/**
 * Look a node's name up in the change map: exact match first, then the shared
 * normalisation (see node-index.ts). `slugify` is retired — matching one
 * mangling of a name against a differently-mangled one is how "Cube.001" lost
 * its highlight.
 */
function kindFor(
  name: string,
  changeMap: Map<string, ChangeKind>,
  byNormalized: Map<string, ChangeKind>,
): ChangeKind | undefined {
  return changeMap.get(name) ?? byNormalized.get(normalizeName(name));
}

export function buildSceneGraph(entities: Entity[], changeMap: Map<string, ChangeKind>): SceneNode[] {
  const byNormalized = normalizedKinds(changeMap);
  // parseGltf emits parents before children, so one forward pass can resolve a
  // depth from the depth already recorded for the parent.
  const depthById = new Map<string, number>();
  return entities.map((e) => {
    const depth = e.parentEntityId === null ? 0 : (depthById.get(e.parentEntityId) ?? -1) + 1;
    depthById.set(e.entityId, depth);
    const kind = kindFor(e.name, changeMap, byNormalized) ?? "unchanged";
    const color = kind === "unchanged" ? NEUTRAL : KIND_COLOR[kind] ?? NEUTRAL;
    return {
      id: e.entityId,
      name: e.name,
      depth,
      position: e.transform?.position ?? [0, 0, 0],
      rotationEulerDeg: e.transform?.rotationEulerDeg ?? [0, 0, 0],
      scale: e.transform?.scale ?? [1, 1, 1],
      color,
      kind,
    };
  });
}
