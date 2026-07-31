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
 * Look a node's name up in the change map: exact match first, then the shared
 * normalisation (see node-index.ts). `slugify` is retired — matching one
 * mangling of a name against a differently-mangled one is how "Cube.001" lost
 * its highlight.
 */
function kindFor(name: string, changeMap: Map<string, ChangeKind>): ChangeKind | undefined {
  const exact = changeMap.get(name);
  if (exact) return exact;
  const wanted = normalizeName(name);
  for (const [key, kind] of changeMap) {
    if (normalizeName(key) === wanted) return kind;
  }
  return undefined;
}

export function buildSceneGraph(entities: Entity[], changeMap: Map<string, ChangeKind>): SceneNode[] {
  // parseGltf emits parents before children, so one forward pass can resolve a
  // depth from the depth already recorded for the parent.
  const depthById = new Map<string, number>();
  return entities.map((e) => {
    const depth = e.parentEntityId === null ? 0 : (depthById.get(e.parentEntityId) ?? -1) + 1;
    depthById.set(e.entityId, depth);
    const kind = kindFor(e.name, changeMap) ?? "unchanged";
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
