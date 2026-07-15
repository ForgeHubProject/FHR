import { describe, it, expect } from "vitest";
import type { StructuredDiff } from "@fhr/types";
import { flattenDiff, diffSummary, formatValue } from "./diff.js";

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
    expect(diffSummary(nullChanges)).toEqual({ added: 0, removed: 0, modified: 0, total: 0 });
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
});

describe("diffSummary", () => {
  it("counts every node including nested children", () => {
    const s = diffSummary(nested);
    expect(s).toEqual({ added: 1, removed: 1, modified: 3, total: 5 });
  });

  it("is all-zero for an empty diff", () => {
    expect(diffSummary({ version: "1.0", format: "x", changes: [] })).toEqual({
      added: 0,
      removed: 0,
      modified: 0,
      total: 0,
    });
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
