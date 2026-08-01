import { describe, it, expect } from "vitest";
import { decodeGltf, type GltfDocument } from "./gltf-parse.js";
import {
  ambiguousNameMessage,
  buildNameIndex,
  indirectNodeChanges,
  nodeKey,
  normalizeName,
  resolveNodeIndex,
} from "./node-index.js";
import type { EntityChange } from "./diff-map.js";
import { buildGltf, toGlb } from "./glb-fixture.js";

const indexOf = (nodes: { name?: string }[]) =>
  buildNameIndex(decodeGltf(toGlb(buildGltf({ nodes, sceneNodes: nodes.map((_, i) => i) }))));

describe("nodeKey (mirrors the handler's naming)", () => {
  it("uses the node's name when it has one", () => {
    expect(nodeKey({ name: "Wheel" }, 4)).toBe("Wheel");
  });

  it("falls back to node[i] for missing or empty names", () => {
    expect(nodeKey({}, 3)).toBe("node[3]");
    expect(nodeKey({ name: "" }, 0)).toBe("node[0]");
    expect(nodeKey(undefined, 7)).toBe("node[7]");
  });
});

describe("normalizeName (the manglings this stack applies)", () => {
  it("collapses every known mangling of one name to one form", () => {
    // raw (handler) · sanitizeNodeName (ForgeHub ingest) · slugify (retired here)
    expect(normalizeName("Cube.001")).toBe("cube001");
    expect(normalizeName("Cube001")).toBe("cube001");
    expect(normalizeName("cube-001")).toBe("cube001");
    expect(normalizeName("CUBE 001")).toBe("cube001");
    expect(normalizeName("Cube_001")).toBe("cube001");
  });

  it("collapses the unnamed-node fallback and path punctuation", () => {
    expect(normalizeName("node[3]")).toBe("node3");
    expect(normalizeName("Body:Left/Front")).toBe("bodyleftfront");
  });

  it("keeps genuinely different names apart", () => {
    expect(normalizeName("Cube.001")).not.toBe(normalizeName("Cube.002"));
  });
});

describe("buildNameIndex / resolveNodeIndex", () => {
  it("resolves an exact name to its node index", () => {
    const index = indexOf([{ name: "Hood" }, { name: "Mirror" }]);
    expect(resolveNodeIndex(index, "Mirror")).toMatchObject({ index: 1, via: "key", ambiguous: false });
  });

  it("resolves a dotted name exactly — the whole name, not its last segment", () => {
    const index = indexOf([{ name: "Cube.001" }, { name: "Cube.002" }]);
    expect(resolveNodeIndex(index, "Cube.001").index).toBe(0);
    expect(resolveNodeIndex(index, "Cube.002").index).toBe(1);
  });

  it("resolves a sanitized label back to the raw node (the trap)", () => {
    // The file has "Cube.001"; a layer that strips [].:/ hands us "Cube001".
    const index = indexOf([{ name: "Body" }, { name: "Cube.001" }]);
    const hit = resolveNodeIndex(index, "Cube001");
    expect(hit.index).toBe(1);
    expect(hit.via).toBe("normalized");
  });

  it("resolves a slugified label back to the raw node", () => {
    const index = indexOf([{ name: "Front Left Wheel" }]);
    expect(resolveNodeIndex(index, "front-left-wheel")).toMatchObject({ index: 0, via: "normalized" });
  });

  it("indexes unnamed nodes under node[i], and resolves them", () => {
    const index = indexOf([{ name: "Named" }, {}, {}]);
    expect(index.byKey.get("node[1]")).toEqual([1]);
    expect(resolveNodeIndex(index, "node[2]").index).toBe(2);
  });

  it("flags duplicate names, picking the first (the handler's node map does too)", () => {
    const index = indexOf([{ name: "Cube" }, { name: "Other" }, { name: "Cube" }]);
    const hit = resolveNodeIndex(index, "Cube");
    expect(hit.index).toBe(0);
    expect(hit.all).toEqual([0, 2]);
    expect(hit.ambiguous).toBe(true);
  });

  it("flags duplicates that only collide after normalisation", () => {
    const index = indexOf([{ name: "Cube.001" }, { name: "Cube 001" }]);
    const hit = resolveNodeIndex(index, "cube-001");
    expect(hit.via).toBe("normalized");
    expect(hit.ambiguous).toBe(true);
    expect(hit.all).toEqual([0, 1]);
  });

  it("prefers an exact match over a normalised one", () => {
    const index = indexOf([{ name: "cube001" }, { name: "Cube.001" }]);
    expect(resolveNodeIndex(index, "Cube.001")).toMatchObject({ index: 1, via: "key" });
  });

  it("misses cleanly for a name that isn't in this file", () => {
    const index = indexOf([{ name: "Hood" }]);
    expect(resolveNodeIndex(index, "Spoiler")).toMatchObject({ index: null, via: "none", all: [] });
    expect(resolveNodeIndex(index, "")).toMatchObject({ index: null, via: "none" });
  });

  it("indexes an empty document without inventing nodes", () => {
    const index = buildNameIndex({});
    expect(index.nodeCount).toBe(0);
    expect(resolveNodeIndex(index, "Anything").index).toBeNull();
  });

  it("explains ambiguity in plain language", () => {
    const msg = ambiguousNameMessage("Cube", 3);
    expect(msg).toContain('3 nodes');
    expect(msg).toContain('"Cube"');
    expect(msg).toContain("only the first is highlighted");
  });
});

