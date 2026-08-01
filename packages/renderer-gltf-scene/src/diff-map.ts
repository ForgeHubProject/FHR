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
// What this module deliberately does *not* read: the `before`/`after` values of
// a *measured* change. They are display strings in Blender coordinate space
// ("[1 2 -3]"), so deriving a motion vector from them would mean string-parsing
// plus a coordinate-space round trip. The renderer has both files loaded, so it
// takes old and new transforms from the two scene graphs instead — exact, and
// immune to changes in how the handler formats values.
//
// The one exception is a `renamed` change's `before` (#47), which is not a
// measurement but the node's name in the base file — the only place that name
// exists, and the only key that finds the node in the previous version.

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
  /**
   * For a `renamed` change (#47), the name the *base* file uses for this node.
   * `name` is always the head file's name, so anything that has to find the node
   * in the previous version — the ghost, the motion vector — has to look it up
   * under this one or find nothing at all.
   */
  oldName?: string;
};

/**
 * Per-node change kinds, keyed by node name. Kept for the scene-graph outline
 * view and for callers that only need the colour; `nodeChanges` carries detail.
 *
 * One kind per name, and a name can now carry two changes (see `nodeChanges`).
 * The tie-break is not diff order — a removal may be emitted first or second, and
 * taking the first painted a node that still exists in the removal colour. It is
 * what the view being coloured draws: the outline is built from the HEAD file's
 * scene graph, and a `removed` node is precisely the one that is not in it. So
 * every other kind outranks `removed`, which keeps a name only while nothing
 * about a surviving node claims it.
 */
export function diffChangeTypes(diff: StructuredDiff | undefined): Map<string, ChangeKind> {
  const acc = new Map<string, ChangeKind>();
  for (const change of nodeChanges(diff)) {
    const held = acc.get(change.name);
    if (held === undefined || (held === "removed" && change.kind !== "removed")) {
      acc.set(change.name, change.kind);
    }
  }
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
    // Keyed on the path, not the name. Since #47 a name can legitimately appear
    // twice in one diff and mean two different nodes: the previous version's
    // "Wheel" was deleted while an unrelated node was *renamed to* "Wheel", which
    // is one removal and one rename, each about a different object. Merging them
    // by name kept the first kind and dropped the deletion — the one thing a diff
    // must never lose — and folded the dead node's field labels into the rename,
    // so the overlay drew a move that never happened. The handler guarantees one
    // change per path, which is what makes the path the identity here.
    const existing = seen.get(change.path);
    if (existing) {
      // The same change reached twice by the walk: keep the kind, union the fields.
      for (const f of fields) if (!existing.fields.includes(f)) existing.fields.push(f);
      return;
    }
    const entry: NodeChange = { name, kind: change.kind, fields, path: change.path };
    const oldName = previousNameOf(change);
    if (oldName !== undefined) entry.oldName = oldName;
    seen.set(change.path, entry);
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

function collectionChanges(diff: StructuredDiff | undefined, collection: string): EntityChange[] {
  const out: EntityChange[] = [];
  const seen = new Map<string, EntityChange>();
  if (!diff) return out;

  const prefix = `${collection}/`;
  const entityPath = new RegExp(`^${collection}/[^/]+$`);

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

  const collect = (change: DiffChange): void => {
    const name =
      change.label !== undefined && change.label !== ""
        ? change.label
        : entityPath.test(change.path)
          ? unescapeSegment(change.path.slice(prefix.length))
          : "";
    if (name === "") return;
    const fields = (change.children ?? []).map(fieldNameOf).filter((f) => f !== "");
    const primitives = primitivesUnder(change);
    // By path, for `nodeChanges`' reason: a deleted mesh and a mesh renamed into
    // the name it vacated are two changes about two meshes.
    const existing = seen.get(change.path);
    if (existing) {
      for (const f of fields) if (!existing.fields.includes(f)) existing.fields.push(f);
      for (const p of primitives) if (!existing.primitives.includes(p)) existing.primitives.push(p);
      existing.primitives.sort((a, b) => a - b);
      return;
    }
    const entry: EntityChange = { name, kind: change.kind, fields, path: change.path, primitives };
    seen.set(change.path, entry);
    out.push(entry);
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
  return out;
}

/**
 * The previous name carried by a `renamed` change, or undefined for every other
 * kind. `before` is the bare old name and `after` is the new one *plus* the
 * evidence the handler matched on ("Fender (matched by content, ~91% similar)"),
 * so the new name is read from `label` and never parsed back out of `after`.
 */
function previousNameOf(change: DiffChange): string | undefined {
  if (change.kind !== "renamed") return undefined;
  return typeof change.before === "string" && change.before !== "" ? change.before : undefined;
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
