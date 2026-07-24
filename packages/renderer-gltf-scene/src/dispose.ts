// Recursive GPU teardown.
//
// three.js frees nothing automatically: every BufferGeometry, Material and
// Texture holds GPU memory until something calls .dispose() on it, and a real
// model allocates thousands of each. (The previous scene freed one box geometry
// and its per-node materials — fine for a unit-box diagram, a leak per opened
// diff for a real model.) The renderer mounts and unmounts on a toggle, so this
// has to be exhaustive.
//
// Textures decoded from a GLB additionally hold an ImageBitmap, which is
// off-heap: .dispose() releases the GPU texture but not the bitmap. Only
// ImageBitmap.close() does, so it is called explicitly.

import type { BufferGeometry, Material, Object3D, Texture } from "three";

export type DisposeReport = { geometries: number; materials: number; textures: number; bitmaps: number };

type MeshLike = Object3D & { geometry?: BufferGeometry; material?: Material | Material[] };
type SkinnedLike = Object3D & { skeleton?: { dispose?: () => void } };

const isTexture = (value: unknown): value is Texture =>
  typeof value === "object" && value !== null && (value as { isTexture?: boolean }).isTexture === true;

/**
 * Dispose everything reachable from `root`, plus any `extraMaterials` the caller
 * created and swapped out (the originals a ghost/tint replaced, which are no
 * longer referenced by the tree). Every object is visited once, so shared
 * geometries and materials are disposed exactly once and clones that share a
 * parent's geometry don't double-dispose it.
 */
export function disposeTree(root: Object3D, extraMaterials: Iterable<Material> = []): DisposeReport {
  const geometries = new Set<BufferGeometry>();
  const materials = new Set<Material>();

  root.traverse((object) => {
    const mesh = object as MeshLike;
    if (mesh.geometry) geometries.add(mesh.geometry);
    const material = mesh.material;
    if (Array.isArray(material)) for (const m of material) materials.add(m);
    else if (material) materials.add(material);
    const skinned = object as SkinnedLike;
    skinned.skeleton?.dispose?.();
  });
  for (const material of extraMaterials) materials.add(material);

  for (const geometry of geometries) geometry.dispose();

  const textures = new Set<Texture>();
  for (const material of materials) {
    for (const value of Object.values(material as unknown as Record<string, unknown>)) {
      if (isTexture(value)) textures.add(value);
    }
    material.dispose();
  }

  let bitmaps = 0;
  for (const texture of textures) {
    // The decoded bitmap is off-heap; dispose() alone leaves it allocated.
    const data = (texture.source as { data?: unknown } | undefined)?.data;
    if (typeof ImageBitmap !== "undefined" && data instanceof ImageBitmap) {
      data.close();
      bitmaps++;
    }
    texture.dispose();
  }

  // Detach so the scene doesn't hold the disposed subtree alive.
  root.removeFromParent();

  return {
    geometries: geometries.size,
    materials: materials.size,
    textures: textures.size,
    bitmaps,
  };
}
