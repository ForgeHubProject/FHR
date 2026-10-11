// The index-based diff→object mapping, end to end on real bytes: fixture GLB →
// GLTFLoader.parseAsync → parser.associations → node index → Object3D. No WebGL
// involved, so this is the headless smoke test issue #44 asks for.

import { describe, it, expect } from "vitest";
import type { Object3D } from "three";
import { loadGltf } from "./gltf-load.js";
import { decodeGltf } from "./gltf-parse.js";
import { buildNameIndex, resolveNodeIndex } from "./node-index.js";
import { meshesIn, nodeIndexOfObject, objectsByNodeIndex } from "./associations.js";
import { buildGltf, toArrayBuffer, toGlb, type FixtureSpec } from "./glb-fixture.js";

async function load(spec: FixtureSpec) {
  const bytes = toGlb(buildGltf(spec));
  const { gltf } = await loadGltf(toArrayBuffer(bytes));
  return { gltf, index: buildNameIndex(decodeGltf(bytes)) };
}

/** The mapping this slice exists to provide: a diff label → the drawn objects. */
async function objectsFor(
  spec: FixtureSpec,
  label: string,
): Promise<{ nodeIndex: number | null; objects: Object3D[] }> {
  const { gltf, index } = await load(spec);
  const resolved = resolveNodeIndex(index, label);
  if (resolved.index === null) return { nodeIndex: null, objects: [] };
  return { nodeIndex: resolved.index, objects: objectsByNodeIndex(gltf).get(resolved.index) ?? [] };
}

describe("objectsByNodeIndex", () => {
  it("maps every glTF node index to the object built for it", async () => {
    const { gltf } = await load({
      nodes: [{ name: "Hood", mesh: 0 }, { name: "Rig", children: [2] }, { name: "Bone", mesh: 0 }],
    });
    const byIndex = objectsByNodeIndex(gltf);
    expect([...byIndex.keys()].sort()).toEqual([0, 1, 2]);
    expect(byIndex.get(0)![0]!.name).toBe("Hood");
    expect(byIndex.get(2)![0]!.name).toBe("Bone");
  });

  it("maps a multi-primitive node to its Group, whose children hold the meshes", async () => {
    const { gltf } = await load({ nodes: [{ name: "Shell", mesh: 0 }], primitives: 3 });
    const object = objectsByNodeIndex(gltf).get(0)![0]!;
    expect(meshesIn(object)).toHaveLength(3);
    // Every drawn mesh of that node is reachable from the node's object …
    for (const mesh of meshesIn(object)) {
      // … and leads back to node 0 by walking up parents (raycast direction).
      expect(nodeIndexOfObject(mesh, gltf)).toBe(0);
    }
  });

  it("ignores material and texture associations", async () => {
    const { gltf } = await load({ nodes: [{ name: "A", mesh: 0 }] });
    const materialRecords = [...gltf.parser.associations.entries()].filter(
      ([object]) => (object as { isMaterial?: boolean }).isMaterial === true,
    );
    expect(materialRecords.length).toBeGreaterThan(0); // the loader does record materials
    expect([...objectsByNodeIndex(gltf).values()].flat().every((o) => o.isObject3D)).toBe(true);
  });
});

describe("nodeIndexOfObject", () => {
  it("finds the node of a mesh directly associated with one", async () => {
    const { gltf } = await load({ nodes: [{ name: "Solo", mesh: 0 }] });
    const mesh = meshesIn(gltf.scene)[0]!;
    expect(nodeIndexOfObject(mesh, gltf)).toBe(0);
  });

  it("walks up to the nearest node ancestor, not the root", async () => {
    const { gltf } = await load({
      nodes: [{ name: "Rig", children: [1] }, { name: "Bone", mesh: 0 }],
    });
    const bone = meshesIn(gltf.scene)[0]!;
    expect(nodeIndexOfObject(bone, gltf)).toBe(1);
  });

  it("returns null for objects the loader didn't create", async () => {
    const { gltf } = await load({ nodes: [{ name: "A", mesh: 0 }] });
    expect(nodeIndexOfObject(null, gltf)).toBeNull();
    expect(nodeIndexOfObject(gltf.scene, gltf)).toBeNull();
  });
});

describe("diff label → drawn objects (name-mangling proof)", () => {
  it("resolves a plain name", async () => {
    const { nodeIndex, objects } = await objectsFor({ nodes: [{ name: "Mirror", mesh: 0 }] }, "Mirror");
    expect(nodeIndex).toBe(0);
    expect(objects.map((o) => o.name)).toEqual(["Mirror"]);
  });

  it("resolves a dotted name to the right one of several", async () => {
    const spec = { nodes: [{ name: "Cube.001", mesh: 0 }, { name: "Cube.002", mesh: 0 }] };
    const { nodeIndex, objects } = await objectsFor(spec, "Cube.002");
    expect(nodeIndex).toBe(1);
    expect(objects).toHaveLength(1);
  });

  it("resolves a sanitized label (the mangling the old slugify matching missed)", async () => {
    const spec = { nodes: [{ name: "Body", mesh: 0 }, { name: "Cube.001", mesh: 0 }] };
    expect((await objectsFor(spec, "Cube001")).nodeIndex).toBe(1);
  });

  it("resolves an unnamed node by its node[i] label", async () => {
    const spec = { nodes: [{ name: "Named", mesh: 0 }, { mesh: 0 }] };
    const { nodeIndex, objects } = await objectsFor(spec, "node[1]");
    expect(nodeIndex).toBe(1);
    expect(objects).toHaveLength(1);
    expect(objects[0]!.name).not.toBe("Named");
  });

  it("finds nothing for a name that isn't in the head file (e.g. a removed node)", async () => {
    expect(await objectsFor({ nodes: [{ name: "Hood", mesh: 0 }] }, "Mirror")).toEqual({
      nodeIndex: null,
      objects: [],
    });
  });

  it("why indices and not object names: GLTFLoader sanitizes the names itself", async () => {
    // three runs node names through PropertyBinding.sanitizeNodeName, which
    // deletes `[].:/ ` — so the drawn object is called "Cube001" even though the
    // file (and the diff) call it "Cube.001". Matching a diff label against
    // `object.name` would silently miss every dotted Blender duplicate; going
    // through the node index is immune.
    const { gltf, index } = await load({ nodes: [{ name: "Cube.001", mesh: 0 }] });
    const drawn = gltf.scene.children[0]!;
    expect(drawn.name).toBe("Cube001");
    expect(index.byKey.has("Cube.001")).toBe(true);
    expect(resolveNodeIndex(index, "Cube.001").index).toBe(0);
  });
});
