import { describe, it, expect } from "vitest";
import type { StructuredDiff } from "@fhr/types";
import {
  diffChangeTypes,
  geometryChanges,
  hasTransformChange,
  isTransformOnly,
  nodeChanges,
} from "./diff-map.js";

/** A diff shaped the way handler-gltf-scene emits one today. */
function diffOf(...children: StructuredDiff["changes"]): StructuredDiff {
  return {
    version: "1.0",
    format: "gltf-scene",
    changes: [{ path: "nodes", label: "nodes", kind: "modified", children }],
  };
}

describe("nodeChanges", () => {
  it("reads node-level changes out of the nodes collection, with their fields", () => {
    const diff = diffOf(
      {
        path: "nodes/Cube",
        label: "Cube",
        kind: "modified",
        children: [
          { path: "nodes/Cube/translation", label: "translation", kind: "modified", before: "[0 0 0]", after: "[1 0 0]" },
          { path: "nodes/Cube/mesh", label: "mesh", kind: "modified" },
        ],
      },
      { path: "nodes/NewLamp", label: "NewLamp", kind: "added" },
      { path: "nodes/Mirror", label: "Mirror", kind: "removed" },
    );
    expect(nodeChanges(diff)).toEqual([
      { name: "Cube", kind: "modified", fields: ["translation", "mesh"], path: "nodes/Cube" },
      { name: "NewLamp", kind: "added", fields: [], path: "nodes/NewLamp" },
      { name: "Mirror", kind: "removed", fields: [], path: "nodes/Mirror" },
    ]);
  });

  it("keeps the raw name — no slugifying, no lowercasing", () => {
    const diff = diffOf({ path: "nodes/Front Wheel.001", label: "Front Wheel.001", kind: "modified" });
    expect(nodeChanges(diff)[0]!.name).toBe("Front Wheel.001");
  });

  it("takes a dotted name from the path remainder when there's no label", () => {
    // A dot is legal inside a segment — the whole remainder is the name.
    const diff = diffOf({ path: "nodes/Cube.001", kind: "modified" });
    expect(nodeChanges(diff)[0]!.name).toBe("Cube.001");
  });

  it("reads the unnamed-node fallback label", () => {
    const diff = diffOf({ path: "nodes/node[2]", label: "node[2]", kind: "removed" });
    expect(nodeChanges(diff)[0]).toEqual({
      name: "node[2]",
      kind: "removed",
      fields: [],
      path: "nodes/node[2]",
    });
  });

  it("also accepts node changes that appear without the collection wrapper", () => {
    const diff: StructuredDiff = {
      version: "1.0",
      format: "gltf-scene",
      changes: [{ path: "nodes/Cube", label: "Cube", kind: "added" }],
    };
    expect(nodeChanges(diff)).toEqual([{ name: "Cube", kind: "added", fields: [], path: "nodes/Cube" }]);
  });

  it("ignores other collections (materials, meshes, animations)", () => {
    const diff: StructuredDiff = {
      version: "1.0",
      format: "gltf-scene",
      changes: [
        { path: "materials", label: "materials", kind: "modified", children: [{ path: "materials/Paint", label: "Paint", kind: "modified" }] },
        { path: "meshes", label: "meshes", kind: "modified", children: [{ path: "meshes/Tri", label: "Tri", kind: "modified" }] },
      ],
    };
    expect(nodeChanges(diff)).toEqual([]);
  });

  it("finds nodes nested under an outer wrapper change", () => {
    const diff: StructuredDiff = {
      version: "1.0",
      format: "gltf-scene",
      changes: [
        {
          path: "scene",
          label: "scene",
          kind: "modified",
          children: [{ path: "nodes", label: "nodes", kind: "modified", children: [{ path: "nodes/Cube", label: "Cube", kind: "added" }] }],
        },
      ],
    };
    expect(nodeChanges(diff).map((c) => c.name)).toEqual(["Cube"]);
  });

  it("does not mistake a field for a node", () => {
    const diff = diffOf({
      path: "nodes/Cube",
      label: "Cube",
      kind: "modified",
      children: [{ path: "nodes/Cube/translation", label: "translation", kind: "modified" }],
    });
    expect(nodeChanges(diff).map((c) => c.name)).toEqual(["Cube"]);
  });

  it("merges a node named twice, keeping the first kind and unioning fields", () => {
    const diff = diffOf(
      { path: "nodes/Cube", label: "Cube", kind: "modified", children: [{ path: "nodes/Cube/translation", label: "translation", kind: "modified" }] },
      { path: "nodes/Cube", label: "Cube", kind: "modified", children: [{ path: "nodes/Cube/scale", label: "scale", kind: "modified" }] },
    );
    expect(nodeChanges(diff)).toEqual([
      { name: "Cube", kind: "modified", fields: ["translation", "scale"], path: "nodes/Cube" },
    ]);
  });

  it("survives an absent diff and a null changes array from the wire", () => {
    expect(nodeChanges(undefined)).toEqual([]);
    const nulled = { version: "1.0", format: "gltf-scene", changes: null } as unknown as StructuredDiff;
    expect(nodeChanges(nulled)).toEqual([]);
  });
});