// #56 + #51: the structure tree's rows are *nodes*, and two whole classes of
// change are named for something else — a mesh, a material — while painting the
// nodes that carry them. Without this resolution the tree marked that geometry
// "unchanged" beside a viewport showing it painted, and the queue's highlight
// looked for a row called "BodyMesh" that no tree has.
describe("indirectNodeChanges — where a mesh or material change lands on the nodes", () => {
  const car: GltfDocument = {
    asset: { version: "2.0" },
    scene: 0,
    scenes: [{ nodes: [0, 1, 2, 3] }],
    nodes: [
      { name: "Body", mesh: 0 },
      { name: "Wheel_FL", mesh: 1 },
      { name: "Wheel_FR", mesh: 1 },
      { name: "Rig" },
    ],
    meshes: [
      { name: "BodyMesh", primitives: [{ material: 0 }, { material: 1 }] },
      { name: "WheelMesh", primitives: [{ material: 1 }] },
    ],
    materials: [{ name: "Paint" }, { name: "Rubber" }],
  };

  const entity = (name: string, path: string, kind: EntityChange["kind"] = "modified"): EntityChange => ({
    name,
    kind,
    fields: [],
    path,
    primitives: [],
  });

  it("resolves a mesh change to every node instancing it, not just the first", () => {
    const found = indirectNodeChanges(buildNameIndex(car), [entity("WheelMesh", "meshes/WheelMesh")], []);
    expect(found.byChange.get("WheelMesh")).toEqual(["Wheel_FL", "Wheel_FR"]);
    expect([...found.byNode.keys()]).toEqual(["Wheel_FL", "Wheel_FR"]);
    expect(found.byNode.get("Wheel_FR")!.name).toBe("WheelMesh");
  });

  it("resolves a material change through the primitives referencing it", () => {
    const found = indirectNodeChanges(buildNameIndex(car), [], [entity("Rubber", "materials/Rubber")]);
    // Rubber is on one primitive of BodyMesh and the whole of WheelMesh, so it
    // reaches three nodes — and Body only once, however many primitives hit.
    expect(found.byChange.get("Rubber")).toEqual(["Body", "Wheel_FL", "Wheel_FR"]);
  });

  it("carries the change's own kind, so a row can be marked with it", () => {
    const found = indirectNodeChanges(buildNameIndex(car), [entity("BodyMesh", "meshes/BodyMesh", "added")], []);
    expect(found.byNode.get("Body")!.kind).toBe("added");
  });

  it("lets the change a reviewer meets first describe a node reached twice", () => {
    // Meshes before materials, matching the order buildOverlay paints them in:
    // the tree's dot and the geometry's colour must come from one answer.
    const found = indirectNodeChanges(
      buildNameIndex(car),
      [entity("BodyMesh", "meshes/BodyMesh")],
      [entity("Paint", "materials/Paint")],
    );
    expect(found.byNode.get("Body")!.name).toBe("BodyMesh");
    expect(found.byChange.get("Paint")).toEqual(["Body"]);
  });

  it("yields no row for a change nothing in the file draws", () => {
    const found = indirectNodeChanges(buildNameIndex(car), [entity("Spoiler", "meshes/Spoiler")], []);
    expect(found.byChange.has("Spoiler")).toBe(false);
    expect(found.byNode.size).toBe(0);
  });

  it("matches a mangled key the same way the paint does", () => {
    const found = indirectNodeChanges(buildNameIndex(car), [entity("wheel-mesh", "meshes/wheel-mesh")], []);
    expect(found.byChange.get("wheel-mesh")).toEqual(["Wheel_FL", "Wheel_FR"]);
  });

  it("names an unnamed node the way the handler would", () => {
    const anonymous: GltfDocument = {
      asset: { version: "2.0" },
      scene: 0,
      scenes: [{ nodes: [0] }],
      nodes: [{ mesh: 0 }],
      meshes: [{ name: "Tri", primitives: [] }],
    };
    const found = indirectNodeChanges(buildNameIndex(anonymous), [entity("Tri", "meshes/Tri")], []);
    expect(found.byChange.get("Tri")).toEqual(["node[0]"]);
  });
});
