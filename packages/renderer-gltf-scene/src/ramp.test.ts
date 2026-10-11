// The properties the ramp was chosen for, asserted so an edit that swaps it for
// something prettier has to argue with a failing test.

import { describe, it, expect } from "vitest";
import { RAMP_STOPS, rampCss, rampGradientCss, rampLinear, rampLuminance, rampSrgb } from "./ramp.js";

describe("the deviation ramp", () => {
  it("starts and ends on viridis's own endpoints", () => {
    expect(rampCss(0)).toBe("#440154");
    expect(rampCss(1)).toBe("#fde725");
    expect(RAMP_STOPS[0]).toBe("#440154");
    expect(RAMP_STOPS[RAMP_STOPS.length - 1]).toBe("#fde725");
  });

  it("rises monotonically in lightness across the whole range", () => {
    // The property that makes it readable in greyscale and under every form of
    // colour-vision deficiency — and the one a rainbow ramp does not have.
    let previous = -Infinity;
    for (let i = 0; i <= 200; i++) {
      const luminance = rampLuminance(i / 200);
      expect(luminance).toBeGreaterThan(previous);
      previous = luminance;
    }
  });

  it("spans a wide enough lightness range to read as a scale", () => {
    expect(rampLuminance(1) - rampLuminance(0)).toBeGreaterThan(0.5);
  });

  it("interpolates continuously between anchors", () => {
    // No visible step at an anchor boundary: the ramp is a scale, and a jump in
    // it would read as a threshold in the data.
    for (let i = 1; i < RAMP_STOPS.length - 1; i++) {
      const t = i / (RAMP_STOPS.length - 1);
      const before = rampSrgb(t - 1e-4);
      const after = rampSrgb(t + 1e-4);
      expect(Math.abs(after.r - before.r)).toBeLessThan(0.01);
      expect(Math.abs(after.g - before.g)).toBeLessThan(0.01);
      expect(Math.abs(after.b - before.b)).toBeLessThan(0.01);
    }
  });

  it("clamps out-of-range input instead of extrapolating off the map", () => {
    expect(rampCss(-3)).toBe(rampCss(0));
    expect(rampCss(9)).toBe(rampCss(1));
    // A max of zero divides into NaN upstream; the ramp must still be a colour.
    expect(rampCss(NaN)).toBe(rampCss(0));
  });

  it("converts to the linear working space three.js reads buffer colours in", () => {
    // sRGB values written straight into a colour attribute come out washed out,
    // and it looks like a lighting bug rather than a colour-space one.
    for (const t of [0, 0.25, 0.5, 0.75, 1]) {
      const srgb = rampSrgb(t);
      const linear = rampLinear(t);
      for (const key of ["r", "g", "b"] as const) {
        expect(linear[key]).toBeLessThanOrEqual(srgb[key] + 1e-9);
        expect(linear[key]).toBeGreaterThanOrEqual(0);
        expect(linear[key]).toBeLessThanOrEqual(1);
      }
    }
    expect(rampLinear(1).r).toBeCloseTo(0.9822, 3);
  });

  it("is NOT linear in colour space, so the shading reads high between corners", () => {
    // The correction this pins, and the reason `heatmap.readAt`'s doc no longer
    // promises that picture and number agree everywhere.
    //
    // `buildColors` writes rampLinear(t) per VERTEX and heatMaterial sets
    // `vertexColors` on a stock three material, so the GPU blends the three
    // corner COLOURS across a face. That is lerp(viridis(t)), not
    // viridis(lerp(t)) — and viridis is a piecewise-linear sRGB curve through a
    // gamma transfer, so the two are not the same map. `readAt` reports
    // viridis's ARGUMENT blended, which is the honest reading of the
    // measurement; the pixel beside it is the other one.
    //
    // If this ever fails because the two agree, the ramp lookup has moved into a
    // fragment shader and `faceValue`'s doc should say so instead.
    const midColour = (t0: number, t1: number): { r: number; g: number; b: number } => {
      const a = rampLinear(t0);
      const b = rampLinear(t1);
      return { r: (a.r + b.r) / 2, g: (a.g + b.g) / 2, b: (a.b + b.b) / 2 };
    };
    // What t the shown pixel decodes to, read off the ramp the legend draws.
    const decode = (c: { r: number; g: number; b: number }): number => {
      let best = 0;
      let closest = Infinity;
      for (let i = 0; i <= 2000; i++) {
        const s = rampLinear(i / 2000);
        const d = (s.r - c.r) ** 2 + (s.g - c.g) ** 2 + (s.b - c.b) ** 2;
        if (d < closest) {
          closest = d;
          best = i / 2000;
        }
      }
      return best;
    };

    // The demo car body's side panels: corners at 0 mm and at the top of the
    // range. readAt says t=0.5 (60 mm of 120); the pixel reads ~0.88 (~105 mm).
    expect(decode(midColour(0, 1))).toBeGreaterThan(0.85);
    // And it is high, not merely different, right across the ramp.
    for (const [t0, t1] of [
      [0, 0.5],
      [0.25, 0.75],
      [0.5, 1],
    ] as const) {
      expect(decode(midColour(t0, t1))).toBeGreaterThan((t0 + t1) / 2 + 0.01);
    }
    // Agreement is exact AT a vertex, which is the part the doc may still claim.
    for (const t of [0, 0.25, 0.5, 0.75, 1]) expect(decode(rampLinear(t))).toBeCloseTo(t, 2);
  });

  it("renders as a CSS gradient with every anchor in it", () => {
    const gradient = rampGradientCss();
    expect(gradient.startsWith("linear-gradient(90deg,")).toBe(true);
    for (const stop of RAMP_STOPS) expect(gradient).toContain(stop);
    expect(gradient).toContain("0.0%");
    expect(gradient).toContain("100.0%");
  });
});