describe("diffChangeTypes", () => {
  it("keys node change kinds by raw name, ignoring field children", () => {
    const diff = diffOf(
      { path: "nodes/Cube.001", label: "Cube.001", kind: "modified", children: [{ path: "nodes/Cube/translation", label: "translation", kind: "modified" }] },
      { path: "nodes/NewLamp", label: "NewLamp", kind: "added" },
    );
    const map = diffChangeTypes(diff);
    expect(map.get("Cube.001")).toBe("modified");
    expect(map.get("NewLamp")).toBe("added");
    expect(map.has("cube-001")).toBe(false); // the retired slugified key
    expect(map.has("translation")).toBe(false);
  });

  it("returns an empty map for no diff", () => {
    expect(diffChangeTypes(undefined).size).toBe(0);
  });
});

describe("transform-change classification (drives the move ghost)", () => {
  it("recognises a pure move / rotate / scale", () => {
    expect(isTransformOnly({ name: "A", kind: "modified", fields: ["translation"], path: "nodes/A" })).toBe(true);
    expect(isTransformOnly({ name: "A", kind: "modified", fields: ["rotation", "scale"], path: "nodes/A" })).toBe(true);
  });

  it("is not a pure move when something else changed too", () => {
    expect(isTransformOnly({ name: "A", kind: "modified", fields: ["translation", "mesh"], path: "nodes/A" })).toBe(false);
    expect(hasTransformChange({ name: "A", kind: "modified", fields: ["translation", "mesh"], path: "nodes/A" })).toBe(true);
  });

  it("added/removed nodes are not moves, and fieldless changes aren't either", () => {
    expect(isTransformOnly({ name: "A", kind: "added", fields: ["translation"], path: "nodes/A" })).toBe(false);
    expect(isTransformOnly({ name: "A", kind: "modified", fields: [], path: "nodes/A" })).toBe(false);
    expect(hasTransformChange({ name: "A", kind: "modified", fields: ["mesh"], path: "nodes/A" })).toBe(false);
  });
});

describe("path escaping (handler scheme: %2F for '/', %25 for '%')", () => {
  it("unescapes a slash-bearing name from the path when there is no label", () => {
    const diff: StructuredDiff = {
      version: "1.0",
      format: "gltf-scene",
      changes: [{ path: "nodes/rig%2Fhand", kind: "removed" }],
    };
    // The name is unescaped for display; the path stays the escaped machine key.
    expect(nodeChanges(diff)).toEqual([
      { name: "rig/hand", kind: "removed", fields: [], path: "nodes/rig%2Fhand" },
    ]);
  });

  it("never mistakes a qualified field path for a node", () => {
    const diff: StructuredDiff = {
      version: "1.0",
      format: "gltf-scene",
      changes: [{ path: "nodes/Cube/translation", label: "translation", kind: "modified" }],
    };
    expect(nodeChanges(diff)).toEqual([]);
  });
});

