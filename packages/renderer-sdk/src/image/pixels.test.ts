import { describe, it, expect } from "vitest";
import { describeDiff, diffPixels, overlayPixels, samePixels, DIFF_TINT, type Pixels } from "./pixels.js";

/** A w × h raster filled with one RGBA colour. */
function fill(w: number, h: number, rgba: [number, number, number, number]): Pixels & { data: Uint8ClampedArray } {
  const data = new Uint8ClampedArray(w * h * 4);
  for (let i = 0; i < data.length; i += 4) data.set(rgba, i);
  return { width: w, height: h, data };
}
function set(p: Pixels & { data: Uint8ClampedArray }, x: number, y: number, rgba: [number, number, number, number]): void {
  p.data.set(rgba, (y * p.width + x) * 4);
}

describe("diffPixels", () => {
  it("finds nothing in two identical images", () => {
    const d = diffPixels(fill(4, 3, [10, 20, 30, 255]), fill(4, 3, [10, 20, 30, 255]));
    expect(d).toMatchObject({ width: 4, height: 3, count: 0, bounds: null });
  });

  it("marks each changed pixel and boxes them", () => {
    const a = fill(5, 4, [0, 0, 0, 255]);
    const b = fill(5, 4, [0, 0, 0, 255]);
    set(b, 1, 1, [0, 0, 1, 255]); // one channel, one level
    set(b, 3, 2, [0, 0, 0, 254]); // alpha alone
    const d = diffPixels(a, b);
    expect(d.count).toBe(2);
    expect(d.bounds).toEqual({ x: 1, y: 1, w: 3, h: 2 });
    expect([...d.mask].map((m, i) => (m ? i : -1)).filter((i) => i >= 0)).toEqual([6, 13]);
  });

  it("treats fully transparent pixels as the same whatever colour sits under them", () => {
    expect(diffPixels(fill(2, 2, [255, 0, 0, 0]), fill(2, 2, [0, 255, 0, 0])).count).toBe(0);
  });

  it("compares different sizes top-left aligned, counting only what is visible", () => {
    const small = fill(2, 2, [9, 9, 9, 255]);
    const large = fill(3, 2, [9, 9, 9, 255]);
    set(large, 2, 1, [0, 0, 0, 0]); // the extra column: one opaque pixel, one clear
    const d = diffPixels(small, large);
    expect(d).toMatchObject({ width: 3, height: 2, count: 1, bounds: { x: 2, y: 0, w: 1, h: 1 } });
  });

  it("counts every visible pixel when one side is missing", () => {
    const only = fill(2, 1, [1, 2, 3, 255]);
    set(only, 1, 0, [0, 0, 0, 0]);
    expect(diffPixels(null, only).count).toBe(1);
    expect(diffPixels(only, null).count).toBe(1);
  });
});

describe("samePixels", () => {
  it("agrees with diffPixels", () => {
    const a = fill(3, 3, [5, 5, 5, 255]);
    const b = fill(3, 3, [5, 5, 5, 255]);
    expect(samePixels(a, b)).toBe(true);
    set(b, 2, 2, [5, 5, 6, 255]);
    expect(samePixels(a, b)).toBe(false);
    expect(samePixels(fill(1, 1, [1, 0, 0, 0]), fill(1, 1, [0, 1, 0, 0]))).toBe(true);
    expect(samePixels(fill(2, 1, [0, 0, 0, 0]), fill(1, 1, [0, 0, 0, 0]))).toBe(true);
    expect(samePixels(fill(2, 1, [0, 0, 0, 255]), fill(1, 1, [0, 0, 0, 255]))).toBe(false);
  });
});

describe("overlayPixels", () => {
  it("tints the changed pixels and leaves the rest clear", () => {
    const b = fill(2, 1, [0, 0, 0, 255]);
    set(b, 1, 0, [1, 1, 1, 255]);
    const out = overlayPixels(diffPixels(fill(2, 1, [0, 0, 0, 255]), b));
    expect([...out]).toEqual([0, 0, 0, 0, ...DIFF_TINT]);
  });
});

describe("describeDiff", () => {
  const withCount = (count: number, w = 1000, h = 1000) => ({ width: w, height: h, mask: new Uint8Array(0), count, bounds: null });
  it("says how many pixels differ and what share that is", () => {
    expect(describeDiff(withCount(0))).toBe("No pixel differs");
    expect(describeDiff(withCount(1))).toBe("1 pixel differs (<0.01%)");
    expect(describeDiff(withCount(1204))).toBe("1,204 pixels differ (0.12%)");
    expect(describeDiff(withCount(4000))).toBe("4,000 pixels differ (0.4%)");
    expect(describeDiff(withCount(250_000))).toBe("250,000 pixels differ (25%)");
  });
});
