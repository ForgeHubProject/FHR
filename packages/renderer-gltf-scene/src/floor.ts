// Where the viewport's floor grid goes, as pure math on three's Box3 — no
// renderer, so it is testable without WebGL (see floor.test.ts).
//
// The grid used to be a fixed 40x40 at y=0. A model centred on the origin was
// cut through its waist, a millimetre-scale one dwarfed it, and a tiny one fit
// inside one cell (FHR#86). The floor now stands under the content and is sized
// from it. The model never moves: the change list's positions are in file
// coordinates, so shifting the model onto y=0 would make the numbers disagree
// with the picture.

import type { Box3 } from "three";

/** The floor to draw: its height, centre on the ground plane, and grid shape. */
export type Floor = {
  /** World y of the grid: the bottom of the content, so the model stands on it. */
  y: number;
  /** World x/z the grid is centred on: under the content, not the origin. */
  centerX: number;
  centerZ: number;
  /** Side length of the (square) grid. */
  size: number;
  /** Cells along one side. */
  divisions: number;
};

/** What the grid was before it was fitted; used when there is nothing to fit to. */
export const DEFAULT_FLOOR: Floor = { y: 0, centerX: 0, centerZ: 0, size: 40, divisions: 40 };

/** Aim for about this many cells across the content's widest extent. */
const CELLS_ACROSS_CONTENT = 10;
/** The grid reaches this far past the content, so the model sits inside it. */
const OVERHANG = 1.5;
/** Never draw a grid denser than this: a runaway extent must not stall a frame. */
const MAX_DIVISIONS = 200;

/** The nearest 1, 2 or 5 times a power of ten at or above `x` (x > 0). */
export function niceStep(x: number): number {
  const exp = Math.floor(Math.log10(x));
  const base = 10 ** exp;
  const m = x / base;
  const nice = m <= 1 ? 1 : m <= 2 ? 2 : m <= 5 ? 5 : 10;
  return nice * base;
}

/**
 * The floor under `box`. An empty or degenerate box (no extent in x and z) has
 * nothing to fit, so it keeps the original grid at the box's bottom if it has one.
 */
export function fitFloor(box: Box3): Floor {
  if (box.isEmpty()) return DEFAULT_FLOOR;
  const width = box.max.x - box.min.x;
  const depth = box.max.z - box.min.z;
  const extent = Math.max(width, depth, box.max.y - box.min.y);
  const y = box.min.y;
  if (!(extent > 1e-9) || !Number.isFinite(extent)) {
    return { ...DEFAULT_FLOOR, y: Number.isFinite(y) ? y : 0 };
  }

  const cell = niceStep(extent / CELLS_ACROSS_CONTENT);
  // Whole cells, even, so the grid's centre line falls on a cell edge.
  let divisions = Math.ceil((Math.max(width, depth) * OVERHANG) / cell);
  divisions = Math.min(MAX_DIVISIONS, Math.max(2, divisions + (divisions % 2)));
  return {
    y,
    centerX: (box.min.x + box.max.x) / 2,
    centerZ: (box.min.z + box.max.z) / 2,
    size: divisions * cell,
    divisions,
  };
}
