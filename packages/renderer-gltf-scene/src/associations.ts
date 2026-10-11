// glTF node index → the three.js objects the loader built for it, and back.
//
// GLTFLoader records this for us in `parser.associations`: a Map from every
// Object3D/Material/Texture it created to the glTF indices it came from, with
// plural collection keys ({ nodes: 3 }, { meshes: 1, primitives: 0 }). It has
// been stable since r133 and is the only mapping in the stack that survives name
// mangling, because it never involves a name.
//
// Two shapes to expect for one glTF node:
//   * mesh with a single primitive → one Mesh, associated { meshes, primitives, nodes }
//   * mesh with several primitives → a Group associated { nodes }, whose child
//     Meshes are associated { meshes, primitives } and carry no `nodes` key
// so "the objects for node i" is the associated object *plus its descendants*,
// and "the node for this object" is found by walking up to the first ancestor
// with a `nodes` key (the raycast-picking direction, used by #45).

import type { Material, Object3D, Texture } from "three";

/** The parts of GLTFLoader's result this module needs (keeps tests light). */
export type AssociatedGltf = {
  scene: Object3D;
  parser: { associations: Map<Object3D | Material | Texture, { nodes?: number }> };
};

const isObject3D = (v: unknown): v is Object3D =>
  typeof v === "object" && v !== null && (v as { isObject3D?: boolean }).isObject3D === true;

/**
 * Node index → the objects created for that node, in traversal order. One index
 * can map to several objects when the loader instances a reused node.
 */
export function objectsByNodeIndex(gltf: AssociatedGltf): Map<number, Object3D[]> {
  const out = new Map<number, Object3D[]>();
  for (const [object, record] of gltf.parser.associations) {
    if (record.nodes === undefined || !isObject3D(object)) continue;
    const existing = out.get(record.nodes);
    if (existing) existing.push(object);
    else out.set(record.nodes, [object]);
  }
  return out;
}

/**
 * The glTF node index an object belongs to, walking up ancestors until one is
 * associated with a node. Returns null for objects the loader didn't create
 * (lights, helpers, the ghost overlay's clones).
 */
export function nodeIndexOfObject(object: Object3D | null, gltf: AssociatedGltf): number | null {
  const associations = gltf.parser.associations;
  for (let current = object; current; current = current.parent) {
    const record = associations.get(current);
    if (record?.nodes !== undefined) return record.nodes;
  }
  return null;
}

/** Every Mesh-like object in a subtree (things with geometry and material). */
export function meshesIn(root: Object3D): Object3D[] {
  const out: Object3D[] = [];
  root.traverse((object) => {
    if ((object as { isMesh?: boolean; isPoints?: boolean; isLine?: boolean }).isMesh === true) out.push(object);
  });
  return out;
}
