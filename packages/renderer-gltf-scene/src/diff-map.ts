// StructuredDiff → per-node change facts, keyed by the node's name *as the
// handler wrote it*. `slugify` is retired: lowercasing and hyphenating a name
// before matching it against another layer's differently-mangled name is how a
// change to "Cube.001" ends up unpainted. Names are reconciled once, in
// node-index.ts, and only against the file's own JSON.
//
// Path scheme (see handler-gltf-scene): segments are "/"-separated and fully
// qualified — "nodes/Cube.001/translation" — with "%" and "/" percent-escaped
// inside segments ("%25", "%2F"). `label` always carries the raw display name;
// the path is the escaped machine key.
//
// What this module deliberately does *not* read: the `before`/`after` values.
// They are display strings in Blender coordinate space ("[1 2 -3]"), so deriving
// a motion vector from them would mean string-parsing plus a coordinate-space
// round trip. The renderer has both files loaded, so it takes old and new
// transforms from the two scene graphs instead — exact, and immune to changes in
// how the handler formats values.

import type { StructuredDiff, DiffChange, ChangeKind } from "@fhr/types";
import { unescapeSegment } from "./change-path.js";

/** The glTF collection this renderer paints. */
const NODES = "nodes";
const NODE_PREFIX = `${NODES}/`;

/** A node-level path: exactly one (escaped) segment below the collection. */
const NODE_PATH = /^nodes\/[^/]+$/;

/** Primitive ordinals named anywhere under an entity change. */
const PRIMITIVE_SEGMENT = /\/primitives\/(\d+)(?:\/|$)/;

/** Node fields that move geometry in space, as the handler labels them. */
export const TRANSFORM_FIELDS: readonly string[] = ["translation", "rotation", "scale"];

export type NodeChange = {
  /** The node's name as the diff names it (handler's `name || node[i]`). */
  name: string;
  kind: ChangeKind;
  /** Changed field labels on this node, e.g. ["translation", "mesh"]. */
  fields: string[];
  /**
   * The change's fully-qualified path — the selection key the host and the change
   * tree use for this node (#45). Carried here because this is the one place that
   * has seen both the name and the path the handler paired it with.
   */
  path: string;
};

/**
 * Per-node change kinds, keyed by node name. Kept for the scene-graph outline
 * view and for callers that only need the colour; `nodeChanges` carries detail.
 */
export function diffChangeTypes(diff: StructuredDiff | undefined): Map<string, ChangeKind> {
  const acc = new Map<string, ChangeKind>();
  for (const change of nodeChanges(diff)) acc.set(change.name, change.kind);
  return acc;
}

/**
 * Node-level changes, in diff order. Recognises both shapes the handler may
 * emit: node changes nested under a "nodes" collection change (today), and node
 * changes appearing directly with a "nodes/<name>" path.
 *
 * Names are taken from `label` first and only then from the unescaped path
 * remainder — a node-level path is exactly one segment below the collection, so
 * a fully-qualified field path ("nodes/Cube/translation") never reads as a node.
 */
export function nodeChanges(diff: StructuredDiff | undefined): NodeChange[] {
  const out: NodeChange[] = [];
  const seen = new Map<string, NodeChange>();
  if (!diff) return out;

  const collect = (change: DiffChange): void => {
    const name = nodeNameOf(change);
    if (name === "") return;
    const fields = (change.children ?? []).map(fieldNameOf).filter((f) => f !== "");
    const existing = seen.get(name);
    if (existing) {
      // Same node named twice in one diff: keep the first kind, union the fields.
      for (const f of fields) if (!existing.fields.includes(f)) existing.fields.push(f);
      return;
    }
    const entry: NodeChange = { name, kind: change.kind, fields, path: change.path };
    seen.set(name, entry);
    out.push(entry);
  };

  const walk = (changes: DiffChange[]): void => {
    for (const change of changes) {
      if (change.path === NODES || change.path.endsWith(`/${NODES}`)) {
        // The collection wrapper: its children are the node-level changes.
        for (const child of change.children ?? []) collect(child);
        continue;
      }
      if (NODE_PATH.test(change.path)) {
        collect(change);
        continue;
      }
      if (change.children?.length) walk(change.children);
    }
  };

  // A nil Go slice marshals to JSON null, so `changes` may be null over the wire.
  walk(diff.changes ?? []);
  return out;
}

