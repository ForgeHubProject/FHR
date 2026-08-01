import { describe, it, expect } from "vitest";
import type { StructuredDiff } from "@fhr/types";
import { diffChangeTypes, hasTransformChange, isTransformOnly, nodeChanges } from "./diff-map.js";

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

  // Since #47 one name can carry two changes about two different nodes: the
  // previous version's "B" was deleted, and an unrelated node was renamed *to*
  // "B". The handler keeps them apart by path — the removal takes the `#1` suffix
  // — and merging them by name kept only the rename, folded the dead node's field
  // labels into it, and lost the deletion entirely.
  it("keeps a deletion whose name a rename took over", () => {
    const diff = diffOf(
      { path: "nodes/B", label: "B", kind: "renamed", before: "A", after: "B (matched by fhr_uid)" },
      {
        path: "nodes/B#1",
        label: "B",
        kind: "removed",
        before: "node",
        children: [
          { path: "nodes/B#1/translation", label: "translation", kind: "removed", before: "[2 0 0]" },
          { path: "nodes/B#1/mesh", label: "mesh", kind: "removed", before: "BodyMesh" },
        ],
      },
    );
    expect(nodeChanges(diff)).toEqual([
      { name: "B", kind: "renamed", fields: [], path: "nodes/B", oldName: "A" },
      { name: "B", kind: "removed", fields: ["translation", "mesh"], path: "nodes/B#1" },
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

describe("renamed nodes (#47)", () => {
  it("keys the change on the head name and carries the base name alongside", () => {
    const diff = diffOf({
      path: "nodes/Fender",
      label: "Fender",
      kind: "renamed",
      before: "Cube.003",
      after: "Fender (matched by content, ~91% similar)",
      children: [
        { path: "nodes/Fender/translation", label: "translation", kind: "modified", before: "[0 0 0]", after: "[0 0 1]" },
      ],
    });
    expect(nodeChanges(diff)).toEqual([
      {
        name: "Fender",
        oldName: "Cube.003",
        kind: "renamed",
        fields: ["translation"],
        path: "nodes/Fender",
      },
    ]);
  });

  it("never reads the evidence parenthetical as part of a name", () => {
    // `after` is the new name plus how the handler matched it; the new name comes
    // from `label`, so nothing has to parse that string back apart.
    const diff = diffOf({
      path: "nodes/Fender",
      label: "Fender",
      kind: "renamed",
      before: "Cube.003",
      after: "Fender (matched by fhr_uid)",
    });
    const [change] = nodeChanges(diff);
    expect(change!.name).toBe("Fender");
    expect(change!.oldName).toBe("Cube.003");
  });

  it("leaves oldName off every other kind", () => {
    const diff = diffOf(
      { path: "nodes/Lamp", label: "Lamp", kind: "added", after: "node" },
      { path: "nodes/Mirror", label: "Mirror", kind: "removed", before: "node" },
    );
    for (const change of nodeChanges(diff)) expect(change.oldName).toBeUndefined();
  });

  it("reports a rename that also moved as a transform change", () => {
    const diff = diffOf({
      path: "nodes/Fender",
      label: "Fender",
      kind: "renamed",
      before: "Cube.003",
      after: "Fender (matched by fhr_uid)",
      children: [{ path: "nodes/Fender/translation", label: "translation", kind: "modified" }],
    });
    const [change] = nodeChanges(diff);
    // Not "transform only" — the rename is a change in its own right — but the
    // move grammar still applies, so the overlay draws the old pose and a vector.
    expect(hasTransformChange(change!)).toBe(true);
    expect(isTransformOnly(change!)).toBe(false);
    expect(diffChangeTypes(diff).get("Fender")).toBe("renamed");
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
