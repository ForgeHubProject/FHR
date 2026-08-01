// The measurement's foundations: the triangle projection, and the index that
// makes asking it 100 000 times affordable. Pure arithmetic — no three.js, no
// DOM, no WebGL — so all of it runs here for real.

import { describe, it, expect } from "vitest";
import { buildSurfaceIndex, buildSurfaceIndexChunked, closestPointOnTriangle } from "./closest-point.js";

/** Project P onto ABC and return the point, for readable assertions. */
function project(
  p: readonly [number, number, number],
  a: readonly [number, number, number],
  b: readonly [number, number, number],
  c: readonly [number, number, number],
): [number, number, number] {
  const out = { 0: 0, 1: 0, 2: 0 };
  closestPointOnTriangle(p[0], p[1], p[2], a[0], a[1], a[2], b[0], b[1], b[2], c[0], c[1], c[2], out);
  return [out[0], out[1], out[2]];
}

/** The unit right triangle in the z = 0 plane. */
const A: readonly [number, number, number] = [0, 0, 0];
const B: readonly [number, number, number] = [1, 0, 0];
const C: readonly [number, number, number] = [0, 1, 0];

const near = (got: readonly number[], want: readonly number[], tolerance = 1e-6): void => {
  expect(got.length).toBe(want.length);
  for (let i = 0; i < want.length; i++) expect(got[i]!).toBeCloseTo(want[i]!, Math.log10(1 / tolerance));
};

describe("closestPointOnTriangle", () => {
  it("projects a point above the interior straight down onto the face", () => {
    near(project([0.25, 0.25, 3], A, B, C), [0.25, 0.25, 0]);
  });

  it("returns the point itself when it is already on the face", () => {
    near(project([0.3, 0.4, 0], A, B, C), [0.3, 0.4, 0]);
  });

  it("clamps to a vertex when the point is in that vertex's region", () => {
    // Outside both edges meeting at A — the answer is A, not a projection onto
    // either edge's infinite line, which is where a naive clamp lands.
    near(project([-2, -3, 1], A, B, C), [0, 0, 0]);
    near(project([5, -1, -2], A, B, C), [1, 0, 0]);
    near(project([-1, 4, 0], A, B, C), [0, 1, 0]);
  });

  it("clamps to a point along an edge when the point is beside it", () => {
    // Beside the AB edge (y < 0, x within the edge's slab).
    near(project([0.5, -2, 0], A, B, C), [0.5, 0, 0]);
    // Beside the AC edge.
    near(project([-2, 0.25, 0], A, B, C), [0, 0.25, 0]);
    // Beside the hypotenuse: the foot of the perpendicular from (1,1,0).
    near(project([1, 1, 0], A, B, C), [0.5, 0.5, 0]);
  });

  it("answers a degenerate triangle with a finite point on it, never NaN", () => {
    // Zero-area faces are ordinary in exported geometry. The contract is only
    // that the answer is finite and lies on the (collapsed) triangle: a face
    // with no surface loses the minimum to one that has surface, whereas a NaN
    // would poison every comparison downstream of it.
    for (const p of [
      [2, 0, 0],
      [0, 3, 0],
      [-1, -1, -1],
    ] as const) {
      const segment = project(p, [0, 0, 0], [0, 0, 0], [1, 0, 0]);
      for (const v of segment) expect(Number.isFinite(v)).toBe(true);
      expect(segment[1]).toBeCloseTo(0, 9);
      expect(segment[2]).toBeCloseTo(0, 9);
      expect(segment[0]).toBeGreaterThanOrEqual(0);
      expect(segment[0]).toBeLessThanOrEqual(1);

      const collapsed = project(p, [0, 0, 0], [0, 0, 0], [0, 0, 0]);
      near(collapsed, [0, 0, 0]);
    }
  });
});

