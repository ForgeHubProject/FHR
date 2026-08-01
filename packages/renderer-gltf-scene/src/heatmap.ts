// The deviation heatmap: changed geometry coloured by how far it moved (#46).
//
// A sub-view of overlay mode, not a mode of its own. Overlay is already the
// "where, and how far" rung of the ladder — the previous version underneath the
// current one — and the heatmap is the quantitative reading of the same
// question, so it belongs inside it rather than beside it. Outside overlay it is
// not offered: on a side-by-side pane it would recolour a model the pane's label
// promises is the file's own, and in structural mode it would compete with the
// change grammar for the same surfaces.
//
// ── what is measured, and in which space ─────────────────────────────────────
//
// For every vertex of the current mesh: the distance to the closest point on the
// *previous* mesh's surface (closest-point.ts). Surface, not vertices — see that
// file for why an index-wise or nearest-vertex comparison is wrong the moment
// topology changes, which is most of what an edit does.
//
// Both sides are mapped through ONE transform — the *head* node's — before they
// are compared. That is the transform-delta subtraction #46 asks for: a node
// that was only moved carries an unedited mesh, so it produces no geometry row
// in the diff, is never a heatmap target, and reads as *moved* (the motion
// vector) — which is what it is; and a node that was moved *and* sculpted is
// measured with its motion divided out, because the previous shape is placed by
// the current pose before the distance is taken. Measuring each side at its own
// pose would instead paint a rigid translation as a fully deviated surface, the
// single most misleading thing this view could do.
//
// One transform, but the head's and not the identity, because the number is
// labelled in millimetres. glTF fixes the metre as the unit of the SCENE (spec
// §3.3), not of a node's local space: a mesh authored in millimetres and hung
// under a node scaled 0.001 — the ordinary CAD and Blender export — has local
// coordinates a thousand times its real size, and a heatmap that read them raw
// would report a 0.12 mm lift as "120 mm". So the node chain's transform is
// applied to both sides, and the answer comes out in the scene's own metres.
//
// Only the 3×3 linear part is applied: translation cancels out of a distance,
// and dropping it keeps float32 arithmetic near the geometry rather than out at
// whatever offset the node sits at.
//
// Shared geometry instanced at two different scales is the one case with no
// single answer — one BufferGeometry, one colour attribute, two truths. The
// largest instance wins (so the headline "max deviation" stays an upper bound
// over every copy on screen) and the summary carries `mixedScale` so the legend
// can say the picture is of one copy.
//
// ── how it is paid for ───────────────────────────────────────────────────────
//
// Nothing here runs until the reviewer asks. The first toggle then does three
// things, ALL of them in slices off idle callbacks — build the spatial index
// (closest-point.ts), measure every vertex against it (deviation.ts), and turn
// the measurements into colours. Any one of the three left whole would freeze
// the view for several frames on a real mesh, and the frozen one would be
// whichever was overlooked. The results of all three are cached per geometry
// pair, so switching the heatmap off and back on, or leaving overlay and
// returning, costs one pointer swap per painted mesh.

import { BufferAttribute, Matrix4, Vector3 } from "three";
import type { BufferGeometry, Color, Material, Object3D } from "three";
import type { GeometryChange } from "./diff-map.js";
import { buildSurfaceIndexChunked } from "./closest-point.js";
import {
  CHUNK_VERTICES,
  deviationChunked,
  idleYield,
  type DeviationResult,
  type Yielder,
} from "./deviation.js";
import { rampLinear } from "./ramp.js";
import { meshesIn, objectsByNodeIndex } from "./associations.js";
import { resolveMeshNodes, resolveNodeIndex } from "./node-index.js";
import type { LoadedSide } from "./model-overlay.js";

/**
 * Whether the heatmap can honestly be offered at all.
 *
 * Both conditions are about having something to measure, and failing either
 * means the toggle is ABSENT rather than present-and-disabled. A control that is
 * there but refuses is a puzzle: the reviewer cannot tell "this file has no
 * geometry edits" from "this view is broken", and the first is ordinary. The
 * banners #56 built already say when the previous version didn't load, so the
 * missing toggle has an explanation on screen without adding one of its own.
 */
