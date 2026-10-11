// The change tree as a review surface: summary bar, one highlighted row, click
// and keyboard stepping, and the panel formatting rules on the row itself.
//
// These tests use the dependency-free fake document (fake-dom.ts), so what they
// prove is DOM structure, text and attributes — never layout, and never that a
// real browser dispatches the events. The pointer/keyboard *plumbing* is real
// (listeners are registered and fired); the browser's dispatch is not.

import { describe, it, expect } from "vitest";
import type { MountProps, StructuredDiff } from "@fhr/types";
import { renderDiffTree, stepIntent } from "./dom.js";
import { createFakeDocument, asElement, fakeKey, type FakeElement } from "./fake-dom.js";

const diff: StructuredDiff = {
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
            {
              path: "nodes/Wheel_FL/translation",
              kind: "modified",
              label: "translation",
              before: "[0.00 0.00 0.00]",
              after: "[0.00 0.05 0.00]",
            },
            {
              path: "nodes/Wheel_FL/mesh",
              kind: "modified",
              label: "mesh",
              before: "mesh[3]",
              after: "mesh[5]",
            },
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
            {
              path: "materials/Paint/baseColorFactor",
              kind: "modified",
              label: "baseColorFactor",
              before: "[1.00 0.00 0.00 1.00]",
              after: "[0.00 0.00 1.00 1.00]",
            },
          ],
        },
      ],
    },
  ],
};

function mount(props: Partial<MountProps> = {}, options = {}) {
  const doc = createFakeDocument();
  const container = doc.createElement("div");
  const handle = renderDiffTree(asElement(container), { mode: "diff", diff, ...props }, options);
  return { doc, container, handle, root: handle.root as unknown as FakeElement };
}

const rows = (root: FakeElement): FakeElement[] => root.byClass("fhr-diff__row");
const rowFor = (root: FakeElement, path: string): FakeElement =>
  rows(root).find((r) => r.attributes["data-path"] === path)!;

describe("renderDiffTree — structure", () => {
  it("renders one row per change, deepest first-in-order", () => {
    const { root } = mount();
    expect(rows(root).map((r) => r.attributes["data-path"])).toEqual([
      "nodes",
      "nodes/Wheel_FL",
      "nodes/Wheel_FL/translation",
      "nodes/Wheel_FL/mesh",
      "nodes/Mirror_L",
      "materials",
      "materials/Paint",
      "materials/Paint/baseColorFactor",
    ]);
  });

  it("marks the review stops, not the wrappers or the fields", () => {
    const { root } = mount();
    const stops = rows(root)
      .filter((r) => r.attributes["data-stop"] === "1")
      .map((r) => r.attributes["data-path"]);
    expect(stops).toEqual(["nodes/Wheel_FL", "nodes/Mirror_L", "materials/Paint"]);
  });

  it("exposes the review stops on the handle in display order", () => {
    const { handle } = mount();
    expect(handle.stops).toEqual(["nodes/Wheel_FL", "nodes/Mirror_L", "materials/Paint"]);
  });

  it("still works, and still returns a usable handle, with no options at all", () => {
    const doc = createFakeDocument();
    const container = doc.createElement("div");
    const handle = renderDiffTree(asElement(container), { mode: "diff", diff });
    expect(handle.stops.length).toBe(3);
    expect(handle.selected).toBeNull();
  });

  // The handle is returned before the rows exist on this path, and a caller (a
  // host pushing a selection) can still use it.
  it("returns a handle that stays usable when there is nothing to select", () => {
    const { handle } = mount({ diff: { version: "1.0", format: "x", changes: [] } });
    expect(handle.stops).toEqual([]);
    expect(handle.select("nodes/Cube")).toBe(false);
    expect(handle.select(null)).toBe(true);
    handle.focus();
    handle.dispose();
  });

  it("says so when there is nothing to show", () => {
    const { root } = mount({ diff: undefined });
    expect(root.allText()).toContain("No diff provided.");
    const empty = mount({ diff: { version: "1.0", format: "x", changes: [] } });
    expect(empty.root.allText()).toContain("No changes.");
  });
});

