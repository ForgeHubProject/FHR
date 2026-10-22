// Pixel comparison for the "Show differences" toggle: which pixels of two
// RGBA rasters differ, how many, and where.
//
// Two images of different sizes compare top-left aligned over the area that
// covers both; outside one image its pixels count as transparent. A pixel that
// is fully transparent on both sides is the same pixel whatever colour channels
// sit under its zero alpha — canvases and encoders disagree about those.

import type { Rect } from "./viewport.js";

/** An RGBA raster, 4 bytes a pixel, rows top to bottom. */
export type Pixels = { width: number; height: number; data: ArrayLike<number> };

export type PixelDiff = {
  /** The compared area: the larger width by the larger height. */
  width: number;
  height: number;
  /** One byte per pixel of the compared area, 1 where it differs. */
  mask: Uint8Array;
  /** How many pixels differ. */
  count: number;
  /** The smallest rectangle around every differing pixel; null when none do. */
  bounds: Rect | null;
};

/** The tint changed pixels get in the overlay: a magenta that reads on light and dark. */
export const DIFF_TINT: readonly [number, number, number, number] = [255, 0, 170, 170];

export function diffPixels(a: Pixels | null, b: Pixels | null): PixelDiff {
  const aw = a?.width ?? 0;
  const ah = a?.height ?? 0;
  const bw = b?.width ?? 0;
  const bh = b?.height ?? 0;
  const width = Math.max(aw, bw);
  const height = Math.max(ah, bh);
  const mask = new Uint8Array(width * height);
  const ad = a?.data;
  const bd = b?.data;
  let count = 0;
  let x0 = width;
  let y0 = height;
  let x1 = -1;
  let y1 = -1;
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      const ia = ad && x < aw && y < ah ? (y * aw + x) * 4 : -1;
      const ib = bd && x < bw && y < bh ? (y * bw + x) * 4 : -1;
      const alphaA = ia >= 0 ? ad![ia + 3]! : 0;
      const alphaB = ib >= 0 ? bd![ib + 3]! : 0;
      if (alphaA === 0 && alphaB === 0) continue;
      if (
        alphaA === alphaB &&
        ad![ia]! === bd![ib]! &&
        ad![ia + 1]! === bd![ib + 1]! &&
        ad![ia + 2]! === bd![ib + 2]!
      ) continue;
      mask[y * width + x] = 1;
      count++;
      if (x < x0) x0 = x;
      if (x > x1) x1 = x;
      if (y < y0) y0 = y;
      if (y > y1) y1 = y;
    }
  }
  return { width, height, mask, count, bounds: count ? { x: x0, y: y0, w: x1 - x0 + 1, h: y1 - y0 + 1 } : null };
}

/** Whether two rasters hold the same picture, stopping at the first difference. */
export function samePixels(a: Pixels, b: Pixels): boolean {
  if (a.width !== b.width || a.height !== b.height) return diffPixels(a, b).count === 0;
  const ad = a.data;
  const bd = b.data;
  for (let i = 0, n = a.width * a.height * 4; i < n; i += 4) {
    const alpha = ad[i + 3]!;
    if (alpha !== bd[i + 3]!) return false;
    if (alpha === 0) continue;
    if (ad[i]! !== bd[i]! || ad[i + 1]! !== bd[i + 1]! || ad[i + 2]! !== bd[i + 2]!) return false;
  }
  return true;
}

/** The overlay raster for a diff: the tint where pixels differ, clear elsewhere. */
export function overlayPixels(d: PixelDiff, out: Uint8ClampedArray = new Uint8ClampedArray(d.width * d.height * 4)): Uint8ClampedArray {
  const [r, g, b, alpha] = DIFF_TINT;
  for (let i = 0; i < d.mask.length; i++) {
    if (!d.mask[i]) continue;
    const o = i * 4;
    out[o] = r;
    out[o + 1] = g;
    out[o + 2] = b;
    out[o + 3] = alpha;
  }
  return out;
}

/** "1,204 pixels differ (0.42%)" — the share of the compared area. */
export function describeDiff(d: PixelDiff): string {
  if (d.count === 0) return "No pixel differs";
  const share = (d.count / (d.width * d.height)) * 100;
  const pct = share >= 10 ? share.toFixed(0) : share >= 0.01 ? share.toFixed(2).replace(/\.?0+$/, "") : "<0.01";
  return `${d.count.toLocaleString("en-US")} ${d.count === 1 ? "pixel differs" : "pixels differ"} (${pct}%)`;
}
