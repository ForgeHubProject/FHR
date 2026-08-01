// The structure region on its own, headless. chrome.test.ts covers how the
// region sits in the layout; this file covers what the region does with a node
// list that is larger than a person can read — which is the case #56 introduced
// by putting the tree on every mount instead of only the failed-decode fallback.

import { describe, it, expect } from "vitest";
import { createFakeDocument, asDocument, type FakeElement } from "./fake-dom.js";
import { renderStructureTree, MAX_ROWS } from "./structure-tree.js";
import type { SceneNode } from "./scene-graph.js";

const node = (name: string, kind: SceneNode["kind"] = "unchanged", depth = 0): SceneNode => ({
  id: name,
  name,
  depth,
  position: [0, 0, 0],
  rotationEulerDeg: [0, 0, 0],
  scale: [1, 1, 1],
  color: 0,
  kind,
});

function render(nodes: SceneNode[]) {
  const doc = createFakeDocument();
  const picked: string[] = [];
  const tree = renderStructureTree(asDocument(doc), nodes, { onPick: (n) => picked.push(n) });
  const el = tree.el as unknown as FakeElement;
  return { tree, el, picked, rows: () => el.byClass("fhr3d__node") };
}

describe("renderStructureTree", () => {
  it("lists every node when the model fits under the cap", () => {
    const { el, rows } = render([node("Car"), node("Wheel", "modified", 1)]);
    expect(rows().map((r) => r.attributes["data-node"])).toEqual(["Car", "Wheel"]);
    expect(el.descendants().filter((e) => e.attributes["data-truncated"] !== undefined)).toEqual([]);
  });

  it("caps the rows it builds, so a huge assembly can't freeze the mount", () => {
    // Three elements and a listener per row, built synchronously before the
    // canvas exists: 50 000 nodes was 150 000 live elements and ~0.5 s of
    // blocking DOM work. Nothing upstream bounds the node count.
    const nodes = Array.from({ length: MAX_ROWS * 3 }, (_, i) => node(`Part_${i}`));
    const { el, rows } = render(nodes);
    expect(rows()).toHaveLength(MAX_ROWS);
    const note = el.descendants().filter((e) => e.attributes["data-truncated"] !== undefined);
    expect(note).toHaveLength(1);
    expect(note[0]!.getAttribute("data-truncated")).toBe(String(MAX_ROWS * 2));
    // The omission is stated, not silent.
    expect(note[0]!.textContent).toContain(String(MAX_ROWS * 3));
  });

  it("keeps every changed node when it caps, wherever it sits in the file", () => {
    // The queue asks select() for changed names; a changed row dropped by the
    // cap would break that link silently rather than merely shorten a list.
    const nodes = Array.from({ length: MAX_ROWS * 2 }, (_, i) => node(`Part_${i}`));
    const late = MAX_ROWS * 2 - 1;
    nodes[late] = node(`Part_${late}`, "removed");
    const { tree, el, picked, rows } = render(nodes);

    expect(rows()).toHaveLength(MAX_ROWS);
    expect(rows().map((r) => r.attributes["data-node"])).toContain(`Part_${late}`);
    // Rows stay in document order: the kept change is last, not hoisted.
    expect(rows()[MAX_ROWS - 1]!.attributes["data-node"]).toBe(`Part_${late}`);
    expect(tree.select(`Part_${late}`)).toBe(true);
    expect(el.byAttr("data-node", `Part_${late}`)[0]!.getAttribute("aria-selected")).toBe("true");
    // …and it is still a click target.
    el.byAttr("data-node", `Part_${late}`)[0]!.fire("click");
    expect(picked).toEqual([`Part_${late}`]);
  });

  it("reports a miss for a node the cap dropped rather than throwing", () => {
    const nodes = Array.from({ length: MAX_ROWS * 2 }, (_, i) => node(`Part_${i}`));
    const { tree } = render(nodes);
    expect(tree.select(`Part_${MAX_ROWS * 2 - 1}`)).toBe(false);
  });

  it("says the scene graph is empty rather than rendering nothing", () => {
    const { el } = render([]);
    expect(el.byClass("fhr3d__empty")[0]!.textContent).toContain("empty");
  });
});