describe("renderDiffTree — summary bar", () => {
  // Counted over the changed objects, not over every row in the tree: three
  // objects changed here, and the wrapper rows above them are not changes a
  // reviewer would count.
  it("counts by kind above the tree", () => {
    const { root } = mount();
    const counts = root.byClass("fhr-diff__count").map((c) => c.textContent);
    expect(counts).toEqual(["− 1 removed", "~ 2 modified"]);
  });

  it("shows a kind it has never heard of rather than dropping it", () => {
    const renamed = {
      version: "1.0",
      format: "x",
      changes: [{ path: "a", kind: "renamed", label: "a" }],
    } as unknown as StructuredDiff;
    const { root } = mount({ diff: renamed });
    const chip = root.byClass("fhr-diff__count")[0]!;
    expect(chip.textContent).toBe("• 1 renamed");
    expect(chip.className).toContain("fhr-diff__count--other");
  });

  it("shows the review position and steps from the nav buttons", () => {
    const steps: number[] = [];
    const { root, handle } = mount({}, { onStep: (d: number) => steps.push(d) });
    const position = root.byClass("fhr-diff__position")[0]!;
    expect(position.textContent).toBe("3 changes");
    handle.select("nodes/Mirror_L");
    expect(position.textContent).toBe("2 / 3");

    const buttons = root.byClass("fhr-diff__step");
    buttons[0]!.fire("click");
    buttons[1]!.fire("click");
    expect(steps).toEqual([-1, 1]);
  });

  it("leaves the nav out when nobody is listening for steps", () => {
    const { root } = mount();
    expect(root.byClass("fhr-diff__step")).toEqual([]);
  });
});

describe("renderDiffTree — selection", () => {
  it("highlights exactly one row at a time", () => {
    const { root, handle } = mount();
    expect(handle.select("nodes/Wheel_FL")).toBe(true);
    expect(rowFor(root, "nodes/Wheel_FL").attributes["aria-selected"]).toBe("true");
    handle.select("nodes/Mirror_L");
    expect(rowFor(root, "nodes/Wheel_FL").attributes["aria-selected"]).toBe("false");
    expect(rowFor(root, "nodes/Mirror_L").attributes["aria-selected"]).toBe("true");
    expect(handle.selected).toBe("nodes/Mirror_L");
  });

  it("clears the highlight for null", () => {
    const { root, handle } = mount();
    handle.select("nodes/Wheel_FL");
    handle.select(null);
    expect(rowFor(root, "nodes/Wheel_FL").attributes["aria-selected"]).toBe("false");
    expect(handle.selected).toBeNull();
  });

  it("reports a selection key that isn't in this diff instead of pretending", () => {
    const { handle } = mount();
    expect(handle.select("nodes/NotHere")).toBe(false);
    expect(handle.selected).toBe("nodes/NotHere");
  });

  it("scrolls a selected row into view when the DOM can", () => {
    const { root, handle } = mount();
    handle.select("materials/Paint");
    expect(rowFor(root, "materials/Paint").scrolled).toBe(1);
  });

  it("applies an initial selection from the options", () => {
    const { root } = mount({}, { selectedPath: "nodes/Mirror_L" });
    expect(rowFor(root, "nodes/Mirror_L").attributes["aria-selected"]).toBe("true");
  });

  it("reports a click through onSelect and does NOT highlight on its own", () => {
    const picked: string[] = [];
    const { root, handle } = mount({}, { onSelect: (p: string) => picked.push(p) });
    rowFor(root, "nodes/Wheel_FL").fire("click");
    expect(picked).toEqual(["nodes/Wheel_FL"]);
    // The owner decides what selection becomes — a 3D viewport and this tree must
    // not each keep their own idea of what is selected.
    expect(handle.selected).toBeNull();
  });

  it("keeps the pre-options behaviour: a click is a host select event", () => {
    const events: unknown[] = [];
    const { root } = mount({ onEvent: (e: unknown) => events.push(e) });
    rowFor(root, "nodes/Wheel_FL").fire("click");
    expect(events).toEqual([{ type: "select", changePath: "nodes/Wheel_FL" }]);
  });

  it("does not double-report a click to both channels", () => {
    const events: unknown[] = [];
    const picked: string[] = [];
    const { root } = mount(
      { onEvent: (e: unknown) => events.push(e) },
      { onSelect: (p: string) => picked.push(p) },
    );
    rowFor(root, "nodes/Wheel_FL").fire("click");
    expect(picked).toEqual(["nodes/Wheel_FL"]);
    expect(events).toEqual([]);
  });
});

