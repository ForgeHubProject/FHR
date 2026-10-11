// Turning a click in the viewport into a change.
//
// Two steps, both pure, so both are tested without a browser:
//
//   pointer → normalised device coordinates   (ndcFromPointer)
//   ray hits → the change that owns them      (changeAtHits, by change path)
//
// The second step is the interesting one. A raycast hits a *Mesh*, which is
// rarely the thing the diff talks about: GLTFLoader builds a Group per glTF node
// when a mesh has several primitives, and the overlay adds cloned ghosts that the
// loader never saw at all. So resolution walks up the ancestor chain, asking two
// questions in order:
//
//   1. is this object one the overlay painted for a change?  (the ghosts, and the
//      node subtrees it tinted — an exact answer, and the only one that works for
//      clones, which appear in no glTF association)
//   2. is this object associated with a glTF node?           (associations.ts, the
//      loader's own record; the fallback for geometry the overlay didn't paint)
//
// A click that resolves to neither is a click on unchanged geometry or on empty
// space, and the honest answer to that is "nothing is selected".

import type { Object3D } from "three";

/** The rectangle a pointer event is measured against (a DOMRect, or a stub). */
export type Rect = { left: number; top: number; width: number; height: number };

/** A pointer position, in client coordinates. */
export type Pointer = { clientX: number; clientY: number };

/** Normalised device coordinates: x,y ∈ [-1, 1], y up — what a Raycaster wants. */
export function ndcFromPointer(pointer: Pointer, rect: Rect): { x: number; y: number } {
  if (rect.width <= 0 || rect.height <= 0) return { x: 0, y: 0 };
  const x = ((pointer.clientX - rect.left) / rect.width) * 2 - 1;
  const y = -(((pointer.clientY - rect.top) / rect.height) * 2 - 1);
  // `+ 0` turns the -0 that flipping the y axis produces back into 0: harmless
  // for the maths, but it makes a dead-centre click compare equal to {x:0,y:0}.
  return { x: x + 0, y: y + 0 };
}

/** Just enough of an intersection for resolution (`Raycaster` returns more). */
export type Hit = { object: Object3D };

export type ChangeLookup = {
  /** Objects the overlay painted for a change → that change's path. */
  changePathByObject: Map<Object3D, string>;
  /** The glTF node an object belongs to, walking up (see associations.ts). */
  nodeIndexOf?: (object: Object3D) => number | null;
  /** Node index → change path, for changed nodes the overlay tinted in place. */
  changePathByNodeIndex?: Map<number, string>;
};

/**
 * The change a click landed on: the first *visible* hit whose ancestor chain
 * reaches a painted change, or null. Hits arrive nearest-first (three.js sorts
 * them), so "the first one that resolves" is "the frontmost change under the
 * cursor".
 *
 * The visibility filter is not optional: three.js's raycaster does not check
 * `visible` (Raycaster.js only tests layers), so while a change is isolated every
 * hidden mesh is still in the ray's path — and a click that selected something the
 * reviewer cannot see would be indistinguishable from a bug.
 */
export function changeAtHits(hits: readonly Hit[], lookup: ChangeLookup): string | null {
  for (const hit of hits) {
    if (!isVisibleInTree(hit.object)) continue;
    const path = changeAtObject(hit.object, lookup);
    if (path !== null) return path;
  }
  return null;
}

/** True when an object and every ancestor is visible — i.e. it is on screen. */
export function isVisibleInTree(object: Object3D | null): boolean {
  for (let current: Object3D | null = object; current; current = current.parent) {
    if ((current as { visible?: boolean }).visible === false) return false;
  }
  return true;
}

/** The change path that owns `object` or one of its ancestors, or null. */
export function changeAtObject(object: Object3D | null, lookup: ChangeLookup): string | null {
  for (let current: Object3D | null = object; current; current = current.parent) {
    const painted = lookup.changePathByObject.get(current);
    if (painted !== undefined) return painted;
  }
  // Nothing painted in this chain. The loader's associations are the fallback:
  // they survive the name mangling that the diff's labels don't (node-index.ts).
  if (lookup.nodeIndexOf && lookup.changePathByNodeIndex) {
    const index = lookup.nodeIndexOf(object as Object3D);
    if (index !== null) {
      const path = lookup.changePathByNodeIndex.get(index);
      if (path !== undefined) return path;
    }
  }
  return null;
}

/**
 * Whether a pointer sequence should count as a click rather than the end of an
 * orbit drag. OrbitControls owns dragging, and a reviewer who rotates the model
 * has not asked to change the selection.
 */
export function isClickGesture(
  down: { x: number; y: number; t: number },
  up: { x: number; y: number; t: number },
  limits: { slopPx?: number; maxMs?: number } = {},
): boolean {
  const slop = limits.slopPx ?? 4;
  const maxMs = limits.maxMs ?? 700;
  if (up.t - down.t > maxMs) return false;
  return Math.abs(up.x - down.x) <= slop && Math.abs(up.y - down.y) <= slop;
}
