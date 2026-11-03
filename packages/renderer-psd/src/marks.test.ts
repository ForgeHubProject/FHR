import { describe, it, expect } from "vitest";
import type { StructuredDiff } from "@fhr/types";
import { markKey, marksOf } from "./marks.js";
import type { Layout } from "./layout.js";

// The handler's tree for a poster: a retitled text, a sticker moved inside its
// group, a logo added and a caption removed.
const diff: StructuredDiff = {
  version: "1.0",
  format: "psd",
  changes: [
    { path: "layers/10", kind: "modified", label: 'Text "Hi"', children: [{ path: "layers/10/text", kind: "modified", label: "Text", before: "Hello", after: "Hi" }] },
    {
      path: "groups/20", kind: "modified", label: 'Group "Badges"',
      children: [{ path: "layers/21", kind: "modified", label: 'Layer "Sticker"', children: [{ path: "layers/21/position", kind: "modified", label: "Position", before: [300, 20], after: [280, 30] }] }],
    },
    { path: "layers/31", kind: "added", label: 'Layer "Logo"', after: "40 × 40" },
    { path: "layers/8", kind: "removed", label: 'Text "Caption"', before: "Caption" },
    { path: "document", kind: "modified", label: "Document", children: [{ path: "document/resolution", kind: "modified", label: "Resolution", before: "72 ppi", after: "300 ppi" }] },
  ],
};
const base: Layout = { v: 1, w: 400, h: 300, scale: 1, layers: { "10": [40, 72, 120, 48], "21": [300, 20, 60, 60], "8": [40, 150, 200, 20] } };
const head: Layout = { v: 1, w: 400, h: 300, scale: 1, layers: { "10": [40, 72, 48, 48], "21": [280, 30, 60, 60], "31": [-10, 10, 40, 40] } };

describe("outlines", () => {
  const marks = marksOf(diff, base, head);

  it("outlines each layer row on the sides it is on, groups through their layers", () => {
    expect(marks.map((m) => [m.key, m.side, m.kind])).toEqual([
      ["layers/10", "base", "modified"],
      ["layers/10", "head", "modified"],
      ["layers/21", "base", "modified"],
      ["layers/21", "head", "modified"],
      ["layers/31", "head", "added"],
      ["layers/8", "base", "removed"],
    ]);
  });

  it("clips a box to the canvas", () => {
    const logo = marks.find((m) => m.key === "layers/31")!;
    expect([logo.x, logo.y, logo.w, logo.h]).toEqual([0, 10, 30, 40]);
  });

  it("keys a property row by its layer", () => {
    expect(markKey("layers/21/position")).toBe("layers/21");
    expect(markKey("groups/20")).toBeNull();
    expect(markKey("document/resolution")).toBeNull();
  });
});
