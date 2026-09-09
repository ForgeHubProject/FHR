// Isolate-on-select: show one change, hide the rest, put everything back.
//
// "Isolate" is the verb every DCC tool uses for this (Blender's local view,
// Maya's isolate select), and in a diff review it answers the question the
// overlay alone can't: *is that all that changed here?* A wheel tinted orange in
// a car full of grey geometry is still surrounded by a car.
//
// Two rules, both learned from getting them wrong:
//
//   * only leaf meshes are toggled, never groups. Hiding a group hides
//     everything under it, and a selected mesh whose ancestor got hidden
//     disappears — the one object the reviewer asked to see.
//   * the previous `visible` value of every mesh touched is remembered, so
//     clearing an isolation restores the scene exactly, including meshes that
//     were already hidden for their own reasons (the base-solid group is hidden
//     between blinks, the ghost of a moved node is hidden when it overlaps).
//
// three.js is used for scene traversal only — no renderer, no canvas — so this is
// exercised headlessly.

import type { Object3D } from "three";

export type Isolator = {
  /** Show only these subtrees' meshes. An empty list is a no-op. */
  isolate(keep: readonly Object3D[]): void;
  /** Restore every mesh this isolator hid. */
  clear(): void;
  readonly active: boolean;
};

const isMesh = (object: Object3D): boolean => (object as { isMesh?: boolean }).isMesh === true;

/**
 * An isolator over `root`. Subtrees listed in `skip` are never touched: the
 * previous version's solid copy lives there, and the A/B blink has to keep
 * working while a change is isolated (a blink is a whole-model comparison — it is
 * not about the selection).
 */
export function createIsolator(root: Object3D, options: { skip?: readonly Object3D[] } = {}): Isolator {
  const skip = new Set(options.skip ?? []);
  /** Mesh → the `visible` it had before we touched it. */
  const hidden = new Map<Object3D, boolean>();

  const eachMesh = (subtree: Object3D, visit: (mesh: Object3D) => void): void => {
    subtree.traverse((object) => {
      if (isMesh(object)) visit(object);
    });
  };

  const skipped = (object: Object3D): boolean => {
    for (let current: Object3D | null = object; current; current = current.parent) {
      if (skip.has(current)) return true;
    }
    return false;
  };

  return {
    isolate(keep: readonly Object3D[]): void {
      if (keep.length === 0) return;
      const wanted = new Set<Object3D>();
      for (const subtree of keep) {
        eachMesh(subtree, (mesh) => wanted.add(mesh));
        // A selected object may itself be the mesh.
        if (isMesh(subtree)) wanted.add(subtree);
      }
      if (wanted.size === 0) return;
      this.clear();
      eachMesh(root, (mesh) => {
        if (wanted.has(mesh) || skipped(mesh)) return;
        hidden.set(mesh, mesh.visible);
        mesh.visible = false;
      });
    },
    clear(): void {
      for (const [mesh, visible] of hidden) mesh.visible = visible;
      hidden.clear();
    },
    get active(): boolean {
      return hidden.size > 0;
    },
  };
}