export function heatmapOffered(input: { geometryChanges: number; baseResident: boolean }): boolean {
  return input.geometryChanges > 0 && input.baseResident;
}

/** The numbers the legend and the queue's panel read. */
export type HeatmapSummary = {
  /**
   * Smallest and largest deviation across every measured mesh, in the scene's
   * metres — the node chain's scale is already divided out, so these are
   * numbers a reviewer can quote.
   */
  min: number;
  max: number;
  /** Mesh-change path → that mesh's largest deviation, for its panel row. */
  byPath: Map<string, number>;
  /**
   * At least one measured mesh is drawn at two different scales, so no single
   * set of numbers describes every copy on screen. The values are the largest
   * copy's; the legend says so rather than letting the picture pass for exact.
   */
  mixedScale: boolean;
};

export type Heatmap = {
  /** Head geometries this will colour. Never zero — createHeatmap returns null. */
  readonly meshes: number;
  readonly on: boolean;
  /**
   * Measure (first call only) and paint. Resolves to the summary, or null if the
   * reviewer switched it off again before the measurement finished.
   */
  enable(): Promise<HeatmapSummary | null>;
  /**
   * Put the overlay's own materials back AND take the ramp off the file's
   * geometry — both, because a `color` attribute left behind goes on drawing
   * through any material with `vertexColors` set, which GLTFLoader sets for
   * every primitive that ships COLOR_0. Keeps the measurements and the ramp
   * itself cached, so coming back on is a pointer swap.
   */
  disable(): void;
  /** The summary, once measured; null before that. */
  summary(): HeatmapSummary | null;
  /** The objects a hover raycast should be aimed at. */
  targets(): Object3D[];
  /**
   * The reading under a raycast hit: the mesh's name and the deviation AT THE
   * HIT POINT — the face's three corner measurements blended by that point's
   * barycentric coordinates, which is the same blend the shader interpolates the
   * ramp with, so the number and the colour under the pointer always agree. See
   * `faceValue` for what happens without a point. Null for anything not
   * heatmapped.
   */
  readAt(
    object: Object3D,
    face: { a: number; b: number; c: number } | null | undefined,
    /** Where on the face, in WORLD space — three's `Intersection.point`. */
    point?: { x: number; y: number; z: number } | null,
  ): { label: string; value: number } | null;
  dispose(): { materials: number; attributes: number };
};

export type HeatmapInput = {
  head: LoadedSide;
  /** The previous version. Null is one of the two gates — no base, no heatmap. */
  base: LoadedSide | null;
  /** Meshes the diff reports a vertex-data edit on (diff-map.ts). */
  geometry: readonly GeometryChange[];
  /** Overridden by tests to run the slices synchronously. */
  yieldTo?: Yielder;
};

/** One head geometry and the base geometry it is measured against. */
type Pair = {
  geometry: BufferGeometry;
  baseGeometry: BufferGeometry;
  /** Head meshes drawing this geometry — instances share it, so often several. */
  objects: Object3D[];
  /**
   * Geometry space → scene space, linear part only, taken from the largest
   * instance. Both sides go through it, so the measurement comes out in the
   * scene's metres with the node's own motion divided out.
   */
  linear: Matrix4;
  /** Instances of this geometry disagree about that transform's scale. */
  mixedScale: boolean;
  /** The mesh change's path (the queue's key) and display name. */
  path: string;
  label: string;
};

/** What a painted mesh wore before the heatmap, and what the heatmap put on. */
type Swap = { before: Material | Material[]; heat: Material | Material[] };

/**
 * Build the heatmap for a mount, or null when it cannot be offered — no
 * geometry change, no previous version, or nothing in the two files that pairs
 * up. The caller shows the toggle exactly when this is non-null.
 */