/**
 * A change against a collection other than `nodes` — a mesh or a material.
 *
 * These reach the screen indirectly. A mesh is drawn once per node instancing it;
 * a material is drawn once per primitive referencing it. Resolving either to
 * something paintable is `node-index.ts`'s job, so all this carries is the key to
 * resolve and which primitives, if any, the change narrowed itself to.
 */
export type EntityChange = {
  /** The entity's key as the diff names it (handler's `uniqueKeys` output). */
  name: string;
  kind: ChangeKind;
  fields: string[];
  path: string;
  /**
   * Primitive ordinals named under this change. Empty means the change wasn't
   * specific about one, so it applies to the whole entity — the difference
   * between "primitive 2's material was reassigned" and "this mesh changed".
   */
  primitives: number[];
};

/** Changes against the `meshes` collection, in diff order. */
export function meshChanges(diff: StructuredDiff | undefined): EntityChange[] {
  return collectionChanges(diff, "meshes");
}

/** Changes against the `materials` collection, in diff order. */
export function materialChanges(diff: StructuredDiff | undefined): EntityChange[] {
  return collectionChanges(diff, "materials");
}

/**
 * Changes against `animations`. Nothing paints these — an animation has no single
 * resting place on a static model — but they still have to be *counted*, so the
 * view can say it isn't showing them instead of looking like an unchanged file.
 */
export function animationChanges(diff: StructuredDiff | undefined): EntityChange[] {
  return collectionChanges(diff, "animations");
}

/**
 * Visit every entity-level change of one collection, in diff order, handing the
 * visitor the raw `DiffChange` and the entity key it resolved to.
 *
 * Two shapes again: children of a collection wrapper, or a change appearing
 * directly with a `<collection>/<key>` path. Shared rather than repeated because
 * the geometry scan below has to walk the same tree looking for different rows,
 * and a second copy of this would be a second place for the two shapes to drift.
 */
function forEachEntityChange(
  diff: StructuredDiff | undefined,
  collection: string,
  visit: (change: DiffChange, name: string) => void,
): void {
  if (!diff) return;
  const prefix = `${collection}/`;
  const entityPath = new RegExp(`^${collection}/[^/]+$`);

  const collect = (change: DiffChange): void => {
    const name =
      change.label !== undefined && change.label !== ""
        ? change.label
        : entityPath.test(change.path)
          ? unescapeSegment(change.path.slice(prefix.length))
          : "";
    if (name !== "") visit(change, name);
  };

  const walk = (changes: DiffChange[]): void => {
    for (const change of changes) {
      if (change.path === collection || change.path.endsWith(`/${collection}`)) {
        for (const child of change.children ?? []) collect(child);
        continue;
      }
      if (entityPath.test(change.path)) {
        collect(change);
        continue;
      }
      if (change.children?.length) walk(change.children);
    }
  };

  walk(diff.changes ?? []);
}

function collectionChanges(diff: StructuredDiff | undefined, collection: string): EntityChange[] {
  const out: EntityChange[] = [];
  const seen = new Map<string, EntityChange>();
  if (!diff) return out;

  const primitivesUnder = (change: DiffChange): number[] => {
    const found = new Set<number>();
    const walkOne = (c: DiffChange): void => {
      const match = PRIMITIVE_SEGMENT.exec(c.path);
      if (match) found.add(Number(match[1]));
      for (const child of c.children ?? []) walkOne(child);
    };
    walkOne(change);
    return [...found].sort((a, b) => a - b);
  };

  forEachEntityChange(diff, collection, (change, name) => {
    const fields = (change.children ?? []).map(fieldNameOf).filter((f) => f !== "");
    const primitives = primitivesUnder(change);
    const existing = seen.get(name);
    if (existing) {
      for (const f of fields) if (!existing.fields.includes(f)) existing.fields.push(f);
      for (const p of primitives) if (!existing.primitives.includes(p)) existing.primitives.push(p);
      existing.primitives.sort((a, b) => a - b);
      return;
    }
    const entry: EntityChange = { name, kind: change.kind, fields, path: change.path, primitives };
    seen.set(name, entry);
    out.push(entry);
  });
  return out;
}

