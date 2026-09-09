// The measurement itself, on meshes whose right answer is known by construction.
//
// The scenarios are the ones the heatmap exists to distinguish: a surface pushed
// out by a known amount (every vertex reads that amount), and a part where one
// face moved and another didn't (the untouched face reads zero next to it). Pure
// arithmetic — the three.js side of the same measurement is heatmap.test.ts.

import { describe, it, expect } from "vitest";
import { buildSurfaceIndex } from "./closest-point.js";
import { CHUNK_VERTICES, deviationChunked, deviationSync, formatDeviation } from "./deviation.js";

/**
 * A w × w grid of quads at z = `lift`, as an indexed triangle mesh. `wobble`
 * bends it into a surface with real curvature — a flat plane is the easiest
 * possible workload for the traversal, so timing one would flatter it.
 */
function plane(w: number, lift: number, wobble = 0): { positions: Float32Array; index: Uint32Array } {
  const side = w + 1;
  const positions = new Float32Array(side * side * 3);
  for (let y = 0; y <= w; y++) {
    for (let x = 0; x <= w; x++) {
      const at = (y * side + x) * 3;
      positions[at] = x / w;
      positions[at + 1] = y / w;
      positions[at + 2] = lift + wobble * Math.sin(x * 0.3) * Math.cos(y * 0.21);
    }
  }
  const index = new Uint32Array(w * w * 6);
  let at = 0;
  for (let y = 0; y < w; y++) {
    for (let x = 0; x < w; x++) {
      const i = y * side + x;
      index[at++] = i;
      index[at++] = i + 1;
      index[at++] = i + side;
      index[at++] = i + 1;
      index[at++] = i + side + 1;
      index[at++] = i + side;
    }
  }
  return { positions, index };
}

const immediately = (): Promise<void> => Promise.resolve();

