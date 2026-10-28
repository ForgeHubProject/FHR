// The diff, sorted by screen: which part of the change tree belongs to which
// screen, and where each change is on the screen's before and after images.
//
// The handler groups its tree page → screen → layer, with stable paths:
// `screens/<id>` for a screen's group, `layers/<id>` for a layer's changes (or
// a layer added or removed), `layout/<id>` for what auto layout moved. The
// preview sheets say which screen each layer id is on, and where.

import type { DiffChange, StructuredDiff } from "@fhr/types";
import type { ImageMark, ImageMarkKind } from "@fhr/renderer-sdk/image";
import type { Box, Sheet, SheetScreen } from "./sheet.js";

export type ScreenView = {
  id: number;
  name: string;
  /** Added or removed: the screen is on one side only. */
  status: "modified" | "added" | "removed";
  /** How many layers changed on it; layout moves aside. */
  count: number;
  /** Its part of the change tree. */
  changes: DiffChange[];
  /** Every outline on it, layout moves included. */
  marks: ImageMark[];
  base: SheetScreen | null;
  head: SheetScreen | null;
};

const SCREEN = /^screens\/(\d+)$/;
const LAYER = /^(layers|layout)\/(\d+)$/;

/** The outline key of a change path: its layer's row (`layers/18/text` → `layers/18`). */
export function markKey(path: string): string | null {
  const m = /^(layers|layout)\/\d+/.exec(path);
  return m ? m[0] : null;
}

/** The changed screens in document order (after first, then those only before), and the changes on no screen. */
export function screenViews(diff: StructuredDiff, base: Sheet | null, head: Sheet | null): { screens: ScreenView[]; other: DiffChange[] } {
  const index = (sheet: Sheet | null): Map<string, SheetScreen> => {
    const m = new Map<string, SheetScreen>();
    for (const s of sheet?.screens ?? []) for (const id of Object.keys(s.layers)) m.set(id, s);
    return m;
  };
  const inBase = index(base);
  const inHead = index(head);
  const screenOf = (id: string): number | null => (inHead.get(id) ?? inBase.get(id))?.id ?? null;
  const byId = (sheet: Sheet | null, id: number): SheetScreen | null => sheet?.screens.find((s) => s.id === id) ?? null;

  const assigned = new Map<number, DiffChange[]>();
  const other: DiffChange[] = [];
  const assign = (sid: number, nodes: DiffChange[]): void => {
    const list = assigned.get(sid) ?? [];
    list.push(...nodes);
    assigned.set(sid, list);
  };
  const onScreens = (n: DiffChange): boolean =>
    SCREEN.test(n.path) || LAYER.test(n.path) || (n.children ?? []).some(onScreens);
  const walk = (nodes: DiffChange[]): void => {
    for (const n of nodes) {
      const screen = SCREEN.exec(n.path);
      if (screen) {
        // A screen neither preview drew has no images to show its changes on.
        const sid = Number(screen[1]);
        if (byId(head, sid) || byId(base, sid)) assign(sid, n.children ?? []);
        else other.push(n);
        continue;
      }
      const layer = LAYER.exec(n.path);
      const sid = layer ? screenOf(layer[2]!) : null;
      if (sid !== null) assign(sid, [n]);
      else if (!layer && (n.children ?? []).some(onScreens)) walk(n.children ?? []);
      else other.push(n);
    }
  };
  walk(diff.changes ?? []);

  const order = [...(head?.screens ?? []), ...(base?.screens ?? [])].map((s) => s.id);
  const seen = new Set<number>();
  const screens: ScreenView[] = [];
  for (const id of [...order, ...assigned.keys()]) {
    if (seen.has(id) || !assigned.has(id)) continue;
    seen.add(id);
    const b = byId(base, id);
    const h = byId(head, id);
    const changes = assigned.get(id)!;
    const { marks, count } = outline(changes, id, b, h);
    screens.push({
      id,
      name: (h ?? b)?.name ?? `Screen ${id}`,
      status: b && h ? "modified" : h ? "added" : "removed",
      count,
      changes,
      marks,
      base: b,
      head: h,
    });
  }
  return { screens, other };
}

/** A screen's outlines: every layer row in its changes, on each side it is on. */
function outline(changes: DiffChange[], sid: number, base: SheetScreen | null, head: SheetScreen | null): { marks: ImageMark[]; count: number } {
  const marks: ImageMark[] = [];
  let count = 0;
  // Clipped to the screen: a layer hanging off its edge (the frame clips it)
  // is outlined where it shows.
  const box = (screen: SheetScreen | null, id: string): Box | null => {
    const b = screen?.layers[id];
    if (!screen || !b) return null;
    const x0 = Math.max(0, b[0]);
    const y0 = Math.max(0, b[1]);
    const x1 = Math.min(screen.w, b[0] + b[2]);
    const y1 = Math.min(screen.h, b[1] + b[3]);
    return x1 > x0 && y1 > y0 ? [x0, y0, x1 - x0, y1 - y0] : null;
  };
  const visit = (n: DiffChange): void => {
    const m = LAYER.exec(n.path);
    if (!m) {
      for (const c of n.children ?? []) visit(c);
      return;
    }
    const id = m[2]!;
    const layout = m[1] === "layout";
    if (!layout) count++;
    // The whole screen added or removed: the image itself is the change.
    if (Number(id) === sid && (n.kind === "added" || n.kind === "removed")) return;
    const kind: ImageMarkKind = layout ? "layout" : n.kind === "added" ? "added" : n.kind === "removed" ? "removed" : "modified";
    const sides: ("base" | "head")[] = kind === "added" ? ["head"] : kind === "removed" ? ["base"] : ["base", "head"];
    for (const side of sides) {
      const b = box(side === "base" ? base : head, id);
      if (b) marks.push({ key: n.path, side, x: b[0], y: b[1], w: b[2], h: b[3], kind, label: n.label });
    }
  };
  for (const c of changes) visit(c);
  return { marks, count };
}