export function createHeatmap(input: HeatmapInput): Heatmap | null {
  const { head, base } = input;
  // The `!base` half is the type narrowing, not a second rule — `heatmapOffered`
  // has already refused a mount without a previous version.
  if (!heatmapOffered({ geometryChanges: input.geometry.length, baseResident: base !== null }) || !base) {
    return null;
  }

  const headObjects = objectsByNodeIndex(head.gltf);
  const baseObjects = objectsByNodeIndex(base.gltf);
  const pairs: Pair[] = [];
  const seenGeometry = new Set<string>();

  for (const change of input.geometry) {
    for (const headNode of resolveMeshNodes(head.index, change.name)) {
      // Pair through the NODE, not the mesh: mesh keys are disambiguated per
      // file (`Wheel`, `Wheel#1`), so a mesh added upstream can shift which key
      // means which geometry, while the node carrying it keeps its name.
      const nodeName = head.index.keyByIndex[headNode];
      if (nodeName === undefined) continue;
      const baseNode = resolveNodeIndex(base.index, nodeName).index;
      if (baseNode === null) continue;

      const headRoot = headObjects.get(headNode)?.[0];
      const baseRoot = baseObjects.get(baseNode)?.[0];
      if (!headRoot || !baseRoot) continue;
      // GLTFLoader emits one Mesh per primitive in primitive order, so the
      // diff's ordinal indexes straight into each side's list.
      const headPrimitives = meshesIn(headRoot);
      const basePrimitives = meshesIn(baseRoot);
      for (const ordinal of change.primitives) {
        const headMesh = headPrimitives[ordinal];
        const baseMesh = basePrimitives[ordinal];
        if (!headMesh || !baseMesh) continue; // a primitive one side doesn't have
        const geometry = geometryOf(headMesh);
        const baseGeometry = geometryOf(baseMesh);
        if (!geometry || !baseGeometry) continue;
        if (seenGeometry.has(geometry.uuid)) continue;
        seenGeometry.add(geometry.uuid);
        // Every node instancing this mesh draws the same measured geometry, so
        // all of them are painted — highlighting one of four wheels would be a
        // lie about where the change is.
        const objects = allInstancesOf(geometry, headObjects, change, head, ordinal);
        const scaling = instanceScaling(objects.length > 0 ? objects : [headMesh], head.gltf.scene);
        pairs.push({
          geometry,
          baseGeometry,
          objects,
          linear: scaling.linear,
          mixedScale: scaling.mixed,
          path: change.path,
          label: change.name,
        });
      }
    }
  }

  if (pairs.length === 0) return null;

  const yieldTo = input.yieldTo ?? idleYield;
  const cache = new Map<string, DeviationResult>();
  /** Head geometry uuid → its measurements, for the hover readout. */
  const byGeometry = new Map<string, DeviationResult>();
  const swaps = new Map<Object3D, Swap>();
  const heatMaterials = new Set<Material>();
  /** Geometry → the `color` attribute it had before, while the ramp is on it. */
  const displacedColor = new Map<BufferGeometry, BufferAttribute | null>();
  /**
   * Geometry → the ramp attribute written for it, kept across an off/on.
   *
   * Held rather than rebuilt because the colour pass is a real cost (~54 ms for
   * 100k vertices) and the promise the toggle makes is that switching back on is
   * a pointer swap. The measurements it was computed from are cached beside it,
   * so the two never disagree.
   */
  const heatColor = new Map<BufferGeometry, BufferAttribute>();
  const labelByGeometry = new Map<string, string>();
  for (const pair of pairs) labelByGeometry.set(pair.geometry.uuid, pair.label);

  let painted = false;
  let computed: HeatmapSummary | null = null;
  let running: Promise<HeatmapSummary | null> | null = null;
  let signal: { cancelled: boolean } | null = null;

  const measureAll = async (): Promise<HeatmapSummary | null> => {
    const local = { cancelled: false };
    signal = local;
    const results: DeviationResult[] = [];
    for (const pair of pairs) {
      const key = `${pair.geometry.uuid}|${pair.baseGeometry.uuid}`;
      let result = cache.get(key);
      if (!result) {
        // The head node's linear part on BOTH sides: the previous shape is put
        // where the current pose would carry it, and the whole comparison lands
        // in scene units. See the note at the top of the file.
        const basePositions = readPositions(pair.baseGeometry, pair.linear);
        const headPositions = readPositions(pair.geometry, pair.linear);
        if (!basePositions || !headPositions) continue;
        const surface = await buildSurfaceIndexChunked(
          basePositions,
          readIndex(pair.baseGeometry),
          yieldTo,
          local,
        );
        if (surface === null) return null;
        const measured = await deviationChunked(headPositions, surface, yieldTo, local);
        if (measured === null) return null;
        result = measured;
        cache.set(key, result);
      }
      byGeometry.set(pair.geometry.uuid, result);
      results.push(result);
    }
    if (local.cancelled) return null;
    if (results.length === 0) return null;

    let min = Infinity;
    let max = 0;
    for (const r of results) {
      if (r.min < min) min = r.min;
      if (r.max > max) max = r.max;
    }
    const byPath = new Map<string, number>();
    for (const pair of pairs) {
      const r = byGeometry.get(pair.geometry.uuid);
      if (!r) continue;
      byPath.set(pair.path, Math.max(byPath.get(pair.path) ?? 0, r.max));
    }
    const mixedScale = pairs.some((p) => p.mixedScale && byGeometry.has(p.geometry.uuid));
    return { min: Number.isFinite(min) ? min : 0, max, byPath, mixedScale };
  };

  /**
   * The ramp for one mesh's measurements, built in slices.
   *
   * Sliced for the same reason the measurement is: `rampLinear` is three
   * `Math.pow` calls per vertex, so a 100k-vertex mesh is ~54 ms of unbroken
   * arithmetic — three dropped frames arriving right after the reviewer has just
   * waited for the measurement, which is the worst possible moment to stutter.
   *
   * Nothing is written to the geometry until the whole array is built: a paint
   * abandoned half way must leave the model exactly as it found it, not with a
   * partial ramp on some of its vertices.
   */
  const buildColors = async (
    result: DeviationResult,
    scale: number,
    local: { cancelled: boolean },
  ): Promise<Float32Array | null> => {
    const count = result.values.length;
    const colors = new Float32Array(count * 3);
    for (let from = 0; from < count; from += CHUNK_VERTICES) {
      if (local.cancelled) return null;
      const to = Math.min(from + CHUNK_VERTICES, count);
      for (let i = from; i < to; i++) {
        const c = rampLinear(result.values[i]! * scale);
        colors[i * 3] = c.r;
        colors[i * 3 + 1] = c.g;
        colors[i * 3 + 2] = c.b;
      }
      if (to < count) await yieldTo();
    }
    return local.cancelled ? null : colors;
  };

  /**
   * Write the ramp into each geometry's `color` attribute and swap in a material
   * that reads it. False when the reviewer cancelled part way through.
   *
   * Normalised against the range of the WHOLE view, not per mesh. Per-mesh
   * normalisation would give an untouched-looking 0.1 mm ripple the same full
   * ramp as a 12 mm sculpt sitting next to it, and the two would be
   * indistinguishable in the only picture that is supposed to tell them apart.
   *
   * Normalised against 0..max and not min..max, so the foot of the ramp is
   * deviation *zero* — the sequential ramp was chosen for having a meaningful
   * zero (ramp.ts), and stretching min..max across it would paint a surface that
   * moved uniformly by 100 mm as a full-range rainbow of its own float noise.
   * legend.ts labels the foot to match; the two must be read together.
   */
  const paint = async (summary: HeatmapSummary, local: { cancelled: boolean }): Promise<boolean> => {
    const scale = summary.max > 0 ? 1 / summary.max : 0;
    for (const pair of pairs) {
      const result = byGeometry.get(pair.geometry.uuid);
      if (!result) continue;
      let ramp = heatColor.get(pair.geometry);
      if (!ramp) {
        const colors = await buildColors(result, scale, local);
        if (colors === null) return false;
        ramp = new BufferAttribute(colors, 3);
        heatColor.set(pair.geometry, ramp);
      }
      if (local.cancelled) return false;
      // Re-read what the geometry is wearing on every paint, not once for the
      // heatmap's lifetime: disable() puts the file's own attribute back, so
      // what has to be restored next time is whatever is there now.
      if (!displacedColor.has(pair.geometry)) {
        const existing = pair.geometry.getAttribute("color");
        displacedColor.set(pair.geometry, (existing as BufferAttribute | undefined) ?? null);
      }
      pair.geometry.setAttribute("color", ramp);
      for (const object of pair.objects) {
        const holder = object as { material?: Material | Material[] };
        const current = holder.material;
        if (!current) continue;
        let swap = swaps.get(object);
        if (!swap) {
          const heat = Array.isArray(current)
            ? current.map((m) => heatMaterial(m, heatMaterials))
            : heatMaterial(current, heatMaterials);
          swap = { before: current, heat };
          swaps.set(object, swap);
        }
        holder.material = swap.heat;
      }
    }
    painted = true;
    return true;
  };

  /** The overlay's materials and the file's own `color` attribute, back on. */
  const unpaint = (): number => {
    for (const [object, swap] of swaps) {
      (object as { material?: Material | Material[] }).material = swap.before;
    }
    // The `color` attribute lives on the FILE's geometry, which outlives both
    // this toggle and this heatmap. GLTFLoader sets `vertexColors` on the
    // material of any primitive carrying COLOR_0, so a model that ships its own
    // vertex colours would go on drawing in the ramp — in structural mode, in
    // side-by-side, everywhere — if the attribute were left behind with the
    // authored material back on top of it.
    let attributes = 0;
    for (const [geometry, previous] of displacedColor) {
      if (previous) geometry.setAttribute("color", previous);
      else geometry.deleteAttribute("color");
      attributes++;
    }
    displacedColor.clear();
    painted = false;
    return attributes;
  };

  return {
    meshes: pairs.length,
    get on(): boolean {
      return painted;
    },
    async enable(): Promise<HeatmapSummary | null> {
      if (computed) {
        if (painted) return computed;
        const local = { cancelled: false };
        signal = local;
        return (await paint(computed, local)) ? computed : null;
      }
      // Join a measurement already in flight — two toggles in quick succession
      // must not race two runs onto the same cache — but only a LIVE one. A run
      // the reviewer cancelled resolves to null, and awaiting that for a toggle
      // they have since switched back on would report "no measurement" and leave
      // the model unpainted until they toggled a third time.
      if (running === null || signal === null || signal.cancelled) running = measureAll();
      const mine = running;
      const summary = await mine;
      if (running === mine) running = null;
      if (summary === null) return null;
      // Kept even if the paint below is abandoned: the measurement is a fact
      // about the two files, and re-measuring for the next toggle would charge
      // the reviewer twice for it.
      computed = summary;
      const local = signal;
      if (local === null || local.cancelled) return null;
      return (await paint(summary, local)) ? summary : null;
    },
    disable(): void {
      if (signal) signal.cancelled = true;
      unpaint();
    },
    summary(): HeatmapSummary | null {
      return computed;
    },
    targets(): Object3D[] {
      return pairs.flatMap((p) => p.objects);
    },
    readAt(object, face, point): { label: string; value: number } | null {
      const geometry = geometryOf(object);
      if (!geometry) return null;
      const result = byGeometry.get(geometry.uuid);
      const label = labelByGeometry.get(geometry.uuid);
      if (!result || label === undefined) return null;
      // No face (a Points/Line hit, or a hit reported without one) still has a
      // useful answer: the mesh's own maximum, which is the number its panel row
      // carries. Better than blanking the readout as the pointer crosses a seam.
      if (!face) return { label, value: result.max };
      const corners: readonly [number, number, number] = [
        result.values[face.a] ?? 0,
        result.values[face.b] ?? 0,
        result.values[face.c] ?? 0,
      ];
      return { label, value: faceValue(object, geometry, face, corners, point) };
    },
    dispose(): { materials: number; attributes: number } {
      if (signal) signal.cancelled = true;
      // Materials and attributes off first: with the heat clones off the model,
      // freeing them cannot leave a Mesh pointing at a disposed material for a
      // frame. `attributes` counts what THIS call put back, so a teardown that
      // follows a disable() reports zero — disable() already did it.
      const attributes = unpaint();
      for (const material of heatMaterials) material.dispose();
      const materials = heatMaterials.size;
      heatMaterials.clear();
      swaps.clear();
      heatColor.clear();
      byGeometry.clear();
      cache.clear();
      return { materials, attributes };
    },
  };
}

