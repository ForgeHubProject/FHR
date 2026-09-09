// The queue region on its own, headless. chrome.test.ts covers how the region
// sits in the layout and what a click on it reports; this file covers what it
// does with a worklist longer than a review session — the case #56 introduced by
// building a row per change inside the 3D mount, where main built none.

import { describe, it, expect } from "vitest";
import { createFakeDocument, asDocument, type FakeElement } from "./fake-dom.js";
import { renderQueue, MAX_ROWS } from "./change-queue.js";
import type { QueueEntry } from "./queue.js";

const entry = (i: number): QueueEntry => ({
  path: `nodes/Part_${i}`,
  label: `Part_${i}`,
  kind: "modified",
  headline: "moved 50 mm",
  details: [{ label: "translation", before: "[0 0 0]", after: "[0 0 0.05]", delta: "+50 mm" }],
});

function render(entries: QueueEntry[]) {
  const doc = createFakeDocument();
  const selected: string[] = [];
  const view = renderQueue(asDocument(doc), entries, {
    onSelect: (p) => selected.push(p),
    onStep: () => {},
  });
  const el = view.el as unknown as FakeElement;
  return {
    view,
    el,
    selected,
    rows: () => el.byClass("fhr3d__stop"),
    position: () => el.byAttr("data-position", "1")[0]!.textContent,
    panel: () => el.byAttr("data-panel", "1")[0]!,
    note: () => el.descendants().filter((e) => e.attributes["data-truncated"] !== undefined),
  };
}

describe("renderQueue", () => {
  it("lists the whole worklist when it fits under the cap", () => {
    const { rows, note } = render([entry(0), entry(1)]);
    expect(rows()).toHaveLength(2);
    expect(note()).toEqual([]);
  });

  it("caps the rows it builds, so a huge diff can't freeze the mount", () => {
    // Four elements and a listener per row, built synchronously before the canvas
    // exists. Nothing upstream bounds the change count: limits.ts caps blob
    // bytes, and gltf-preflight.ts counts nodes and changes not at all.
    const entries = Array.from({ length: MAX_ROWS * 2 }, (_, i) => entry(i));
    const { rows, note } = render(entries);
    expect(rows()).toHaveLength(MAX_ROWS);
    expect(note()).toHaveLength(1);
    expect(note()[0]!.getAttribute("data-truncated")).toBe(String(MAX_ROWS));
    // The omission is stated, not silent.
    expect(note()[0]!.textContent).toContain(String(MAX_ROWS * 2));
  });

  it("keeps the position readout and the panel exact past the cap", () => {
    // The readout is the organising principle of the region, so the cap must cost
    // a row and never a place: it is the thing that tells a reviewer how much of
    // the job is left, and it is keyed on the whole queue.
    const entries = Array.from({ length: MAX_ROWS * 2 }, (_, i) => entry(i));
    const { view, position, panel, rows } = render(entries);
    expect(position()).toBe(`${MAX_ROWS * 2} changes`);

    const late = entries[MAX_ROWS * 2 - 1]!;
    view.select(late.path);
    expect(position()).toBe(`${MAX_ROWS * 2} / ${MAX_ROWS * 2}`);
    expect(panel().allText()).toContain(late.label);
    expect(panel().allText()).toContain("+50 mm");
    // No row to highlight — that, and only that, is what the cap costs.
    expect(rows().filter((r) => r.getAttribute("aria-selected") === "true")).toEqual([]);
  });

  it("still highlights a row the cap kept, and clears it again", () => {
    const entries = Array.from({ length: MAX_ROWS * 2 }, (_, i) => entry(i));
    const { view, el, position } = render(entries);
    view.select("nodes/Part_0");
    expect(el.byAttr("data-path", "nodes/Part_0")[0]!.getAttribute("aria-selected")).toBe("true");
    expect(position()).toBe(`1 / ${MAX_ROWS * 2}`);
    view.select(null);
    expect(el.byAttr("data-path", "nodes/Part_0")[0]!.getAttribute("aria-selected")).toBe("false");
    expect(position()).toBe(`${MAX_ROWS * 2} changes`);
  });

  it("says there is nothing to review rather than rendering an empty list", () => {
    const { el } = render([]);
    expect(el.byClass("fhr3d__empty")[0]!.textContent).toContain("Nothing to review");
  });
});
