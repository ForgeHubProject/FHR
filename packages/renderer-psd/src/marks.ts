// Where each change is on the before and after images: every layer row of the
// diff (`layers/<id>`) outlined at its layer's box on the side it is on.

import type { DiffChange, StructuredDiff } from "@fhr/types";
import type { ImageMark, ImageMarkKind } from "@fhr/renderer-sdk/image";
import type { Box, Layout } from "./layout.js";

const LAYER = /^layers\/(\d+)$/;

/** The outline key of a change path: its layer's row (`layers/10/colour` → `layers/10`). */
export function markKey(path: string): string | null {
  const m = /^layers\/\d+/.exec(path);
  return m ? m[0] : null;
}

/** An outline for every changed, added or removed layer that has a box. */
export function marksOf(diff: StructuredDiff, base: Layout | null, head: Layout | null): ImageMark[] {
  const marks: ImageMark[] = [];
  // Clipped to the canvas: a layer hanging off its edge is outlined where it shows.
  const box = (layout: Layout | null, id: string): Box | null => {
    const b = layout?.layers[id];
    if (!layout || !b) return null;
    const x0 = Math.max(0, b[0]);
    const y0 = Math.max(0, b[1]);
    const x1 = Math.min(layout.w, b[0] + b[2]);
    const y1 = Math.min(layout.h, b[1] + b[3]);
    return x1 > x0 && y1 > y0 ? [x0, y0, x1 - x0, y1 - y0] : null;
  };
  const visit = (n: DiffChange): void => {
    const m = LAYER.exec(n.path);
    if (!m) {
      for (const c of n.children ?? []) visit(c);
      return;
    }
    const id = m[1]!;
    const kind: ImageMarkKind = n.kind === "added" ? "added" : n.kind === "removed" ? "removed" : "modified";
    const sides: ("base" | "head")[] = kind === "added" ? ["head"] : kind === "removed" ? ["base"] : ["base", "head"];
    for (const side of sides) {
      const b = box(side === "base" ? base : head, id);
      if (b) marks.push({ key: n.path, side, x: b[0], y: b[1], w: b[2], h: b[3], kind, label: n.label });
    }
  };
  for (const c of diff.changes ?? []) visit(c);
  return marks;
}
