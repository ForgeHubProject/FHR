// "How far is this point from that surface?", fast enough to ask 100 000 times.
//
// The deviation heatmap (#46) needs, for every vertex of the *current* mesh, the
// distance to the closest point on the *previous* mesh's surface. Not to the
// closest base vertex, and emphatically not to the base vertex with the same
// index: a re-tessellated or decimated edit changes topology, so index i on one
// side is a different corner of the model on the other, and matching by index
// reports enormous deviation for geometry nobody touched. Vertex-to-vertex is
// only marginally better — it saturates wherever the two meshes have different
// sampling density, which is most of what a sculpt does.
//
// So: closest point on a *triangle*, over a spatial index of every base triangle.
//
// Hand-rolled rather than taking `three-mesh-bvh` (+15.8 KB gzip). Measured on
// this build, a 100k-vertex mesh indexes and queries in well under the 1.5 s
// budget the issue set, so the dependency buys nothing the budget needs. Kept
// deliberately plain: flat typed arrays, no objects per node or per triangle, no
// three.js — this file is pure numbers so it is exercised headlessly and could be
// moved to a worker unchanged.

/** Triangles per leaf. Small enough to prune, large enough that the leaf scan
 *  amortises the traversal — measured flat between 4 and 16. */
const LEAF_SIZE = 8;

/**
 * The closest point on triangle ABC to P, written into `out` (3 numbers).
 *
 * Ericson, *Real-Time Collision Detection* §5.1.5: classify P against the
 * triangle's seven Voronoi regions — three vertices, three edges, the interior —
 * and project accordingly. The region test is what makes it exact at the
 * boundaries: a naive "project onto the plane, then clamp barycentrics" is wrong
 * for points outside an edge's slab, and wrong in exactly the place a deviation
 * map cares about (the rim of an edited patch).
 *
 * Degenerate triangles (zero area, repeated vertices) are answered with a point
 * ON the triangle rather than with a division by zero. Real exports contain
 * them, and the caller takes a minimum over many faces, so a conservative answer
 * from a face with no surface simply loses — where a NaN would poison the
 * running best and every comparison made against it.
 */
export function closestPointOnTriangle(
  px: number,
  py: number,
  pz: number,
  ax: number,
  ay: number,
  az: number,
  bx: number,
  by: number,
  bz: number,
  cx: number,
  cy: number,
  cz: number,
  out: { 0: number; 1: number; 2: number },
): void {
  const abx = bx - ax;
  const aby = by - ay;
  const abz = bz - az;
  const acx = cx - ax;
  const acy = cy - ay;
  const acz = cz - az;
  const apx = px - ax;
  const apy = py - ay;
  const apz = pz - az;

  const d1 = abx * apx + aby * apy + abz * apz;
  const d2 = acx * apx + acy * apy + acz * apz;
  if (d1 <= 0 && d2 <= 0) {
    out[0] = ax;
    out[1] = ay;
    out[2] = az;
    return;
  }

  const bpx = px - bx;
  const bpy = py - by;
  const bpz = pz - bz;
  const d3 = abx * bpx + aby * bpy + abz * bpz;
  const d4 = acx * bpx + acy * bpy + acz * bpz;
  if (d3 >= 0 && d4 <= d3) {
    out[0] = bx;
    out[1] = by;
    out[2] = bz;
    return;
  }

  const vc = d1 * d4 - d3 * d2;
  if (vc <= 0 && d1 >= 0 && d3 <= 0) {
    const v = d1 - d3 > 0 ? d1 / (d1 - d3) : 0;
    out[0] = ax + abx * v;
    out[1] = ay + aby * v;
    out[2] = az + abz * v;
    return;
  }

  const cpx = px - cx;
  const cpy = py - cy;
  const cpz = pz - cz;
  const d5 = abx * cpx + aby * cpy + abz * cpz;
  const d6 = acx * cpx + acy * cpy + acz * cpz;
  if (d6 >= 0 && d5 <= d6) {
    out[0] = cx;
    out[1] = cy;
    out[2] = cz;
    return;
  }

  const vb = d5 * d2 - d1 * d6;
  if (vb <= 0 && d2 >= 0 && d6 <= 0) {
    const w = d2 - d6 > 0 ? d2 / (d2 - d6) : 0;
    out[0] = ax + acx * w;
    out[1] = ay + acy * w;
    out[2] = az + acz * w;
    return;
  }

  const va = d3 * d6 - d5 * d4;
  if (va <= 0 && d4 - d3 >= 0 && d5 - d6 >= 0) {
    const span = d4 - d3 + (d5 - d6);
    const w = span > 0 ? (d4 - d3) / span : 0;
    out[0] = bx + (cx - bx) * w;
    out[1] = by + (cy - by) * w;
    out[2] = bz + (cz - bz) * w;
    return;
  }

  // Interior: barycentric coordinates of the perpendicular foot.
  const area = va + vb + vc;
  if (!(area > 0)) {
    // A zero-area triangle that reached this far. Answering with a vertex keeps
    // the result finite and ON the triangle, which is all the caller needs: a
    // degenerate face has no surface to be close to, so an over-reported
    // distance simply loses the min to a face that has one. A NaN would not —
    // it poisons the running best and every comparison against it.
    out[0] = ax;
    out[1] = ay;
    out[2] = az;
    return;
  }
  const denom = 1 / area;
  const v = vb * denom;
  const w = vc * denom;
  out[0] = ax + abx * v + acx * w;
  out[1] = ay + aby * v + acy * w;
  out[2] = az + abz * v + acz * w;
}

