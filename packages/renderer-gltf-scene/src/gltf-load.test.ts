// GLTFLoader *parsing* needs no WebGL — only an ArrayBuffer — so these run for
// real in Node against real GLB/glTF bytes. What is NOT proven here: drawing.
// Nothing in this file creates a WebGLRenderer.

import { describe, it, expect } from "vitest";
import { loadGltf } from "./gltf-load.js";
import { buildGltf, toArrayBuffer, toGlb, toGltfJson } from "./glb-fixture.js";

// A GLB carries its buffer in the file, but a .gltf carries it as a `data:` uri,
// which three's FileLoader fetches — and its progress reporting constructs a
// ProgressEvent, a browser global Node lacks. This one-line shim is the whole
// "minimal shim" needed to parse real glTF headlessly; nothing else in the
// loader's parse path wants a browser.
if (!("ProgressEvent" in globalThis)) {
  (globalThis as Record<string, unknown>)["ProgressEvent"] = class {
    constructor(
      readonly type: string,
      readonly init?: unknown,
    ) {}
  };
}

const glbBuffer = (spec = {}): ArrayBuffer => toArrayBuffer(toGlb(buildGltf(spec)));

describe("loadGltf", () => {
  it("parses a self-contained GLB into a scene graph", async () => {
    const { gltf } = await loadGltf(glbBuffer());
    expect(gltf.scene.children.map((c) => c.name)).toEqual(["Cube"]);
  });

  it("parses a .gltf JSON document with an embedded data: buffer", async () => {
    const bytes = toGltfJson(buildGltf({ nodes: [{ name: "Panel", mesh: 0 }] }));
    const { gltf } = await loadGltf(toArrayBuffer(bytes));
    expect(gltf.scene.children[0]!.name).toBe("Panel");
  });

  it("keeps hierarchy and transforms", async () => {
    const { gltf } = await loadGltf(
      glbBuffer({
        nodes: [
          { name: "Rig", children: [1] },
          { name: "Bone", mesh: 0, translation: [0, 2, 0] },
        ],
      }),
    );
    const rig = gltf.scene.children[0]!;
    expect(rig.name).toBe("Rig");
    expect(rig.children[0]!.name).toBe("Bone");
    expect(rig.children[0]!.position.toArray()).toEqual([0, 2, 0]);
  });

  it("exposes parser.associations with node indices (the mapping bridge)", async () => {
    const { gltf } = await loadGltf(
      glbBuffer({ nodes: [{ name: "A", mesh: 0 }, { name: "B", mesh: 0 }] }),
    );
    const nodeRecords = [...gltf.parser.associations.values()].filter((r) => r.nodes !== undefined);
    expect(nodeRecords.map((r) => r.nodes).sort()).toEqual([0, 1]);
  });

  it("rejects bytes that aren't glTF at all, so the caller can degrade", async () => {
    const junk = toArrayBuffer(new TextEncoder().encode("this is not a model"));
    await expect(loadGltf(junk)).rejects.toBeDefined();
  });

  it("does not attach the meshopt decoder unless asked", async () => {
    const { meshoptReady } = await loadGltf(glbBuffer());
    expect(meshoptReady).toBe(false);
  });

  it("attaching the meshopt decoder is guarded — a refusal must not fail the load", async () => {
    // Node instantiates the inlined wasm fine, so this asserts the happy path
    // *and* that the guard returns a boolean rather than throwing either way.
    const { gltf, meshoptReady } = await loadGltf(glbBuffer(), { meshopt: true });
    expect(typeof meshoptReady).toBe("boolean");
    expect(gltf.scene.children).toHaveLength(1);
  });
});
