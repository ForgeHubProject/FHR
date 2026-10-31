import { describe, it, expect } from "vitest";
import type { StructuredDiff } from "@fhr/types";
import { markKey, screenViews } from "./screens.js";
import type { Sheet } from "./sheet.js";

// The handler's grouping for a refresh of two screens, a new screen, and a style.
const diff: StructuredDiff = {
  version: "1.0",
  format: "figcraft",
  changes: [
    {
      path: "pages/1", kind: "modified", label: 'Page "Onboarding"',
      children: [
        {
          path: "screens/2", kind: "modified", label: 'Screen "1 Welcome"',
          children: [
            { path: "layers/2", kind: "modified", label: 'Frame "1 Welcome"', children: [{ path: "layers/2/fill-colour", kind: "modified", label: "Fill colour", before: [1, 1, 1, 1], after: [0, 0, 0, 1] }] },
            { path: "layers/18", kind: "modified", label: 'Text "Title"', children: [{ path: "layers/18/text", kind: "modified", label: "Text", before: "a", after: "b" }] },
            { path: "layers/146", kind: "added", label: 'Rectangle "Badge"', after: "60 × 24" },
            { path: "layers/23", kind: "removed", label: 'Text "Link"', before: "Link" },
            {
              path: "screens/2/layout", kind: "modified", label: "Moved or resized by layout",
              children: [{ path: "layout/22", kind: "modified", label: 'Text "Go"', children: [{ path: "layout/22/width", kind: "modified", label: "Width", before: 83, after: 86 }] }],
            },
          ],
        },
        { path: "layers/59", kind: "renamed", label: 'Frame "Day trips"', children: [{ path: "layers/59/name", kind: "modified", label: "Name", before: "Day hikes", after: "Day trips" }] },
        { path: "layers/144", kind: "added", label: 'Frame "5 Notifications"', after: "393 × 852" },
      ],
    },
    { path: "document", kind: "modified", label: "Document", children: [{ path: "document/styles/S:1", kind: "added", label: 'Style "Brand"' }] },
  ],
};

const screen = (id: number, name: string, layers: Record<string, [number, number, number, number]>) => ({ id, name, page: 1, pageName: "Onboarding", x: 0, y: 0, w: 393, h: 852, layers });
const base: Sheet = {
  v: 1, scale: 1,
  screens: [
    screen(2, "1 Welcome", { "2": [0, 0, 393, 852], "18": [28, 526, 314, 36], "23": [28, 729, 337, 18], "22": [155, 675, 83, 21] }),
    screen(24, "2 Interests", { "24": [0, 0, 393, 852], "59": [24, 392, 166, 109] }),
  ],
};
const head: Sheet = {
  v: 1, scale: 1,
  screens: [
    screen(2, "1 Welcome", { "2": [0, 0, 393, 852], "18": [28, 526, 287, 36], "146": [28, 729, 60, 24], "22": [153, 675, 86, 21] }),
    screen(24, "2 Interests", { "24": [0, 0, 393, 852], "59": [24, 394, 166, 109] }),
    screen(144, "5 Notifications", { "144": [0, 0, 393, 852] }),
  ],
};