/**
 * The rows that mean "this primitive's vertex data actually changed", as the
 * handler writes them (handler-gltf-scene `diffPrimitive`):
 *
 *   .../primitives/N/geometry/POSITION   the accessor bytes differ
 *   .../primitives/N/bounds              the POSITION min/max box changed
 *   .../primitives/N/centroid            the decoded centroid moved
 *
 * Deliberately NOT every mesh change. A primitive whose only change is
 * `material` — a reassignment to a different existing material — has identical
 * geometry on both sides, and measuring it would produce a heatmap that is
 * uniformly zero: a picture that says "nothing moved" attached to a change that
 * is real, which is worse than not offering the picture.
 */
const GEOMETRY_ROW = /\/primitives\/(\d+)\/(?:bounds|centroid|geometry\/POSITION)$/;

/** A mesh whose vertex data changed — what the deviation heatmap can measure. */
export type GeometryChange = {
  /** The mesh's key as the diff names it. */
  name: string;
  kind: ChangeKind;
  /** The mesh change's own path — the queue is keyed on this. */
  path: string;
  /**
   * Primitive ordinals with a geometry row. Never empty: a mesh with no such row
   * isn't a geometry change and doesn't appear here at all. (Contrast
   * `EntityChange.primitives`, where empty means "the whole entity" — a
   * distinction that matters because a heatmap on an untouched primitive would
   * be a flat zero.)
   */
  primitives: number[];
};

/**
 * Meshes the diff reports a vertex-data edit on, in diff order. The gate for the
 * deviation heatmap (#46): no entry here means there is nothing to measure, and
 * the toggle is not offered at all.
 */
export function geometryChanges(diff: StructuredDiff | undefined): GeometryChange[] {
  const out: GeometryChange[] = [];
  const seen = new Map<string, GeometryChange>();

  const primitivesWithGeometry = (change: DiffChange): number[] => {
    const found = new Set<number>();
    const walkOne = (c: DiffChange): void => {
      const match = GEOMETRY_ROW.exec(c.path);
      if (match) found.add(Number(match[1]));
      for (const child of c.children ?? []) walkOne(child);
    };
    walkOne(change);
    return [...found].sort((a, b) => a - b);
  };

  forEachEntityChange(diff, "meshes", (change, name) => {
    const primitives = primitivesWithGeometry(change);
    if (primitives.length === 0) return;
    const existing = seen.get(name);
    if (existing) {
      for (const p of primitives) if (!existing.primitives.includes(p)) existing.primitives.push(p);
      existing.primitives.sort((a, b) => a - b);
      return;
    }
    const entry: GeometryChange = { name, kind: change.kind, path: change.path, primitives };
    seen.set(name, entry);
    out.push(entry);
  });
  return out;
}

/** True when a node moved/rotated/scaled and nothing else about it changed. */
export function isTransformOnly(change: NodeChange): boolean {
  return (
    change.kind === "modified" &&
    change.fields.length > 0 &&
    change.fields.every((f) => TRANSFORM_FIELDS.includes(f))
  );
}

/** True when a node's transform changed, whatever else did too. */
export function hasTransformChange(change: NodeChange): boolean {
  return change.fields.some((f) => TRANSFORM_FIELDS.includes(f));
}

/** A node change's name: its label, else the unescaped path remainder. */
function nodeNameOf(change: DiffChange): string {
  if (change.label !== undefined && change.label !== "") return change.label;
  if (!NODE_PATH.test(change.path)) return "";
  return unescapeSegment(change.path.slice(NODE_PREFIX.length));
}

/** A field change's name: its label, else the unescaped last path segment. */
function fieldNameOf(change: DiffChange): string {
  if (change.label !== undefined && change.label !== "") return change.label;
  const at = change.path.lastIndexOf("/");
  return unescapeSegment(at === -1 ? change.path : change.path.slice(at + 1));
}