/** A w × w grid of quads on the z = 0 plane, as an indexed triangle mesh. */
function grid(w: number, lift = 0): { positions: Float32Array; index: Uint32Array } {
  const side = w + 1;
  const positions = new Float32Array(side * side * 3);
  for (let y = 0; y <= w; y++) {
    for (let x = 0; x <= w; x++) {
      const at = (y * side + x) * 3;
      positions[at] = x / w;
      positions[at + 1] = y / w;
      positions[at + 2] = lift;
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

describe("buildSurfaceIndex", () => {
  it("has no answer for an empty soup, and says so with Infinity", () => {
    expect(buildSurfaceIndex(new Float32Array(0)).closestDistanceSquared(0, 0, 0)).toBe(Infinity);
    expect(buildSurfaceIndex(new Float32Array(0)).triangles).toBe(0);
  });

  it("indexes a non-indexed soup as consecutive triples", () => {
    const positions = new Float32Array([0, 0, 0, 1, 0, 0, 0, 1, 0, 5, 0, 0, 6, 0, 0, 5, 1, 0]);
    const index = buildSurfaceIndex(positions);
    expect(index.triangles).toBe(2);
    expect(Math.sqrt(index.closestDistanceSquared(5.2, 0.2, 2))).toBeCloseTo(2, 6);
  });

  it("agrees with a brute-force scan over every triangle", () => {
    const { positions, index } = grid(12);
    const bvh = buildSurfaceIndex(positions, index);
    const scratch = { 0: 0, 1: 0, 2: 0 };
    const brute = (x: number, y: number, z: number): number => {
      let best = Infinity;
      for (let t = 0; t < index.length; t += 3) {
        const a = index[t]! * 3;
        const b = index[t + 1]! * 3;
        const c = index[t + 2]! * 3;
        closestPointOnTriangle(
          x, y, z,
          positions[a]!, positions[a + 1]!, positions[a + 2]!,
          positions[b]!, positions[b + 1]!, positions[b + 2]!,
          positions[c]!, positions[c + 1]!, positions[c + 2]!,
          scratch,
        );
        const d = (scratch[0] - x) ** 2 + (scratch[1] - y) ** 2 + (scratch[2] - z) ** 2;
        if (d < best) best = d;
      }
      return best;
    };
    // Deterministic pseudo-random probes, inside the mesh's footprint and well
    // outside it: the traversal's pruning is what differs between the two.
    let seed = 12345;
    const next = (): number => ((seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff);
    for (let i = 0; i < 400; i++) {
      const x = next() * 3 - 1;
      const y = next() * 3 - 1;
      const z = next() * 2 - 1;
      expect(bvh.closestDistanceSquared(x, y, z)).toBeCloseTo(brute(x, y, z), 9);
    }
  });

  it("keeps answering correctly when every centroid is the same point", () => {
    // A fan of coincident-centroid triangles: the spatial split cannot separate
    // them, and the builder has to make a leaf rather than loop.
    const positions: number[] = [];
    for (let i = 0; i < 64; i++) {
      positions.push(-1, 0, 0, 1, 0, 0, 0, 0, 0);
    }
    const bvh = buildSurfaceIndex(new Float32Array(positions));
    expect(bvh.triangles).toBe(64);
    expect(Math.sqrt(bvh.closestDistanceSquared(0, 4, 0))).toBeCloseTo(4, 5);
  });
});

describe("buildSurfaceIndexChunked", () => {
  /** The same probes against two indexes, in the same order — the traversal
   *  carries a seeded best-guess between queries, so order is part of the run. */
  const probe = (bvh: { closestDistanceSquared(x: number, y: number, z: number): number }): number[] => {
    const out: number[] = [];
    let seed = 999;
    const next = (): number => ((seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff);
    for (let i = 0; i < 200; i++) out.push(bvh.closestDistanceSquared(next() * 3 - 1, next() * 3 - 1, next() * 2 - 1));
    return out;
  };

  it("yields between slices and answers exactly as the one-pass build does", async () => {
    // The build is not a rounding error beside the measurement it feeds: 200k
    // triangles is ~200 ms here, which is a dozen frames dropped at the moment
    // the reviewer just asked the model a question. `sliceMs: 0` makes every
    // checkpoint yield so the test sees the slicing without needing that mesh.
    const { positions, index } = grid(12);
    const once = buildSurfaceIndex(positions, index);
    let yields = 0;
    const sliced = await buildSurfaceIndexChunked(
      positions,
      index,
      async () => {
        yields++;
      },
      undefined,
      0,
    );
    expect(sliced).not.toBeNull();
    // Every phase — unpack, tree, repack — has to be sliced; one is not enough.
    expect(yields).toBeGreaterThan(2);
    expect(sliced!.triangles).toBe(once.triangles);
    expect(probe(sliced!)).toEqual(probe(once));
  });

  it("abandons the build when the reviewer switches the heatmap off", async () => {
    const { positions, index } = grid(12);
    const signal = { cancelled: false };
    let yields = 0;
    const sliced = await buildSurfaceIndexChunked(
      positions,
      index,
      async () => {
        yields++;
        signal.cancelled = true;
      },
      signal,
      0,
    );
    expect(sliced).toBeNull();
    // Stopped at the first boundary, not after finishing the tree anyway.
    expect(yields).toBe(1);
  });

  it("runs to completion without yielding when the mesh fits in one slice", async () => {
    const positions = new Float32Array([0, 0, 0, 1, 0, 0, 0, 1, 0]);
    let yields = 0;
    const sliced = await buildSurfaceIndexChunked(positions, null, async () => {
      yields++;
    });
    expect(yields).toBe(0);
    expect(sliced!.triangles).toBe(1);
  });
});
