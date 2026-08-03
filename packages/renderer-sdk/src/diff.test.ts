import { describe, it, expect } from "vitest";
import type { StructuredDiff } from "@fhr/types";
import { flattenDiff, diffSummary, countKinds, formatValue, reviewStops, stepIndex } from "./diff.js";

const nested: StructuredDiff = {
  version: "1.0",
  format: "gltf-scene",
  changes: [
    { path: "n0", kind: "added", label: "Cube" },
    {
      path: "n1",
      kind: "modified",
      label: "Lamp",
      children: [
        { path: "n1.pos", kind: "modified", before: [0, 0, 0], after: [1, 0, 0] },
        { path: "n1.rot", kind: "modified", before: 0.0000004, after: 90 },
      ],
    },
    { path: "n2", kind: "removed" },
  ],
};

// A nil Go slice marshals to JSON null, so a diff with no changes can arrive
// as { changes: null } — the SDK must not crash on it.
const nullChanges = { version: "1.0", format: "gltf-scene", changes: null } as unknown as StructuredDiff;

describe("flattenDiff", () => {
  it("flattens depth-first with parents before children", () => {
    const rows = flattenDiff(nested);
    expect(rows.map((r) => r.path)).toEqual(["n0", "n1", "n1.pos", "n1.rot", "n2"]);
  });

  it("treats null changes (nil Go slice over the wire) as empty", () => {
    expect(flattenDiff(nullChanges)).toEqual([]);
    expect(diffSummary(nullChanges)).toMatchObject({ added: 0, removed: 0, modified: 0, total: 0 });
    expect(reviewStops(nullChanges)).toEqual([]);
  });

  it("assigns depth by nesting level", () => {
    const rows = flattenDiff(nested);
    expect(rows.find((r) => r.path === "n1")?.depth).toBe(0);
    expect(rows.find((r) => r.path === "n1.pos")?.depth).toBe(1);
  });

  it("falls back to path when label is absent, and flags children", () => {
    const rows = flattenDiff(nested);
    expect(rows.find((r) => r.path === "n2")?.label).toBe("n2");
    expect(rows.find((r) => r.path === "n1")?.hasChildren).toBe(true);
    expect(rows.find((r) => r.path === "n0")?.hasChildren).toBe(false);
  });

  // Handlers emit fully-qualified paths ("/"-separated, names percent-escaped for
  // "%" and "/"), and flattenDiff must carry them through verbatim: a row's path
  // is the selection key a renderer round-trips against the scene. Composing
  // paths here instead would guess at a separator the handler already chose.
  it("preserves the handler's fully-qualified child paths", () => {
    const fromHandler: StructuredDiff = {
      version: "1.0",
      format: "gltf-scene",
      changes: [
        {
          path: "nodes",
          kind: "modified",
          label: "nodes",
          children: [
            {
              path: "nodes/Cube.001",
              kind: "modified",
              label: "Cube.001",
              children: [{ path: "nodes/Cube.001/translation", kind: "modified", label: "translation" }],
            },
            {
              path: "nodes/rig%2Fhand",
              kind: "modified",
              label: "rig/hand",
              children: [{ path: "nodes/rig%2Fhand/scale", kind: "modified", label: "scale" }],
            },
          ],
        },
      ],
    };

    const rows = flattenDiff(fromHandler);
    expect(rows.map((r) => r.path)).toEqual([
      "nodes",
      "nodes/Cube.001",
      "nodes/Cube.001/translation",
      "nodes/rig%2Fhand",
      "nodes/rig%2Fhand/scale",
    ]);
    // Labels stay raw for display, escaping is confined to the path.
    expect(rows.find((r) => r.path === "nodes/rig%2Fhand")?.label).toBe("rig/hand");
    // Paths are unique, which is what makes them usable as selection keys.
    expect(new Set(rows.map((r) => r.path)).size).toBe(rows.length);
  });
});

describe("diffSummary", () => {
  it("counts every node including nested children", () => {
    const s = diffSummary(nested);
    expect(s).toMatchObject({ added: 1, removed: 1, modified: 3, total: 5 });
  });

  it("is all-zero for an empty diff", () => {
    expect(diffSummary({ version: "1.0", format: "x", changes: [] })).toMatchObject({
      added: 0,
      removed: 0,
      modified: 0,
      total: 0,
      kinds: [],
    });
  });

  // A handler that starts emitting a kind this SDK build has never heard of must
  // still be counted, not silently dropped from the summary bar. "renamed" is the
  // case that actually shipped (#47): it is a real ChangeKind on the wire now,
  // and this SDK still carries it without knowing anything about it — which is
  // why adding it needed no change here at all.
  it("counts kinds it has never heard of, known ones first", () => {
    const withRename = {
      version: "1.0",
      format: "gltf-scene",
      changes: [
        { path: "a", kind: "renamed" },
        { path: "b", kind: "added" },
        { path: "c", kind: "moved" },
        { path: "d", kind: "renamed" },
      ],
    } as unknown as StructuredDiff;
    const s = diffSummary(withRename);
    expect(s.byKind).toEqual({ renamed: 2, added: 1, moved: 1 });
    expect(s.kinds).toEqual(["added", "moved", "renamed"]);
    expect(s.total).toBe(4);
  });

  // The same property, stated against the *typed* wire format rather than a cast:
  // a diff a type-checker accepts must survive this SDK unchanged.
  it("counts a typed renamed change without knowing what a rename is", () => {
    const diff: StructuredDiff = {
      version: "1.0",
      format: "gltf-scene",
      changes: [
        { path: "nodes/Fender", label: "Fender", kind: "renamed", before: "Cube.003", after: "Fender" },
        { path: "nodes/Lamp", label: "Lamp", kind: "added" },
      ],
    };
    expect(diffSummary(diff).byKind["renamed"]).toBe(1);
    expect(countKinds(diff.changes).kinds).toEqual(["added", "renamed"]);
  });

  // "reparented" (#42) is the next kind to ship, and the same carry-through
  // property holds: this is the typed-wire compile check that the union member
  // landed, and the ordering check that unknown kinds sort after the known
  // three.
  it("counts a typed reparented change without knowing what a reparent is", () => {
    const diff: StructuredDiff = {
      version: "1.0",
      format: "gltf-scene",
      changes: [
        {
          path: "nodes/Mirror_L",
          label: "Mirror_L",
          kind: "reparented",
          before: "Body",
          after: "Door_L (matched by structure)",
          children: [
            { path: "nodes/Mirror_L/parent", label: "parent", kind: "modified", before: "Body", after: "Door_L" },
          ],
        },
        { path: "nodes/Lamp", label: "Lamp", kind: "added" },
      ],
    };
    expect(diffSummary(diff).byKind["reparented"]).toBe(1);
    expect(countKinds(diff.changes).kinds).toEqual(["added", "reparented"]);
  });
});

