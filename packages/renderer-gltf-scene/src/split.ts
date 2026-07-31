// Side-by-side geometry: how the one canvas is cut in two, and which way.
//
// ONE WebGLRenderer, TWO scissored viewports. Never two canvases: browsers cap
// live WebGL contexts (~16), which is why teardown already calls
// forceContextLoss(), and a review page showing several diffs would start losing
// the older ones. Scissoring costs one extra draw call pair and no context.
//
// Pure: no three.js, no DOM. scene-3d.ts turns these rects into
// setViewport/setScissor calls and CSS label positions.

/** "columns" = the two panes sit beside each other; "rows" = one above the other. */
export type SplitOrientation = "columns" | "rows";

/** Which version a pane draws. Reading order is previous → current. */
export type PaneSide = "base" | "head";

export type Rect = { x: number; y: number; width: number; height: number };

export type Pane = {
  side: PaneSide;
  /** Drawing rect in WebGL coordinates — origin bottom-left. */
  gl: Rect;
  /** The same rect in CSS coordinates — origin top-left, for the pane's label. */
  css: Rect;
  /** The camera aspect this pane must be drawn with (see camera-sync.ts). */
  aspect: number;
};

/** Hairline gutter between the panes, in CSS pixels. */
export const SPLIT_GAP = 2;

/**
 * Which way to cut, from the model's own proportions.
 *
 * A wide model wants wide panes, so it stacks; a tall model wants tall panes, so
 * the panes sit beside each other. The horizontal extent is the larger of the two
 * ground-plane axes because a model can be long in either — glTF is Y-up, so X
 * and Z are both "wide".
 *
 * This is only the *initial* choice; the toggle overrides it. The default has to
 * be usually right, not always right, so it does not consult the container: a
 * host that is itself very wide or very narrow is the reviewer's to correct.
 */
export function defaultSplit(size: { x: number; y: number; z: number }): SplitOrientation {
  const horizontal = Math.max(size.x, size.z);
  const vertical = size.y;
  // Degenerate (an empty box, a flat plan view): "columns" is the mode's own
  // name and the arrangement a reviewer expects when nothing suggests otherwise.
  if (!(horizontal > 0) || !(vertical > 0)) return "columns";
  return horizontal >= vertical ? "rows" : "columns";
}

/**
 * A Box3's extent as three non-negative lengths. An *empty* Box3 carries
 * ±Infinity bounds, which would otherwise reach `defaultSplit` as a NaN, so the
 * clamp here is what keeps the degenerate case degenerate rather than wrong.
 * Structural typing keeps three.js out of this module.
 */
export function boxSize(box: {
  min: { x: number; y: number; z: number };
  max: { x: number; y: number; z: number };
}): { x: number; y: number; z: number } {
  const span = (min: number, max: number): number => (Number.isFinite(max - min) ? Math.max(0, max - min) : 0);
  return {
    x: span(box.min.x, box.max.x),
    y: span(box.min.y, box.max.y),
    z: span(box.min.z, box.max.z),
  };
}

export function otherSplit(orientation: SplitOrientation): SplitOrientation {
  return orientation === "columns" ? "rows" : "columns";
}

export const SPLIT_LABEL: Record<SplitOrientation, string> = {
  columns: "Split left / right",
  rows: "Split top / bottom",
};

/**
 * The two panes for a canvas of `size`, previous version first (left, or top).
 *
 * Sizes are floored and the second pane takes the remainder, so the two rects
 * plus the gutter always add back up to the canvas exactly — a rounding leak
 * shows as an unpainted seam column that looks like a rendering bug.
 */
export function splitPanes(
  orientation: SplitOrientation,
  size: { width: number; height: number },
  gap: number = SPLIT_GAP,
): Pane[] {
  const width = Math.max(0, Math.floor(size.width));
  const height = Math.max(0, Math.floor(size.height));
  const cut = Math.min(gap, orientation === "columns" ? width : height);

  if (orientation === "columns") {
    const first = Math.floor((width - cut) / 2);
    const second = Math.max(0, width - cut - first);
    return [
      pane("base", { x: 0, y: 0, width: first, height }, 0),
      pane("head", { x: first + cut, y: 0, width: second, height }, 0),
    ];
  }

  const first = Math.floor((height - cut) / 2);
  const second = Math.max(0, height - cut - first);
  // CSS runs top-down and GL runs bottom-up, so the *first* pane (the previous
  // version, on top) is the one with the higher GL y.
  return [
    pane("base", { x: 0, y: second + cut, width, height: first }, 0),
    pane("head", { x: 0, y: 0, width, height: second }, first + cut),
  ];
}

function pane(side: PaneSide, gl: Rect, cssTop: number): Pane {
  return {
    side,
    gl,
    css: { x: gl.x, y: cssTop, width: gl.width, height: gl.height },
    aspect: gl.height > 0 ? gl.width / gl.height : 1,
  };
}