describe("geometryChanges (the heatmap's gate)", () => {
  /** A meshes-collection diff, the shape handler-gltf-scene emits. */
  const meshDiff = (...children: StructuredDiff["changes"]): StructuredDiff => ({
    version: "1.0",
    format: "gltf-scene",
    changes: [{ path: "meshes", label: "meshes", kind: "modified", children }],
  });

  const primitive = (mesh: string, ordinal: number, ...rows: StructuredDiff["changes"]) => ({
    path: `meshes/${mesh}/primitives/${ordinal}`,
    label: `primitive[${ordinal}]`,
    kind: "modified" as const,
    children: rows,
  });

  it("picks up the three rows that mean the vertex data moved", () => {
    for (const row of [
      { path: "meshes/Hood/primitives/0/geometry/POSITION", label: "POSITION" },
      { path: "meshes/Hood/primitives/0/bounds", label: "bounds" },
      { path: "meshes/Hood/primitives/0/centroid", label: "centroid" },
    ]) {
      const diff = meshDiff({
        path: "meshes/Hood",
        label: "Hood",
        kind: "modified",
        children: [primitive("Hood", 0, { ...row, kind: "modified" })],
      });
      expect(geometryChanges(diff)).toEqual([
        { name: "Hood", kind: "modified", path: "meshes/Hood", primitives: [0] },
      ]);
    }
  });

  it("ignores a mesh change with no vertex data in it", () => {
    // A material reassignment is a real change on a real mesh — and the two
    // sides' geometry is byte-identical, so a heatmap of it would read zero
    // everywhere. Offering the toggle for it would be a picture of nothing.
    const diff = meshDiff({
      path: "meshes/Hood",
      label: "Hood",
      kind: "modified",
      children: [
        primitive("Hood", 0, {
          path: "meshes/Hood/primitives/0/material",
          label: "material",
          kind: "modified",
        }),
      ],
    });
    expect(geometryChanges(diff)).toEqual([]);
  });

  it("names only the primitives that actually changed", () => {
    const diff = meshDiff({
      path: "meshes/Body",
      label: "Body",
      kind: "modified",
      children: [
        primitive("Body", 0, {
          path: "meshes/Body/primitives/0/material",
          label: "material",
          kind: "modified",
        }),
        primitive("Body", 2, {
          path: "meshes/Body/primitives/2/geometry/POSITION",
          label: "POSITION",
          kind: "modified",
        }),
        primitive("Body", 3, {
          path: "meshes/Body/primitives/3/bounds",
          label: "bounds",
          kind: "modified",
        }),
      ],
    });
    expect(geometryChanges(diff)).toEqual([
      { name: "Body", kind: "modified", path: "meshes/Body", primitives: [2, 3] },
    ]);
  });

  it("reads a mesh change that arrives without the collection wrapper", () => {
    const diff: StructuredDiff = {
      version: "1.0",
      format: "gltf-scene",
      changes: [
        {
          path: "meshes/Hull",
          label: "Hull",
          kind: "modified",
          children: [
            primitive("Hull", 1, {
              path: "meshes/Hull/primitives/1/centroid",
              label: "centroid",
              kind: "modified",
            }),
          ],
        },
      ],
    };
    expect(geometryChanges(diff)).toEqual([
      { name: "Hull", kind: "modified", path: "meshes/Hull", primitives: [1] },
    ]);
  });

  it("never reads a node or material change as geometry", () => {
    const diff: StructuredDiff = {
      version: "1.0",
      format: "gltf-scene",
      changes: [
        { path: "nodes", label: "nodes", kind: "modified", children: [
          { path: "nodes/Hood", label: "Hood", kind: "modified", children: [
            { path: "nodes/Hood/translation", label: "translation", kind: "modified" },
          ] },
        ] },
        { path: "materials", label: "materials", kind: "modified", children: [
          { path: "materials/Paint", label: "Paint", kind: "modified" },
        ] },
      ],
    };
    expect(geometryChanges(diff)).toEqual([]);
    expect(geometryChanges(undefined)).toEqual([]);
  });
});