/** A triangle soup you can ask "what is the nearest surface point to P". */
export type SurfaceIndex = {
  readonly triangles: number;
  /**
   * Squared distance from P to the closest point on any triangle. Squared
   * because every use of it is a comparison or a sqrt at the very end, and the
   * inner loop runs once per (vertex × candidate triangle).
   *
   * Infinity for an empty index — callers must treat that as "no answer", never
   * as "very far".
   */
  closestDistanceSquared(x: number, y: number, z: number): number;
};

const EMPTY: SurfaceIndex = {
  triangles: 0,
  closestDistanceSquared: (): number => Infinity,
};

/**
 * Index a triangle soup: a median-split BVH over triangle centroids, flattened
 * into typed arrays.
 *
 * `positions` is a glTF POSITION attribute's array (3 floats per vertex);
 * `index` is the primitive's index buffer, or null for a non-indexed primitive
 * where every three vertices are one triangle. Both are read in the geometry's
 * OWN space — see heatmap.ts for why the comparison is made there and not in
 * world space.
 */
export function buildSurfaceIndex(
  positions: ArrayLike<number>,
  index?: ArrayLike<number> | null,
): SurfaceIndex {
  const count = index ? Math.floor(index.length / 3) : Math.floor(positions.length / 9);
  if (count <= 0) return EMPTY;

  // Triangle vertices, unpacked once. float32 because the source attribute is
  // float32 — a float64 copy would triple the footprint of a 200k-triangle mesh
  // to buy precision the input never had.
  const tri = new Float32Array(count * 9);
  const centroid = new Float32Array(count * 3);
  for (let t = 0; t < count; t++) {
    const i0 = (index ? index[t * 3]! : t * 3) * 3;
    const i1 = (index ? index[t * 3 + 1]! : t * 3 + 1) * 3;
    const i2 = (index ? index[t * 3 + 2]! : t * 3 + 2) * 3;
    const at = t * 9;
    for (let k = 0; k < 3; k++) {
      const a = positions[i0 + k] ?? 0;
      const b = positions[i1 + k] ?? 0;
      const c = positions[i2 + k] ?? 0;
      tri[at + k] = a;
      tri[at + 3 + k] = b;
      tri[at + 6 + k] = c;
      centroid[t * 3 + k] = (a + b + c) / 3;
    }
  }

  // Triangle ids in the order the tree wants them. Leaves address a contiguous
  // slice of this, so the scan of a leaf is a linear walk.
  const order = new Uint32Array(count);
  for (let t = 0; t < count; t++) order[t] = t;

  // Node arrays, grown as the tree is built and frozen into typed arrays after.
  // Children of a node are always adjacent (`left`, `left + 1`), so one int per
  // node addresses both.
  const bounds: number[] = [];
  const left: number[] = [];
  const start: number[] = [];
  const span: number[] = [];

  const newNode = (): number => {
    const at = left.length;
    bounds.push(0, 0, 0, 0, 0, 0);
    left.push(-1);
    start.push(0);
    span.push(0);
    return at;
  };

  /** Triangle bounds of [from, to), written into node `n`. */
  const setBounds = (n: number, from: number, to: number): void => {
    let minX = Infinity;
    let minY = Infinity;
    let minZ = Infinity;
    let maxX = -Infinity;
    let maxY = -Infinity;
    let maxZ = -Infinity;
    for (let i = from; i < to; i++) {
      const at = order[i]! * 9;
      for (let v = 0; v < 9; v += 3) {
        const x = tri[at + v]!;
        const y = tri[at + v + 1]!;
        const z = tri[at + v + 2]!;
        if (x < minX) minX = x;
        if (y < minY) minY = y;
        if (z < minZ) minZ = z;
        if (x > maxX) maxX = x;
        if (y > maxY) maxY = y;
        if (z > maxZ) maxZ = z;
      }
    }
    const b = n * 6;
    bounds[b] = minX;
    bounds[b + 1] = minY;
    bounds[b + 2] = minZ;
    bounds[b + 3] = maxX;
    bounds[b + 4] = maxY;
    bounds[b + 5] = maxZ;
  };

  // Explicit stack rather than recursion: a pathological mesh (every centroid
  // coincident under a spatial split) can drive the tree deeper than the JS call
  // stack tolerates, and a RangeError here would take out the whole 3D view.
  const todo: number[] = [newNode(), 0, count, 0];
  let maxDepth = 0;
  while (todo.length > 0) {
    const depth = todo.pop()!;
    const to = todo.pop()!;
    const from = todo.pop()!;
    const node = todo.pop()!;
    if (depth > maxDepth) maxDepth = depth;
    setBounds(node, from, to);
    const n = to - from;
    if (n <= LEAF_SIZE) {
      start[node] = from;
      span[node] = n;
      continue;
    }

    // Split on the widest axis of the *centroid* bounds — the triangle bounds
    // are inflated by triangle size and pick the wrong axis for long thin faces.
    let cMinX = Infinity;
    let cMinY = Infinity;
    let cMinZ = Infinity;
    let cMaxX = -Infinity;
    let cMaxY = -Infinity;
    let cMaxZ = -Infinity;
    for (let i = from; i < to; i++) {
      const at = order[i]! * 3;
      const x = centroid[at]!;
      const y = centroid[at + 1]!;
      const z = centroid[at + 2]!;
      if (x < cMinX) cMinX = x;
      if (y < cMinY) cMinY = y;
      if (z < cMinZ) cMinZ = z;
      if (x > cMaxX) cMaxX = x;
      if (y > cMaxY) cMaxY = y;
      if (z > cMaxZ) cMaxZ = z;
    }
    const ex = cMaxX - cMinX;
    const ey = cMaxY - cMinY;
    const ez = cMaxZ - cMinZ;
    const axis = ex >= ey && ex >= ez ? 0 : ey >= ez ? 1 : 2;
    const extent = axis === 0 ? ex : axis === 1 ? ey : ez;
    if (!(extent > 0)) {
      // Every centroid in this range is the same point — no plane separates
      // them. Cut by count instead of looping forever on a split that can't move.
      start[node] = from;
      span[node] = n;
      continue;
    }

    const plane = (axis === 0 ? cMinX + cMaxX : axis === 1 ? cMinY + cMaxY : cMinZ + cMaxZ) / 2;
    let mid = from;
    for (let i = from; i < to; i++) {
      if (centroid[order[i]! * 3 + axis]! < plane) {
        const swap = order[i]!;
        order[i] = order[mid]!;
        order[mid] = swap;
        mid++;
      }
    }
    // A spatial median can put everything on one side (clustered geometry with
    // one far outlier). Fall back to the object median, which always splits.
    if (mid === from || mid === to) mid = (from + to) >> 1;

    const l = newNode();
    newNode(); // right, adjacent by construction
    left[node] = l;
    todo.push(l, from, mid, depth + 1, l + 1, mid, to, depth + 1);
  }

  const nodeBounds = Float32Array.from(bounds);
  const nodeLeft = Int32Array.from(left);
  const nodeStart = Int32Array.from(start);
  const nodeSpan = Int32Array.from(span);
  // Repacked in leaf order so a leaf scan reads one contiguous run. Worth the
  // extra pass: the leaf scan is the innermost loop of the whole computation.
  const packed = new Float32Array(count * 9);
  for (let i = 0; i < count; i++) packed.set(tri.subarray(order[i]! * 9, order[i]! * 9 + 9), i * 9);

  // Ordered traversal pushes two children and pops one per level, so the stack
  // never exceeds the tree's depth — but that depth is a property of the mesh,
  // not a constant, and a fixed-size Int32Array would silently DROP writes past
  // its end rather than throwing. Sized from the tree that was actually built.
  const stack = new Int32Array(maxDepth + 4);
  const hit = { 0: 0, 1: 0, 2: 0 };
  // The triangle that answered the *previous* query, used to seed the search
  // bound for the next one. Vertices arrive in mesh order, which is spatially
  // coherent, so this is usually the answer again or very near it — and a tight
  // initial bound is what turns the traversal from "visit the tree" into "reject
  // the tree". Measured ~3× on a real mesh; it is never wrong, only unhelpful.
  let lastHit = 0;

  const triangleDistanceSquared = (t: number, x: number, y: number, z: number): number => {
    const at = t * 9;
    closestPointOnTriangle(
      x,
      y,
      z,
      packed[at]!,
      packed[at + 1]!,
      packed[at + 2]!,
      packed[at + 3]!,
      packed[at + 4]!,
      packed[at + 5]!,
      packed[at + 6]!,
      packed[at + 7]!,
      packed[at + 8]!,
      hit,
    );
    const dx = hit[0] - x;
    const dy = hit[1] - y;
    const dz = hit[2] - z;
    return dx * dx + dy * dy + dz * dz;
  };

  /** Squared distance from P to a node's box; 0 when P is inside it. */
  const boxDistanceSquared = (n: number, x: number, y: number, z: number): number => {
    const b = n * 6;
    const dx = x < nodeBounds[b]! ? nodeBounds[b]! - x : x > nodeBounds[b + 3]! ? x - nodeBounds[b + 3]! : 0;
    const dy =
      y < nodeBounds[b + 1]! ? nodeBounds[b + 1]! - y : y > nodeBounds[b + 4]! ? y - nodeBounds[b + 4]! : 0;
    const dz =
      z < nodeBounds[b + 2]! ? nodeBounds[b + 2]! - z : z > nodeBounds[b + 5]! ? z - nodeBounds[b + 5]! : 0;
    return dx * dx + dy * dy + dz * dz;
  };

  return {
    triangles: count,
    closestDistanceSquared(x: number, y: number, z: number): number {
      let best = triangleDistanceSquared(lastHit, x, y, z);
      let bestTri = lastHit;
      let sp = 0;
      stack[sp++] = 0;
      while (sp > 0) {
        const node = stack[--sp]!;
        if (boxDistanceSquared(node, x, y, z) >= best) continue;
        const l = nodeLeft[node]!;
        if (l < 0) {
          const from = nodeStart[node]!;
          const to = from + nodeSpan[node]!;
          for (let t = from; t < to; t++) {
            const d = triangleDistanceSquared(t, x, y, z);
            if (d < best) {
              best = d;
              bestTri = t;
            }
          }
          continue;
        }
        // Nearer child popped first, so the far one is usually rejected by the
        // bound the near one just tightened rather than descended into.
        const dl = boxDistanceSquared(l, x, y, z);
        const dr = boxDistanceSquared(l + 1, x, y, z);
        if (dl < dr) {
          if (dr < best) stack[sp++] = l + 1;
          if (dl < best) stack[sp++] = l;
        } else {
          if (dl < best) stack[sp++] = l;
          if (dr < best) stack[sp++] = l + 1;
        }
      }
      lastHit = bestTri;
      return best;
    },
  };
}
