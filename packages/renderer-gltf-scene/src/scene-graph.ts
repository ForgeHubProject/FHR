// Pure scene-graph derivation for the gltf-scene 3D view: entities + a per-node
// change map → renderable node descriptors. No three.js and no DOM here, so the
// mapping (transform defaults, change colouring) is unit-testable on its own.

import type { ChangeKind } from "@fhr/types";
import type { Entity } from "./gltf-parse.js";

function slugify(name: string): string {
  return name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "") || "node";
}

// Change colours mirror the change-tree view (added/removed/modified) so the 3D
// scene and the lite tree read consistently; unchanged nodes are neutral.
export const KIND_COLOR: Record<string, number> = {
  added: 0x2ea043,
  removed: 0xcf222e,
  modified: 0xd29922,
};
export const NEUTRAL = 0x8b98a5;

export type SceneNode = {
  name: string;
  position: [number, number, number];
  rotationEulerDeg: [number, number, number];
  scale: [number, number, number];
  color: number;
  kind: ChangeKind | "unchanged";
};

export function buildSceneGraph(entities: Entity[], changeMap: Map<string, ChangeKind>): SceneNode[] {
  return entities.map((e) => {
    const kind = changeMap.get(slugify(e.name)) ?? "unchanged";
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
