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
// The comparison happens in the MESH's own space, not the world's. That is the
// transform-delta subtraction #46 asks for, arrived at for free: a node that was
// only moved carries an unedited mesh, so it produces no geometry row in the
// diff, is never a heatmap target, and reads as *moved* (the motion vector) —
// which is what it is. Measuring in world space would instead paint a rigid
// translation as a fully deviated surface, the single most misleading thing this
// view could do. It is also the only coherent choice for shared geometry: one
// mesh instanced by four nodes with four different scales has no single world
// answer, but it has exactly one answer about its own vertices.
//
// Units are therefore the model's own, which glTF defines as metres (spec §3.3).
//
// ── how it is paid for ───────────────────────────────────────────────────────
//
// Nothing here runs until the reviewer asks. The first toggle builds the spatial
// index and measures, in slices, off idle callbacks (deviation.ts); the result
// is cached per geometry pair, so switching the heatmap off and back on, or
// leaving overlay and returning, costs one pointer swap per painted mesh.

import { BufferAttribute } from "three";
import type { BufferGeometry, Color, Material, Object3D } from "three";
import type { GeometryChange } from "./diff-map.js";
import { buildSurfaceIndex } from "./closest-point.js";
import { deviationChunked, idleYield, type DeviationResult, type Yielder } from "./deviation.js";
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
  /** Smallest and largest deviation across every measured mesh, in metres. */
  min: number;
  max: number;
  /** Mesh-change path → that mesh's largest deviation, for its panel row. */
  byPath: Map<string, number>;
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
  /** Put the overlay's own materials back. Keeps the measurements cached. */
  disable(): void;
  /** The summary, once measured; null before that. */
  summary(): HeatmapSummary | null;
  /** The objects a hover raycast should be aimed at. */
  targets(): Object3D[];
  /**
   * The reading under a raycast hit: the mesh's name and the deviation at the
   * nearest corner of the face that was hit. Null for anything not heatmapped.
   */
  readAt(object: Object3D, face: { a: number; b: number; c: number } | null | undefined): {
    label: string;
    value: number;
  } | null;
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
        pairs.push({ geometry, baseGeometry, objects, path: change.path, label: change.name });
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
  /** Geometry → the `color` attribute it had before, so disposal can restore it. */
  const displacedColor = new Map<BufferGeometry, BufferAttribute | null>();
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
        const basePositions = readPositions(pair.baseGeometry);
        const headPositions = readPositions(pair.geometry);
        if (!basePositions || !headPositions) continue;
        const surface = buildSurfaceIndex(basePositions, readIndex(pair.baseGeometry));
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
    return { min: Number.isFinite(min) ? min : 0, max, byPath };
  };

  /**
   * Write the ramp into each geometry's `color` attribute and swap in a material
   * that reads it.
   *
   * Normalised against the range of the WHOLE view, not per mesh. Per-mesh
   * normalisation would give an untouched-looking 0.1 mm ripple the same full
   * ramp as a 12 mm sculpt sitting next to it, and the two would be
   * indistinguishable in the only picture that is supposed to tell them apart.
   */
  const paint = (summary: HeatmapSummary): void => {
    const scale = summary.max > 0 ? 1 / summary.max : 0;
    for (const pair of pairs) {
      const result = byGeometry.get(pair.geometry.uuid);
      if (!result) continue;
      if (!displacedColor.has(pair.geometry)) {
        const existing = pair.geometry.getAttribute("color");
        displacedColor.set(pair.geometry, (existing as BufferAttribute | undefined) ?? null);
        const colors = new Float32Array(result.values.length * 3);
        for (let i = 0; i < result.values.length; i++) {
          const c = rampLinear(result.values[i]! * scale);
          colors[i * 3] = c.r;
          colors[i * 3 + 1] = c.g;
          colors[i * 3 + 2] = c.b;
        }
        pair.geometry.setAttribute("color", new BufferAttribute(colors, 3));
      }
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
  };

  return {
    meshes: pairs.length,
    get on(): boolean {
      return painted;
    },
    async enable(): Promise<HeatmapSummary | null> {
      if (computed) {
        if (!painted) paint(computed);
        return computed;
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
      computed = summary;
      paint(summary);
      return summary;
    },
    disable(): void {
      if (signal) signal.cancelled = true;
      if (!painted) return;
      for (const [object, swap] of swaps) {
        (object as { material?: Material | Material[] }).material = swap.before;
      }
      painted = false;
    },
    summary(): HeatmapSummary | null {
      return computed;
    },
    targets(): Object3D[] {
      return pairs.flatMap((p) => p.objects);
    },
    readAt(object, face): { label: string; value: number } | null {
      const geometry = geometryOf(object);
      if (!geometry) return null;
      const result = byGeometry.get(geometry.uuid);
      const label = labelByGeometry.get(geometry.uuid);
      if (!result || label === undefined) return null;
      // No face (a Points/Line hit, or a hit reported without one) still has a
      // useful answer: the mesh's own maximum, which is the number its panel row
      // carries. Better than blanking the readout as the pointer crosses a seam.
      if (!face) return { label, value: result.max };
      const value = Math.max(
        result.values[face.a] ?? 0,
        result.values[face.b] ?? 0,
        result.values[face.c] ?? 0,
      );
      return { label, value };
    },
    dispose(): { materials: number; attributes: number } {
      if (signal) signal.cancelled = true;
      // Materials back first: with the heat clones off the model, freeing them
      // cannot leave a Mesh pointing at a disposed material for a frame.
      for (const [object, swap] of swaps) {
        (object as { material?: Material | Material[] }).material = swap.before;
      }
      painted = false;
      for (const material of heatMaterials) material.dispose();
      const materials = heatMaterials.size;
      heatMaterials.clear();
      swaps.clear();
      // The `color` attribute lives on the FILE's geometry, which outlives this
      // heatmap: leaving it behind would tint the model wherever a later
      // material happened to read vertex colours. A file that shipped its own
      // COLOR_0 gets it back rather than losing it to a view it never opted into.
      let attributes = 0;
      for (const [geometry, previous] of displacedColor) {
        if (previous) geometry.setAttribute("color", previous);
        else geometry.deleteAttribute("color");
        attributes++;
      }
      displacedColor.clear();
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
 * A geometry's POSITION data as a plain 3-per-vertex array.
 *
 * Read through `getX/getY/getZ` rather than off `.array`, because GLTFLoader
 * hands back an `InterleavedBufferAttribute` for an interleaved accessor — and
 * its `.array` is the whole interleaved buffer, normals and UVs included.
 * Walking that as if it were positions produces a mesh made of garbage, silently.
 */
function readPositions(geometry: BufferGeometry): Float32Array | null {
  const attribute = geometry.getAttribute("position") as
    | { count: number; getX(i: number): number; getY(i: number): number; getZ(i: number): number }
    | undefined;
  if (!attribute || attribute.count === 0) return null;
  const out = new Float32Array(attribute.count * 3);
  for (let i = 0; i < attribute.count; i++) {
    out[i * 3] = attribute.getX(i);
    out[i * 3 + 1] = attribute.getY(i);
    out[i * 3 + 2] = attribute.getZ(i);
  }
  return out;
}

/** A geometry's index buffer, or null for a non-indexed primitive. */
function readIndex(geometry: BufferGeometry): ArrayLike<number> | null {
  const index = geometry.getIndex();
  return index ? (index.array as ArrayLike<number>) : null;
}