/**
 * A clone of `source` that shows the ramp and nothing else.
 *
 * Vertex colours *multiply* the material's own colour and its base-colour map in
 * three's standard shader, so a clone that kept either would show the ramp
 * modulated by the model's paint job — a dark blue panel and a bright one would
 * read as different deviations at the same distance. White, unmapped and
 * un-emissive is what makes the legend's scale mean the same thing everywhere on
 * the model. Lighting is kept: a flat unlit shell loses the shape the deviation
 * is *on*, and the metrology tools this borrows from all keep it.
 */
function heatMaterial(source: Material, owned: Set<Material>): Material {
  const clone = source.clone();
  const m = clone as unknown as {
    vertexColors: boolean;
    color?: Color;
    map?: unknown;
    emissive?: Color;
    emissiveIntensity?: number;
    metalness?: number;
    roughness?: number;
    transparent?: boolean;
    opacity?: number;
  };
  m.vertexColors = true;
  m.color?.setHex(0xffffff);
  if ("map" in m) m.map = null;
  m.emissive?.setHex(0x000000);
  if (typeof m.emissiveIntensity === "number") m.emissiveIntensity = 0;
  if (typeof m.metalness === "number") m.metalness = 0.02;
  if (typeof m.roughness === "number") m.roughness = 0.85;
  // Opaque, whatever the source was: a translucent heatmap blends the ramp with
  // whatever is behind it, and the resulting colour is a reading of nothing.
  m.transparent = false;
  m.opacity = 1;
  clone.needsUpdate = true;
  owned.add(clone);
  return clone;
}