describe("formatValue", () => {
  it("renders undefined as an em dash and null literally", () => {
    expect(formatValue(undefined)).toBe("—");
    expect(formatValue(null)).toBe("null");
  });

  it("trims float noise to 3 decimals and keeps integers exact", () => {
    expect(formatValue(0.0000004)).toBe("0");
    expect(formatValue(1.23456)).toBe("1.235");
    expect(formatValue(42)).toBe("42");
  });

  it("renders arrays shallowly with trimmed numbers", () => {
    expect(formatValue([1, 2.6666, 3])).toBe("[1, 2.667, 3]");
  });

  it("json-stringifies plain objects", () => {
    expect(formatValue({ a: 1 })).toBe('{"a":1}');
  });
});

// The review path: what `n`/`p` steps through. A structured diff nests
// collection wrappers above the things a reviewer thinks of as changes, so the
// stops are the shallowest rows that carry values — for a glTF diff, exactly the
// changed objects, never the "nodes" wrapper and never each field under them.
describe("reviewStops", () => {
  const gltfShaped: StructuredDiff = {
    version: "1.0",
    format: "gltf-scene",
    changes: [
      {
        path: "nodes",
        kind: "modified",
        label: "nodes",
        children: [
          {
            path: "nodes/Wheel_FL",
            kind: "modified",
            label: "Wheel_FL",
            children: [
              { path: "nodes/Wheel_FL/translation", kind: "modified", label: "translation", before: "[0 0 0]", after: "[0 0.05 0]" },
              { path: "nodes/Wheel_FL/mesh", kind: "modified", label: "mesh", before: "mesh[3]", after: "mesh[5]" },
            ],
          },
          { path: "nodes/Mirror_L", kind: "removed", label: "Mirror_L" },
        ],
      },
      {
        path: "materials",
        kind: "modified",
        label: "materials",
        children: [
          {
            path: "materials/Paint",
            kind: "modified",
            label: "Paint",
            children: [
              { path: "materials/Paint/baseColorFactor", kind: "modified", label: "baseColorFactor", before: "[1 0 0 1]", after: "[0 0 1 1]" },
            ],
          },
        ],
      },
    ],
  };

  it("stops on the changed objects, not the collection wrappers", () => {
    expect(reviewStops(gltfShaped).map((s) => s.row.path)).toEqual([
      "nodes/Wheel_FL",
      "nodes/Mirror_L",
      "materials/Paint",
    ]);
  });

  it("keeps a stop's own value rows as its details", () => {
    const stops = reviewStops(gltfShaped);
    expect(stops[0]?.details.map((d) => d.label)).toEqual(["translation", "mesh"]);
    expect(stops[1]?.details).toEqual([]);
    expect(stops[0]?.row.kind).toBe("modified");
    expect(stops[1]?.row.kind).toBe("removed");
  });

  it("stops on a flat diff's own rows", () => {
    const flat: StructuredDiff = {
      version: "1.0",
      format: "csv",
      changes: [
        { path: "r1", kind: "added" },
        { path: "r2", kind: "removed" },
      ],
    };
    expect(reviewStops(flat).map((s) => s.row.path)).toEqual(["r1", "r2"]);
  });

  it("descends to the leaves when nothing above them carries a value", () => {
    const deep: StructuredDiff = {
      version: "1.0",
      format: "x",
      changes: [
        {
          path: "a",
          kind: "modified",
          children: [
            {
              path: "a/b",
              kind: "modified",
              children: [{ path: "a/b/c", kind: "modified", children: [{ path: "a/b/c/d", kind: "modified" }] }],
            },
          ],
        },
      ],
    };
    expect(reviewStops(deep).map((s) => s.row.path)).toEqual(["a/b/c/d"]);
  });
});

describe("stepIndex", () => {
  it("steps forward and backward, wrapping at both ends", () => {
    expect(stepIndex(3, 0, 1)).toBe(1);
    expect(stepIndex(3, 2, 1)).toBe(0);
    expect(stepIndex(3, 0, -1)).toBe(2);
  });

  it("starts at the first change going forward and the last going back", () => {
    expect(stepIndex(3, -1, 1)).toBe(0);
    expect(stepIndex(3, -1, -1)).toBe(2);
  });

  it("has nowhere to go in an empty list", () => {
    expect(stepIndex(0, -1, 1)).toBe(-1);
  });
});