describe("renderDiffTree — keyboard", () => {
  it("steps on n/p and the arrows, and swallows the key", () => {
    const steps: number[] = [];
    const { root } = mount({}, { onStep: (d: number) => steps.push(d) });
    for (const key of ["n", "p", "ArrowDown", "ArrowUp"]) {
      const event = fakeKey(key);
      root.fire("keydown", event);
      expect(event.prevented, key).toBe(1);
    }
    expect(steps).toEqual([1, -1, 1, -1]);
  });

  it("leaves other keys — Space is the blink, and belongs to the scene", () => {
    const steps: number[] = [];
    const { root } = mount({}, { onStep: (d: number) => steps.push(d) });
    const event = fakeKey(" ");
    root.fire("keydown", event);
    expect(steps).toEqual([]);
    expect(event.prevented).toBe(0);
  });

  it("is focusable, so the keys work without clicking a row first", () => {
    const { root, handle } = mount({}, { onStep: () => {} });
    expect(root.attributes["tabindex"]).toBe("0");
    handle.focus();
    expect(root.focused).toBe(1);
  });

  it("registers nothing when keyboard handling is declined", () => {
    const { root } = mount({}, { onStep: () => {}, keyboard: false });
    expect(root.listeners["keydown"]).toBeUndefined();
  });

  it("drops every listener on dispose", () => {
    const steps: number[] = [];
    const { root, handle } = mount({}, { onStep: (d: number) => steps.push(d) });
    handle.dispose();
    root.fire("keydown", fakeKey("n"));
    rowFor(root, "nodes/Wheel_FL").fire("click");
    expect(steps).toEqual([]);
  });
});

describe("stepIntent", () => {
  it("maps the review keys", () => {
    expect(stepIntent({ key: "n" })).toBe(1);
    expect(stepIntent({ key: "N" })).toBe(1);
    expect(stepIntent({ key: "p" })).toBe(-1);
    expect(stepIntent({ key: "ArrowDown" })).toBe(1);
    expect(stepIntent({ key: "ArrowUp" })).toBe(-1);
    expect(stepIntent({ key: "x" })).toBe(0);
  });

  it("ignores the arrows when the caller only wants n/p", () => {
    expect(stepIntent({ key: "ArrowDown" }, { arrows: false })).toBe(0);
    expect(stepIntent({ key: "n" }, { arrows: false })).toBe(1);
  });

  it("never steals a key from a text field", () => {
    for (const tagName of ["INPUT", "TEXTAREA", "SELECT"]) {
      expect(stepIntent({ key: "n", target: { tagName } }), tagName).toBe(0);
    }
    expect(stepIntent({ key: "n", target: { tagName: "DIV", isContentEditable: true } })).toBe(0);
    expect(stepIntent({ key: "n", target: { tagName: "DIV" } })).toBe(1);
  });

  it("leaves modifier chords to the browser", () => {
    expect(stepIntent({ key: "n", ctrlKey: true })).toBe(0);
    expect(stepIntent({ key: "n", metaKey: true })).toBe(0);
    expect(stepIntent({ key: "n", altKey: true })).toBe(0);
  });
});

describe("renderDiffTree — panel formatting", () => {
  const cell = (root: FakeElement, path: string): string =>
    rowFor(root, path).byClass("fhr-diff__delta")[0]?.textContent ?? "";

  it("puts the delta and its magnitude in a column of their own", () => {
    const { root } = mount();
    expect(cell(root, "nodes/Wheel_FL/translation")).toBe("Δ(0, 0.05, 0) = 50 mm");
  });

  it("draws colours as chips beside the hex, not as float tuples", () => {
    const { root } = mount();
    const values = rowFor(root, "materials/Paint/baseColorFactor").byClass("fhr-diff__values")[0]!;
    expect(values.byClass("fhr-diff__swatch").map((s) => s.attributes["data-swatch"])).toEqual([
      "rgb(255, 0, 0)",
      "rgb(0, 0, 255)",
    ]);
    expect(values.allText()).toContain("#FF0000");
    expect(values.allText()).toContain("#0000FF");
    expect(values.allText()).not.toContain("1.00");
  });

  it("de-emphasises index churn instead of dressing it up as a change", () => {
    const { root } = mount();
    const row = rowFor(root, "nodes/Wheel_FL/mesh");
    expect(row.attributes["data-noise"]).toBe("1");
    expect(cell(root, "nodes/Wheel_FL/mesh")).toBe("index");
  });

  it("takes a renderer's own formatter when the defaults don't fit", () => {
    const { root } = mount(
      {},
      {
        format: () => ({ kind: "text" as const, before: "b", after: "a", deltaCell: "custom", noise: false }),
      },
    );
    expect(cell(root, "nodes/Wheel_FL/translation")).toBe("custom");
  });
});