const geometryOf = (object: Object3D): BufferGeometry | null =>
  (object as { geometry?: BufferGeometry }).geometry ?? null;

/**
 * A POSITION accessor, read one component at a time and never off `.array` —
 * see `readPositions` for the interleaving trap that makes that mandatory.
 */
type PositionAttribute = {
  count: number;
  getX(i: number): number;
  getY(i: number): number;
  getZ(i: number): number;
};

const positionsOf = (geometry: BufferGeometry): PositionAttribute | undefined =>
  geometry.getAttribute("position") as PositionAttribute | undefined;

/**
 * The deviation at the point on the face that was actually hit.
 *
 * The ramp is a per-vertex attribute, so the colour a reviewer sees at a pixel
 * is the face's three corner measurements blended by that pixel's barycentric
 * coordinates. The number printed beside the picture is that same blend, because
 * anything else disagrees with the shading it is standing next to — and on the
 * coarse box-and-extrusion geometry that most CAD glTF is made of, disagrees by
 * the full spread of a triangle. (The demo's own car body has side panels whose
 * lower corners did not move and whose upper corners moved 12 mm: reporting the
 * face's largest corner, as this once did, printed "12 mm" everywhere on a panel
 * the heatmap paints at the dead foot of the ramp.)
 *
 * Barycentric coordinates are affine-invariant, so they are computed in the
 * geometry's own space — the world-space hit point brought back through the
 * object's world matrix — and neither the node chain's scale nor which instance
 * of a shared geometry was hit can change them.
 *
 * Without a point, and on a degenerate face where the coordinates are 0/0, the
 * fallback is the face's MEAN rather than any one corner: the pointer is
 * somewhere in the triangle, and the middle of it is wrong by at most half the
 * face's spread where a corner can be wrong by all of it.
 */
