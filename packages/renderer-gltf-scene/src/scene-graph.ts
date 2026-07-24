// Pure scene-graph derivation for the gltf-scene outline view: entities + a
// per-node change map → renderable node descriptors. No three.js and no DOM
// here, so the mapping (transform defaults, change colouring) is unit-testable
// on its own.
//
// This is the *fallback* view since the real-model renderer landed (#44): a unit
// box per node, shown when the real model can't be decoded (Draco/KTX2, a
// sibling .bin, unreadable bytes) — always with a banner saying so.

import type { ChangeKind } from "@fhr/types";
import type { Entity } from "./gltf-parse.js";
import { normalizeName } from "./node-index.js";
import { KIND_COLOR, NEUTRAL } from "./palette.js";

// Change colours come from the shared palette, so this view, the real-model view
// and the lite change tree all name a change with the same colour.
export { KIND_COLOR, NEUTRAL };

export type SceneNode = {
  name: string;
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
  return entities.map((e) => {
    const kind = kindFor(e.name, changeMap) ?? "unchanged";
    const color = kind === "unchanged" ? NEUTRAL : KIND_COLOR[kind] ?? NEUTRAL;
    return {
      name: e.name,
      position: e.transform?.position ?? [0, 0, 0],
      rotationEulerDeg: e.transform?.rotationEulerDeg ?? [0, 0, 0],
      scale: e.transform?.scale ?? [1, 1, 1],
      color,
      kind,
    };
  });
}
