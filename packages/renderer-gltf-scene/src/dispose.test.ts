import { describe, it, expect } from "vitest";
import {
  BoxGeometry,
  Group,
  Mesh,
  MeshStandardMaterial,
  Object3D,
  Texture,
  type BufferGeometry,
  type Material,
} from "three";
import { disposeTree } from "./dispose.js";

/** Count dispose events, which is what three itself listens for. */
function watch(disposables: { addEventListener(t: string, f: () => void): void }[]): () => number {
  let count = 0;
  for (const d of disposables) d.addEventListener("dispose", () => count++);
  return () => count;
}

describe("disposeTree", () => {
  it("frees geometry and materials through the whole subtree", () => {
    const root = new Group();
    const geometries: BufferGeometry[] = [];
    const materials: Material[] = [];
    let parent: Object3D = root;
    for (let i = 0; i < 3; i++) {
      const geometry = new BoxGeometry();
      const material = new MeshStandardMaterial();
      geometries.push(geometry);
      materials.push(material);
      const mesh = new Mesh(geometry, material);
      parent.add(mesh);
      parent = mesh; // nest, so a non-recursive dispose would miss the deeper ones
    }
    const geometryDisposes = watch(geometries);
    const materialDisposes = watch(materials);

    const report = disposeTree(root);
    expect(report).toMatchObject({ geometries: 3, materials: 3 });
    expect(geometryDisposes()).toBe(3);
    expect(materialDisposes()).toBe(3);
  });

  it("disposes shared geometry and materials exactly once", () => {
    const geometry = new BoxGeometry();
    const material = new MeshStandardMaterial();
    const root = new Group();
    for (let i = 0; i < 5; i++) root.add(new Mesh(geometry, material));
    const count = watch([geometry, material]);

    const report = disposeTree(root);
    expect(report).toMatchObject({ geometries: 1, materials: 1 });
    expect(count()).toBe(2); // one geometry event + one material event
  });

  it("handles multi-material meshes", () => {
    const materials = [new MeshStandardMaterial(), new MeshStandardMaterial()];
    const root = new Group();
    root.add(new Mesh(new BoxGeometry(), materials));
    expect(disposeTree(root).materials).toBe(2);
  });

  it("disposes textures hanging off materials", () => {
    const material = new MeshStandardMaterial();
    material.map = new Texture();
    material.normalMap = new Texture();
    const root = new Group();
    root.add(new Mesh(new BoxGeometry(), material));
    const count = watch([material.map, material.normalMap]);

    expect(disposeTree(root).textures).toBe(2);
    expect(count()).toBe(2);
  });

  it("closes ImageBitmaps, which dispose() alone leaves allocated off-heap", () => {
    let closed = 0;
    class FakeImageBitmap {
      close(): void {
        closed++;
      }
    }
    const original = (globalThis as Record<string, unknown>)["ImageBitmap"];
    (globalThis as Record<string, unknown>)["ImageBitmap"] = FakeImageBitmap;
    try {
      const material = new MeshStandardMaterial();
      const texture = new Texture();
      texture.source.data = new FakeImageBitmap();
      material.map = texture;
      const root = new Group();
      root.add(new Mesh(new BoxGeometry(), material));

      const report = disposeTree(root);
      expect(report.bitmaps).toBe(1);
      expect(closed).toBe(1);
    } finally {
      if (original === undefined) delete (globalThis as Record<string, unknown>)["ImageBitmap"];
      else (globalThis as Record<string, unknown>)["ImageBitmap"] = original;
    }
  });

  it("disposes extra materials the caller swapped out of the tree", () => {
    const orphan = new MeshStandardMaterial();
    const count = watch([orphan]);
    const root = new Group();
    root.add(new Mesh(new BoxGeometry(), new MeshStandardMaterial()));

    const report = disposeTree(root, [orphan]);
    expect(report.materials).toBe(2);
    expect(count()).toBe(1);
  });

  it("detaches the subtree so a disposed scene isn't kept alive", () => {
    const scene = new Group();
    const subtree = new Group();
    subtree.add(new Mesh(new BoxGeometry(), new MeshStandardMaterial()));
    scene.add(subtree);

    disposeTree(subtree);
    expect(subtree.parent).toBeNull();
    expect(scene.children).toHaveLength(0);
  });

  it("copes with a tree that has nothing to free", () => {
    expect(disposeTree(new Group())).toEqual({ geometries: 0, materials: 0, textures: 0, bitmaps: 0 });
  });
});