describe("the diff, by screen", () => {
  const { screens, other } = screenViews(diff, base, head);

  it("lists the changed screens in document order, with what is new or gone", () => {
    expect(screens.map((s) => [s.name, s.status, s.count])).toEqual([
      ["1 Welcome", "modified", 4],
      ["2 Interests", "modified", 1],
      ["5 Notifications", "added", 1],
    ]);
    expect(other.map((c) => c.path)).toEqual(["document"]);
  });

  it("gives each screen its own part of the tree", () => {
    expect(screens[0]!.changes.map((c) => c.path)).toEqual(["layers/2", "layers/18", "layers/146", "layers/23", "screens/2/layout"]);
    // A screen where one layer changed, collapsed under the page, is still found by its layer.
    expect(screens[1]!.changes.map((c) => c.path)).toEqual(["layers/59"]);
  });

  it("outlines each change on the sides it is on", () => {
    const marks = screens[0]!.marks.map((m) => [m.key, m.side, m.kind, m.w]);
    expect(marks).toEqual([
      ["layers/2", "base", "modified", 393],
      ["layers/2", "head", "modified", 393],
      ["layers/18", "base", "modified", 314],
      ["layers/18", "head", "modified", 287],
      ["layers/146", "head", "added", 60],
      ["layers/23", "base", "removed", 337],
      ["layout/22", "base", "layout", 83],
      ["layout/22", "head", "layout", 86],
    ]);
    // A whole new screen is its image: nothing to outline on it.
    expect(screens[2]!.marks).toEqual([]);
  });

  it("clips an outline to the screen, and drops one wholly off it", () => {
    const off = (box: [number, number, number, number]): Sheet => ({ v: 1, scale: 1, screens: [screen(2, "1 Welcome", { "2": [0, 0, 393, 852], "18": box })] });
    const titleOn = (s: Sheet) => screenViews(diff, s, s).screens[0]!.marks.filter((m) => m.key === "layers/18").map((m) => [m.x, m.y, m.w, m.h]);
    expect(titleOn(off([320, 90, 120, 120]))).toEqual([[320, 90, 73, 120], [320, 90, 73, 120]]);
    expect(titleOn(off([400, 90, 120, 120]))).toEqual([]);
  });

  it("puts everything on no screen when there are no previews", () => {
    const bare = screenViews(diff, null, null);
    expect(bare.screens).toEqual([]);
    expect(bare.other.map((c) => c.path)).toEqual(["screens/2", "layers/59", "layers/144", "document"]);
  });

  it("keys a change path to its layer's outline", () => {
    expect(markKey("layers/18/text")).toBe("layers/18");
    expect(markKey("layout/22")).toBe("layout/22");
    expect(markKey("screens/2/layout")).toBeNull();
    expect(markKey("document/styles/S:1")).toBeNull();
  });
});

describe("a page added or removed whole", () => {
  // A new file: its page is one added row, and the document's styles another.
  const added: StructuredDiff = {
    version: "1.0",
    format: "figcraft",
    changes: [
      { path: "pages/1", kind: "added", label: 'Page "Foundations"', after: "362 layers" },
      { path: "document", kind: "modified", label: "Document", children: [{ path: "document/styles/S:1", kind: "added", label: 'Style "Pine/50"' }] },
    ],
  };
  const sheet: Sheet = {
    v: 1, scale: 1,
    screens: [
      { ...screen(2, "Foundations", { "2": [0, 0, 1440, 3008], "3": [40, 40, 200, 40] }), w: 1440, h: 3008 },
      { ...screen(363, "Export/Colour ramps", { "363": [0, 0, 1392, 549] }), x: 1440, w: 1392, h: 549 },
      { ...screen(400, "Elsewhere", { "400": [0, 0, 10, 10] }), page: 9 },
    ],
  };

  it("shows each of its screens, new and whole", () => {
    const { screens, other } = screenViews(added, null, sheet);
    expect(screens.map((s) => [s.name, s.status, s.marks.length])).toEqual([
      ["Foundations", "added", 0],
      ["Export/Colour ramps", "added", 0],
    ]);
    expect(screens[0]!.changes).toEqual([{ path: "layers/2", kind: "added", label: 'Screen "Foundations"', after: "2 layers" }]);
    expect(screens[0]!.head).toBe(sheet.screens[0]);
    expect(screens[0]!.base).toBeNull();
    expect(other.map((c) => c.path)).toEqual(["document"]);
  });

  it("shows a deleted page's screens as removed, from the sheet before", () => {
    const removed: StructuredDiff = { ...added, changes: [{ path: "pages/1", kind: "removed", label: 'Page "Foundations"', before: "362 layers" }] };
    const { screens, other } = screenViews(removed, sheet, null);
    expect(screens.map((s) => [s.name, s.status])).toEqual([
      ["Foundations", "removed"],
      ["Export/Colour ramps", "removed"],
    ]);
    expect(screens[1]!.changes[0]).toMatchObject({ kind: "removed", before: "1 layer" });
    expect(other).toEqual([]);
  });

  it("keeps a page with nothing drawn on the sheet as a row", () => {
    const { screens, other } = screenViews(added, null, { v: 1, scale: 1, screens: [] });
    expect(screens).toEqual([]);
    expect(other.map((c) => c.path)).toEqual(["pages/1", "document"]);
  });
});
