import { describe, it, expect } from "vitest";
import type { NodeChange } from "./diff-map.js";
import { emptyKeys, selectionKeys } from "./selection-keys.js";

const change = (name: string, path: string): NodeChange => ({ name, path, kind: "modified", fields: [] });

const keys = selectionKeys([
  change("Wheel_FL", "nodes/Wheel_FL"),
  change("rig/hand", "nodes/rig%2Fhand"),
  change("node[3]", "nodes/node[3]"),
  change("Paint", "materials/Paint"),
]);

describe("selectionKeys", () => {
  it("resolves a change path to itself — both halves speak one key", () => {
    expect(keys.changePathOf("nodes/Wheel_FL")).toBe("nodes/Wheel_FL");
    expect(keys.changePathOf("nodes/rig%2Fhand")).toBe("nodes/rig%2Fhand");
    expect(keys.changePathOf("nodes/node[3]")).toBe("nodes/node[3]");
    expect(keys.changePathOf("materials/Paint")).toBe("materials/Paint");
  });

  it("resolves a field row to the object that owns it", () => {
    // Selecting "translation" under a node means selecting the node in 3D.
    expect(keys.changePathOf("nodes/Wheel_FL/translation")).toBe("nodes/Wheel_FL");
    expect(keys.changePathOf("nodes/rig%2Fhand/scale")).toBe("nodes/rig%2Fhand");
  });

  it("resolves nothing for a change this diff never mentioned", () => {
    // The honest answer: there is no such change, so there is nothing to select.
    expect(keys.changePathOf("nodes/Unknown")).toBeNull();
    expect(keys.changePathOf("nodes/Unknown/translation")).toBeNull();
    expect(keys.changePathOf("meshes/Tri/primitives")).toBeNull();
  });

  it("keeps two changes that share a name apart (#47)", () => {
    // The deletion, and the rename that took the name it vacated. Keyed by name
    // one of the two was unreachable; keyed by path both resolve, to themselves.
    const collided = selectionKeys([change("B", "nodes/B#1"), change("B", "nodes/B")]);
    expect(collided.changePathOf("nodes/B#1")).toBe("nodes/B#1");
    expect(collided.changePathOf("nodes/B")).toBe("nodes/B");
    expect(collided.changePathOf("nodes/B#1/translation")).toBe("nodes/B#1");
  });
});

describe("emptyKeys (the fallback views, which have no diff to resolve against)", () => {
  it("resolves nothing", () => {
    expect(emptyKeys().changePathOf("nodes/Wheel_FL")).toBeNull();
  });
});