describe("deviation", () => {
  it("reads a uniformly lifted surface as that lift, everywhere", () => {
    const base = plane(6, 0);
    const head = plane(6, 0.12);
    const measured = deviationSync(head.positions, buildSurfaceIndex(base.positions, base.index));
    expect(measured.values.length).toBe(head.positions.length / 3);
    for (const value of measured.values) expect(value).toBeCloseTo(0.12, 5);
    expect(measured.max).toBeCloseTo(0.12, 5);
    expect(measured.min).toBeCloseTo(0.12, 5);
  });

  it("reads zero where the surface didn't move", () => {
    const base = plane(6, 0);
    const measured = deviationSync(base.positions, buildSurfaceIndex(base.positions, base.index));
    expect(measured.max).toBeCloseTo(0, 6);
  });

  it("separates a sculpted face from an untouched one on the same part", () => {
    // The sculpt fixture's shape: a top face raised +0.12 in Z, a bottom face
    // left alone. The whole point of the picture is that these read differently
    // — a per-part number could not tell them apart at all.
    const top = plane(4, 0);
    const bottom = plane(4, -1);
    const vertices = top.positions.length / 3;
    const join = (a: typeof top, b: typeof bottom): { positions: Float32Array; index: Uint32Array } => {
      const positions = new Float32Array(a.positions.length + b.positions.length);
      positions.set(a.positions, 0);
      positions.set(b.positions, a.positions.length);
      const index = new Uint32Array(a.index.length + b.index.length);
      index.set(a.index, 0);
      for (let i = 0; i < b.index.length; i++) index[a.index.length + i] = b.index[i]! + vertices;
      return { positions, index };
    };
    const base = join(top, bottom);
    const head = join(plane(4, 0.12), bottom);

    const measured = deviationSync(head.positions, buildSurfaceIndex(base.positions, base.index));
    for (let v = 0; v < vertices; v++) expect(measured.values[v]!).toBeCloseTo(0.12, 5);
    for (let v = vertices; v < measured.values.length; v++) expect(measured.values[v]!).toBeCloseTo(0, 6);
    expect(measured.max).toBeCloseTo(0.12, 5);
    expect(measured.min).toBeCloseTo(0, 6);
  });

  it("is unaffected by topology: a denser head mesh over the same surface reads zero", () => {
    // The reason this is a closest-point-on-triangle query and not a vertex
    // comparison. The two meshes describe the same plane with different vertex
    // counts, so no index-wise or nearest-vertex measure would return zero.
    const base = plane(3, 0);
    const head = plane(11, 0);
    const measured = deviationSync(head.positions, buildSurfaceIndex(base.positions, base.index));
    expect(measured.max).toBeCloseTo(0, 6);
  });

  it("chunked yields the same numbers as one pass", async () => {
    const base = plane(5, 0);
    const head = plane(5, 0.03);
    const surface = buildSurfaceIndex(base.positions, base.index);
    const once = deviationSync(head.positions, surface);
    const sliced = await deviationChunked(head.positions, surface, immediately, undefined, 7);
    expect(sliced).not.toBeNull();
    expect([...sliced!.values]).toEqual([...once.values]);
    expect(sliced!.max).toBe(once.max);
  });

  it("stops at the next slice boundary when the reviewer cancels", async () => {
    const base = plane(8, 0);
    const head = plane(8, 0.02);
    const surface = buildSurfaceIndex(base.positions, base.index);
    const signal = { cancelled: false };
    let slices = 0;
    const yielder = async (): Promise<void> => {
      slices++;
      signal.cancelled = true;
    };
    expect(await deviationChunked(head.positions, surface, yielder, signal, 4)).toBeNull();
    // One slice ran, the yield cancelled, and nothing after it was measured.
    expect(slices).toBe(1);
  });

  it("yields between slices rather than blocking on the whole mesh", async () => {
    const base = plane(10, 0);
    const head = plane(10, 0.01);
    let yields = 0;
    await deviationChunked(head.positions, buildSurfaceIndex(base.positions, base.index), async () => {
      yields++;
    }, undefined, 16);
    const vertices = head.positions.length / 3;
    expect(yields).toBe(Math.ceil(vertices / 16) - 1);
  });

  it("measures a 100k-vertex mesh inside the interactive budget", () => {
    // #46's budget is ~1.5 s for index + query at this size; the assertion below
    // is deliberately far looser so a loaded CI box can't fail the suite over
    // scheduling noise. The measured number is logged either way — a silent
    // regression from 0.8 s to 4.9 s would still pass, and shouldn't pass
    // unnoticed.
    const w = 316; // 317² = 100 489 vertices, 199 712 triangles
    const base = plane(w, 0, 0.05);
    const head = plane(w, 0.004, 0.05);
    const started = now();
    const surface = buildSurfaceIndex(base.positions, base.index);
    const indexed = now();
    const measured = deviationSync(head.positions, surface);
    const done = now();
    const total = done - started;
    console.log(
      `deviation perf: ${measured.values.length} vertices vs ${surface.triangles} triangles — ` +
        `index ${(indexed - started).toFixed(0)} ms, query ${(done - indexed).toFixed(0)} ms, ` +
        `total ${total.toFixed(0)} ms`,
    );
    // A curved surface lifted straight up: the closest point is not directly
    // below, so the reading is at most the lift and never more.
    expect(measured.max).toBeGreaterThan(0);
    expect(measured.max).toBeLessThanOrEqual(0.004 + 1e-6);
    expect(total).toBeLessThan(5000);
  }, 60_000);
});

function now(): number {
  return typeof performance === "object" ? performance.now() : Date.now();
}

describe("formatDeviation", () => {
  it("reports millimetre-scale edits in millimetres, which is the whole point", () => {
    expect(formatDeviation(0.012)).toBe("12.0 mm");
    expect(formatDeviation(0.0012)).toBe("1.20 mm");
    expect(formatDeviation(0.00004)).toBe("0.040 mm");
  });

  it("switches to metres once millimetres stop being readable", () => {
    expect(formatDeviation(1.5)).toBe("1.50 m");
    expect(formatDeviation(42)).toBe("42.0 m");
  });

  it("has a mark for no measurement rather than printing NaN at a reviewer", () => {
    expect(formatDeviation(NaN)).toBe("—");
    expect(formatDeviation(Infinity)).toBe("—");
  });

  it("exports a chunk size a frame can afford", () => {
    expect(CHUNK_VERTICES).toBeGreaterThan(0);
    expect(CHUNK_VERTICES).toBeLessThanOrEqual(16384);
  });
});
