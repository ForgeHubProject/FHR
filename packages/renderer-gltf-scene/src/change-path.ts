// Selection keys: the handler's fully-qualified change paths, and what their
// shape means.
//
// The scheme (#41): segments are "/"-separated, with "%" and "/" percent-escaped
// inside a segment, so "nodes/Cube.001/translation" is one node's one field and
// "nodes/rig%2Fhand" is a node actually called "rig/hand". The shape of a path is
// therefore meaningful:
//
//   nodes                          a collection wrapper
//   nodes/Wheel_FL                 an object — what a reviewer calls "a change"
//   nodes/Wheel_FL/translation     a field of an object
//
// Both bundles need this: the lite one keys the change tree and the host's select
// events on paths, the 3D one has to turn a picked node back into the same key.
// So it stays dependency-free — no SDK, no three.js, no DOM — and both bundles
// pay a few hundred bytes for it rather than sharing a heavier module.

const SEP = "/";
const NODES = "nodes";

/** Reverse the handler's segment escaping ("/" first, then "%"). */
export function unescapeSegment(segment: string): string {
  return segment.replace(/%2F/gi, SEP).replace(/%25/g, "%");
}

/** Apply the handler's segment escaping, so a name can be turned into a path. */
export function escapeSegment(name: string): string {
  return name.replace(/%/g, "%25").replace(/\//g, "%2F");
}

/** How many segments a path has: 1 collection, 2 object, 3+ field. */
export function segmentCount(path: string): number {
  return path.split(SEP).length;
}

/** The object a path belongs to: "nodes/Cube/translation" → "nodes/Cube". */
export function entityPath(path: string): string {
  const parts = path.split(SEP);
  return parts.length <= 2 ? path : `${parts[0]}${SEP}${parts[1]}`;
}

/**
 * The glTF node name a path is about, or null when the path isn't about a node
 * (a material, a mesh, an animation — real changes, but not ones with a place in
 * the scene graph to fly to).
 */
export function nodeNameOfPath(path: string): string | null {
  const parts = path.split(SEP);
  if (parts.length < 2 || parts[0] !== NODES) return null;
  const name = unescapeSegment(parts[1]!);
  return name === "" ? null : name;
}

/** A node name as a selection key: "rig/hand" → "nodes/rig%2Fhand". */
export function pathOfNodeName(name: string): string {
  return `${NODES}${SEP}${escapeSegment(name)}`;
}