function faceValue(
  object: Object3D,
  geometry: BufferGeometry,
  face: { a: number; b: number; c: number },
  corners: readonly [number, number, number],
  point: { x: number; y: number; z: number } | null | undefined,
): number {
  const mean = (corners[0] + corners[1] + corners[2]) / 3;
  const position = positionsOf(geometry);
  if (!point || !position) return mean;
  // The reading must not depend on where in the frame it was asked for, so the
  // chain is brought up to date here rather than trusted to have been walked
  // already — it is one hover per frame, against a render that does the same
  // work for every object on screen.
  object.updateWorldMatrix(true, false);
  const local = new Vector3(point.x, point.y, point.z).applyMatrix4(
    new Matrix4().copy(object.matrixWorld).invert(),
  );

  const ax = position.getX(face.a);
  const ay = position.getY(face.a);
  const az = position.getZ(face.a);
  const v0x = position.getX(face.b) - ax;
  const v0y = position.getY(face.b) - ay;
  const v0z = position.getZ(face.b) - az;
  const v1x = position.getX(face.c) - ax;
  const v1y = position.getY(face.c) - ay;
  const v1z = position.getZ(face.c) - az;
  const v2x = local.x - ax;
  const v2y = local.y - ay;
  const v2z = local.z - az;
  const d00 = v0x * v0x + v0y * v0y + v0z * v0z;
  const d01 = v0x * v1x + v0y * v1y + v0z * v1z;
  const d11 = v1x * v1x + v1y * v1y + v1z * v1z;
  const d20 = v2x * v0x + v2y * v0y + v2z * v0z;
  const d21 = v2x * v1x + v2y * v1y + v2z * v1z;
  const denom = d00 * d11 - d01 * d01;
  if (denom === 0) return mean; // a zero-area face has no interior to place it in

  // Clamped and renormalised: the hit lies on the face, but float error at an
  // edge — or a `point` a caller measured slightly off the plane — can push a
  // coordinate past its end, and the reading would then leave the range the
  // legend's two labels promise.
  const b = clamp01((d11 * d20 - d01 * d21) / denom);
  const c = clamp01((d00 * d21 - d01 * d20) / denom);
  const a = clamp01(1 - b - c);
  const total = a + b + c;
  if (!(total > 0) || !Number.isFinite(total)) return mean;
  return (a * corners[0] + b * corners[1] + c * corners[2]) / total;
}

