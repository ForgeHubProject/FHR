import { describe, it, expect } from "vitest";
import { decodeGltf } from "./gltf-parse.js";
import { ambiguousNameMessage, buildNameIndex, nodeKey, normalizeName, resolveNodeIndex } from "./node-index.js";
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
