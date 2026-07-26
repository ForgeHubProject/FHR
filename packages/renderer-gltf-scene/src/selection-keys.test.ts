import { describe, it, expect } from "vitest";
import type { NodeChange } from "./diff-map.js";
import { emptyKeys, selectionKeys } from "./selection-keys.js";

const change = (name: string, path: string): NodeChange => ({ name, path, kind: "modified", fields: [] });

const keys = selectionKeys([
  change("Wheel_FL", "nodes/Wheel_FL"),
  change("rig/hand", "nodes/rig%2Fhand"),
  change("node[3]", "nodes/node[3]"),
]);

describe("selectionKeys", () => {
  it("translates a change path to the node name the scene knows", () => {
    expect(keys.nameOf("nodes/Wheel_FL")).toBe("Wheel_FL");
    expect(keys.nameOf("nodes/rig%2Fhand")).toBe("rig/hand");
    expect(keys.nameOf("nodes/node[3]")).toBe("node[3]");
  });

  it("translates a field row to the object that owns it", () => {
    // Selecting "translation" under a node means selecting the node in 3D.
    expect(keys.nameOf("nodes/Wheel_FL/translation")).toBe("Wheel_FL");
    expect(keys.nameOf("nodes/rig%2Fhand/scale")).toBe("rig/hand");
  });

  it("falls back to the path scheme for a node this diff never mentioned", () => {
    // A host keying on node paths still lands somewhere sensible; the scene then
    // reports that the name isn't painted.
    expect(keys.nameOf("nodes/Unknown")).toBe("Unknown");
  });

  it("has no name for a change that isn't about a node", () => {
    expect(keys.nameOf("materials/Paint")).toBeNull();
    expect(keys.nameOf("meshes/Tri/primitives")).toBeNull();
  });

  it("translates a picked node name back to the handler's own path", () => {
    expect(keys.pathOf("rig/hand")).toBe("nodes/rig%2Fhand");
    expect(keys.pathOf("Wheel_FL")).toBe("nodes/Wheel_FL");
  });

  it("builds a path for a picked node the diff never mentioned", () => {
    expect(keys.pathOf("Bystander")).toBe("nodes/Bystander");
  });

  it("round-trips every change in the diff", () => {
    for (const path of ["nodes/Wheel_FL", "nodes/rig%2Fhand", "nodes/node[3]"]) {
      expect(keys.pathOf(keys.nameOf(path)!), path).toBe(path);
    }
  });

  it("re-keys the lite bundle's headlines by node name for the callout", () => {
    expect(
      keys.headlinesByName({
        "nodes/Wheel_FL": "moved 50 mm",
        "nodes/rig%2Fhand": "scaled ×1.2",
        "materials/Paint": "recoloured",
      }),
    ).toEqual({ "Wheel_FL": "moved 50 mm", "rig/hand": "scaled ×1.2" });
  });

  it("has no headlines to re-key when none were passed", () => {
    expect(keys.headlinesByName(undefined)).toEqual({});
  });

  it("keeps the first path when one name is changed twice", () => {
    const dup = selectionKeys([change("Cube", "nodes/Cube"), change("Cube", "nodes/Cube%20copy")]);
    expect(dup.pathOf("Cube")).toBe("nodes/Cube");
  });
});

describe("emptyKeys (the fallback views, which have no diff to translate)", () => {
  it("resolves nothing, and still builds a path for a name", () => {
    expect(emptyKeys().nameOf("nodes/Wheel_FL")).toBeNull();
    expect(emptyKeys().pathOf("Wheel_FL")).toBe("nodes/Wheel_FL");
    expect(emptyKeys().headlinesByName({ a: "b" })).toEqual({});
  });
});