const clamp01 = (v: number): number => (v < 0 ? 0 : v > 1 ? 1 : v);

/**
 * Every head Mesh drawing this geometry, across every node instancing the mesh.
 * Found by geometry identity rather than by re-resolving names — GLTFLoader
 * shares one BufferGeometry between a mesh's instances, so identity is exactly
 * the question being asked and needs no second name lookup to answer.
 */
function allInstancesOf(
  geometry: BufferGeometry,
  headObjects: Map<number, Object3D[]>,
  change: GeometryChange,
  head: LoadedSide,
  ordinal: number,
): Object3D[] {
  const out: Object3D[] = [];
  for (const nodeIndex of resolveMeshNodes(head.index, change.name)) {
    for (const root of headObjects.get(nodeIndex) ?? []) {
      const mesh = meshesIn(root)[ordinal];
      if (mesh && geometryOf(mesh) === geometry) out.push(mesh);
    }
  }
  return out;
}

/**
 * A geometry's POSITION data as a plain 3-per-vertex array, mapped through
 * `linear` — the node chain's 3×3, which is what turns local coordinates into
 * the scene's metres.
 *
 * Read through `getX/getY/getZ` rather than off `.array`, because GLTFLoader
 * hands back an `InterleavedBufferAttribute` for an interleaved accessor — and
 * its `.array` is the whole interleaved buffer, normals and UVs included.
 * Walking that as if it were positions produces a mesh made of garbage, silently.
 */
