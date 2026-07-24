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

/** The glTF collection this renderer paints. */
const NODES = "nodes";
const NODE_PREFIX = `${NODES}/`;

/** A node-level path: exactly one (escaped) segment below the collection. */
const NODE_PATH = /^nodes\/[^/]+$/;

/** Reverse the handler's segment escaping ("/" first, then "%"). */
function unescapeSegment(segment: string): string {
  return segment.replace(/%2F/gi, "/").replace(/%25/g, "%");
}

/** Node fields that move geometry in space, as the handler labels them. */
export const TRANSFORM_FIELDS: readonly string[] = ["translation", "rotation", "scale"];

export type NodeChange = {
  /** The node's name as the diff names it (handler's `name || node[i]`). */
  name: string;
  kind: ChangeKind;
  /** Changed field labels on this node, e.g. ["translation", "mesh"]. */
  fields: string[];
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
    const entry: NodeChange = { name, kind: change.kind, fields };
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