function readPositions(geometry: BufferGeometry, linear: Matrix4): Float32Array | null {
  const attribute = positionsOf(geometry);
  if (!attribute || attribute.count === 0) return null;
  const e = linear.elements;
  const out = new Float32Array(attribute.count * 3);
  for (let i = 0; i < attribute.count; i++) {
    const x = attribute.getX(i);
    const y = attribute.getY(i);
    const z = attribute.getZ(i);
    out[i * 3] = e[0]! * x + e[4]! * y + e[8]! * z;
    out[i * 3 + 1] = e[1]! * x + e[5]! * y + e[9]! * z;
    out[i * 3 + 2] = e[2]! * x + e[6]! * y + e[10]! * z;
  }
  return out;
}

/**
 * The transform from an object's own space to the model's scene root, linear
 * part only.
 *
 * Composed from the chain's LOCAL matrices rather than read off `matrixWorld`,
 * because the loaded scene hangs inside the viewer's own groups: whatever those
 * do to place or frame the model is a property of the view, and only the node
 * transforms the glTF file actually describes belong in a number that is going
 * to be labelled in millimetres.
 */
function modelLinear(object: Object3D, root: Object3D): Matrix4 {
  const chain: Object3D[] = [];
  for (let node: Object3D | null = object; node !== null && node !== root; node = node.parent) {
    chain.push(node);
  }
  const out = new Matrix4();
  for (let i = chain.length - 1; i >= 0; i--) {
    const node = chain[i]!;
    if (node.matrixAutoUpdate) node.updateMatrix();
    out.multiply(node.matrix);
  }
  return out;
}

/**
 * Which instance's transform the shared geometry is measured at, and whether the
 * instances disagree.
 *
 * Compared by the metric Lᵀ·L rather than by the matrices themselves: distances
 * are what this file measures, and they are unchanged by rotation and
 * reflection. Four wheels at four orientations measure identically and must not
 * raise the flag; four wheels at four *sizes* do not, and must.
 *
 * The largest instance wins so that "max deviation" stays an upper bound over
 * every copy on screen — an understated headline number is the failure mode that
 * actually costs a review something.
 */
function instanceScaling(objects: Object3D[], root: Object3D): { linear: Matrix4; mixed: boolean } {
  let linear = new Matrix4();
  let best = -Infinity;
  let mixed = false;
  let first: readonly number[] | null = null;
  for (const object of objects) {
    const candidate = modelLinear(object, root);
    const m = metric(candidate);
    if (first === null) first = m;
    else if (!sameMetric(first, m)) mixed = true;
    // Trace of the metric — the sum of the squared column lengths, so "biggest
    // overall" even when the scale is non-uniform.
    const size = m[0]! + m[1]! + m[2]!;
    if (size > best) {
      best = size;
      linear = candidate;
    }
  }
  return { linear, mixed };
}

/** Lᵀ·L as [xx, yy, zz, xy, xz, yz] — the part of a transform distances feel. */
function metric(linear: Matrix4): readonly number[] {
  const e = linear.elements;
  const c: readonly (readonly [number, number, number])[] = [
    [e[0]!, e[1]!, e[2]!],
    [e[4]!, e[5]!, e[6]!],
    [e[8]!, e[9]!, e[10]!],
  ];
  const dot = (a: number, b: number): number =>
    c[a]![0] * c[b]![0] + c[a]![1] * c[b]![1] + c[a]![2] * c[b]![2];
  return [dot(0, 0), dot(1, 1), dot(2, 2), dot(0, 1), dot(0, 2), dot(1, 2)];
}

/** Relative, because a millimetre-authored model's metrics are ~1e-6 apart. */
function sameMetric(a: readonly number[], b: readonly number[]): boolean {
  const magnitude = Math.max(Math.abs(a[0]!) + Math.abs(a[1]!) + Math.abs(a[2]!), 1e-30);
  for (let i = 0; i < 6; i++) {
    if (Math.abs(a[i]! - b[i]!) > 1e-6 * magnitude) return false;
  }
  return true;
}

/** A geometry's index buffer, or null for a non-indexed primitive. */
function readIndex(geometry: BufferGeometry): ArrayLike<number> | null {
  const index = geometry.getIndex();
  return index ? (index.array as ArrayLike<number>) : null;
}
