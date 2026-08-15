// The three-region chrome, headless.
//
// What a fake DOM can prove is exactly what the decided layout is about:
// that the regions exist and stay put, that the toggle offers what the mount can
// honestly show, that a region folds by hand and by width (tree first), and that
// nothing about a mode switch touches the selection. What it cannot prove is
// that any of it *looks* right — that needs a browser, and so does every pixel
// the WebGL half draws.

import { describe, it, expect } from "vitest";
import { createFakeDocument, asElement, type FakeElement } from "./fake-dom.js";
import { createChrome, FRAME_LABEL, FRAME_TITLE, type ChromeOptions } from "./chrome.js";
import { TREE_MIN_WIDTH, QUEUE_MIN_WIDTH } from "./chrome-layout.js";
import { MODE_ORDER, type PresentationMode } from "./presentation.js";
import type { SceneNode } from "./scene-graph.js";
import type { QueueEntry } from "./queue.js";

const node = (name: string, depth: number, kind: SceneNode["kind"]): SceneNode => ({
  id: name,
  name,
  depth,
  position: [0, 0, 0],
  rotationEulerDeg: [0, 0, 0],
  scale: [1, 1, 1],
  color: 0,
  kind,
});

const structure: SceneNode[] = [
  node("Car", 0, "unchanged"),
  node("Wheel_FL", 1, "modified"),
  node("Wheel_FR", 1, "unchanged"),
  node("Mirror_L", 1, "removed"),
];

const queue: QueueEntry[] = [
  {
    path: "nodes/Wheel_FL",
    label: "Wheel_FL",
    kind: "modified",
    headline: "moved 50 mm",
    details: [{ label: "translation", before: "[0 0 0]", after: "[0 0 0.05]", delta: "+50 mm" }],
  },
  { path: "nodes/Mirror_L", label: "Mirror_L", kind: "removed", headline: "removed", details: [] },
];

type Calls = {
  modes: PresentationMode[];
  splits: string[];
  selected: string[];
  steps: number[];
  nodes: string[];
  frames: number;
};

function setup(overrides: Partial<ChromeOptions> = {}) {
  const doc = createFakeDocument();
  const container = doc.createElement("div");
  const calls: Calls = { modes: [], splits: [], selected: [], steps: [], nodes: [], frames: 0 };
  const chrome = createChrome(asElement(container), {
    modes: [...MODE_ORDER],
    mode: "structural",
    split: "columns",
    structure,
    queue,
    info: "4 nodes · 2 changes",
    onMode: (m) => calls.modes.push(m),
    onSplit: (s) => calls.splits.push(s),
    onQueueSelect: (p) => calls.selected.push(p),
    onStep: (d) => calls.steps.push(d),
    onNode: (n) => calls.nodes.push(n),
    onFrameAll: () => {
      calls.frames += 1;
    },
    ...overrides,
  });
  chrome.applyWidth(TREE_MIN_WIDTH + 200);
  return { container, chrome, calls };
}

const region = (container: FakeElement, key: string): FakeElement => container.byAttr("data-region", key)[0]!;
const stateOf = (container: FakeElement, key: string): string | null =>
  region(container, key).getAttribute("data-state");
const modeButton = (container: FakeElement, mode: string): FakeElement =>
  container.byAttr("data-mode", mode).find((e) => e.tagName === "BUTTON")!;
const stopRow = (container: FakeElement, path: string): FakeElement =>
  container.byAttr("data-path", path)[0]!;
const nodeRow = (container: FakeElement, name: string): FakeElement =>
  container.byAttr("data-node", name)[0]!;
const selectedStops = (container: FakeElement): string[] =>
  container
    .byAttr("aria-selected", "true")
    .filter((e) => e.className === "fhr3d__stop")
    .map((e) => e.attributes["data-path"]!);

describe("three regions", () => {
  it("builds structure, viewport and queue, in that order", () => {
    const { container } = setup();
    const regions = container
      .descendants()
      .filter((e) => e.attributes["data-region"] !== undefined)
      .map((e) => e.attributes["data-region"]);
    expect(regions).toEqual(["tree", "viewport", "queue"]);
  });

  it("puts the file info and the view options in the centre's top chrome", () => {
    const { container } = setup();
    const centre = region(container, "viewport");
    expect(centre.byAttr("data-info", "1")[0]!.textContent).toBe("4 nodes · 2 changes");
    expect(centre.byAttr("data-options", "1")).toHaveLength(1);
    // The viewport itself is inside the centre, under the bar.
    expect(centre.byAttr("data-viewport", "1")).toHaveLength(1);
  });

  it("shows the whole model in the tree, unchanged nodes included", () => {
    const { container } = setup();
    const names = region(container, "tree")
      .byClass("fhr3d__node")
      .map((e) => e.attributes["data-node"]);
    expect(names).toEqual(["Car", "Wheel_FL", "Wheel_FR", "Mirror_L"]);
    // Unreachable from the change list, which is the reason the region exists.
    expect(nodeRow(container, "Wheel_FR").getAttribute("data-kind")).toBe("unchanged");
    expect(nodeRow(container, "Mirror_L").getAttribute("data-kind")).toBe("removed");
  });

  it("indents the tree by depth", () => {
    const { container } = setup();
    expect(nodeRow(container, "Car").style.paddingLeft).not.toBe(
      nodeRow(container, "Wheel_FL").style.paddingLeft,
    );
  });

  it("omits the queue region entirely when there is nothing to review", () => {
    // "view" mode: a region holding "no changes" spends width the viewport needs.
    const { container } = setup({ queue: [] });
    expect(container.byAttr("data-region", "queue")).toEqual([]);
    expect(container.byAttr("data-region", "tree")).toHaveLength(1);
  });
});

describe("the queue region", () => {
  it("promotes the position readout and the ‹ › stepping into the region", () => {
    const { container, calls } = setup();
    const queueRegion = region(container, "queue");
    expect(queueRegion.byAttr("data-position", "1")[0]!.textContent).toBe("2 changes");
    for (const delta of ["-1", "1"]) queueRegion.byAttr("data-step", delta)[0]!.fire("click");
    expect(calls.steps).toEqual([-1, 1]);
  });

  it("lists the changes in review order, with their headlines", () => {
    const { container } = setup();
    const rows = region(container, "queue").byClass("fhr3d__stop");
    expect(rows.map((r) => r.attributes["data-path"])).toEqual(["nodes/Wheel_FL", "nodes/Mirror_L"]);
    expect(rows[0]!.allText()).toContain("moved 50 mm");
  });

  it("reports a row click rather than selecting itself", () => {
    // Selection has one owner (live-view.ts); a region that highlighted itself
    // would give the reviewer two places that disagree about where they are.
    const { container, calls } = setup();
    stopRow(container, "nodes/Mirror_L").fire("click");
    expect(calls.selected).toEqual(["nodes/Mirror_L"]);
    expect(selectedStops(container)).toEqual([]);
  });

  it("moves the position and fills the panel when it is told to select", () => {
    const { container, chrome } = setup();
    chrome.selectChange("nodes/Wheel_FL");
    expect(selectedStops(container)).toEqual(["nodes/Wheel_FL"]);
    expect(region(container, "queue").byAttr("data-position", "1")[0]!.textContent).toBe("1 / 2");
    const panel = container.byAttr("data-panel", "1")[0]!;
    expect(panel.allText()).toContain("translation");
    expect(panel.allText()).toContain("+50 mm");
  });

  it("clears back to the size of the job", () => {
    const { container, chrome } = setup();
    chrome.selectChange("nodes/Wheel_FL");
    chrome.selectChange(null);
    expect(selectedStops(container)).toEqual([]);
    expect(container.byAttr("data-position", "1")[0]!.textContent).toBe("2 changes");
    expect(container.byAttr("data-panel", "1")[0]!.childNodes).toEqual([]);
  });
});

describe("the mode toggle", () => {
  it("offers the ladder and marks where the view is", () => {
    const { container } = setup();
    const buttons = container.byAttr("data-modes", "1")[0]!.byClass("fhr3d__mode");
    expect(buttons.map((b) => b.attributes["data-mode"])).toEqual([...MODE_ORDER]);
    expect(modeButton(container, "structural").getAttribute("aria-pressed")).toBe("true");
  });

  it("reports a click and follows when it is told the mode changed", () => {
    const { container, chrome, calls } = setup();
    modeButton(container, "side-by-side").fire("click");
    expect(calls.modes).toEqual(["side-by-side"]);
    chrome.setMode("side-by-side");
    expect(modeButton(container, "side-by-side").getAttribute("aria-pressed")).toBe("true");
    expect(modeButton(container, "structural").getAttribute("aria-pressed")).toBe("false");
  });

  it("has no toggle at all when only one mode is available", () => {
    // The previous version didn't load: a switch with one position is furniture.
    const { container } = setup({ modes: ["structural"] });
    expect(container.byAttr("data-modes", "1")).toEqual([]);
  });

  it("shows the split control only in side-by-side, and names the other way", () => {
    const { container, chrome, calls } = setup();
    const button = container.byAttr("data-split", "1")[0]!;
    expect(button.getAttribute("hidden")).toBe("hidden");
    chrome.setMode("side-by-side");
    expect(button.getAttribute("hidden")).toBeNull();
    expect(button.textContent).toBe("Split top / bottom");
    button.fire("click");
    expect(calls.splits).toEqual(["rows"]);
    chrome.setSplit("rows");
    expect(button.textContent).toBe("Split left / right");
  });
});

describe("the deviation toggle", () => {
  const heatButton = (container: FakeElement): FakeElement | undefined =>
    container.byAttr("data-heatmap", "1")[0];

  it("is absent — not disabled — when there is nothing to measure", () => {
    // No vertex-data edit, or no previous version. A control that is there but
    // refuses cannot be told apart from a broken one; the banners already say
    // why the previous version is missing, so nothing is left unexplained.
    expect(heatButton(setup().container)).toBeUndefined();
    expect(heatButton(setup({ heatmap: false }).container)).toBeUndefined();
  });

  it("appears only in overlay, the mode it is a sub-view of", () => {
    const { container, chrome } = setup({ heatmap: true });
    const button = heatButton(container)!;
    expect(button.getAttribute("hidden")).toBe("hidden");
    chrome.setMode("overlay");
    expect(button.getAttribute("hidden")).toBeNull();
    chrome.setMode("side-by-side");
    expect(button.getAttribute("hidden")).toBe("hidden");
  });

  it("reports each toggle once and tracks its own pressed state", () => {
    const asked: boolean[] = [];
    const { container, chrome } = setup({ heatmap: true, onHeatmap: (on) => asked.push(on) });
    const button = heatButton(container)!;
    chrome.setMode("overlay");
    expect(button.getAttribute("aria-pressed")).toBe("false");
    button.fire("click");
    expect(asked).toEqual([true]);
    expect(button.getAttribute("aria-pressed")).toBe("true");
    button.fire("click");
    expect(asked).toEqual([true, false]);
    expect(button.getAttribute("aria-pressed")).toBe("false");
  });

  it("keeps its pressed state across a trip through another mode", () => {
    // The scene suspends the heatmap while the reviewer is elsewhere and brings
    // it back on return, so the button must not forget — a reset one would
    // disagree with the picture the moment they came back.
    const asked: boolean[] = [];
    const { container, chrome } = setup({ heatmap: true, onHeatmap: (on) => asked.push(on) });
    const button = heatButton(container)!;
    chrome.setMode("overlay");
    button.fire("click");
    chrome.setMode("side-by-side");
    chrome.setMode("overlay");
    expect(button.getAttribute("aria-pressed")).toBe("true");
    expect(asked).toEqual([true]);
  });

  it("puts a measured deviation on the selected change's panel row", () => {
    const { container, chrome } = setup({ heatmap: true });
    chrome.selectChange("nodes/Wheel_FL");
    expect(container.byAttr("data-field", "max deviation")).toHaveLength(0);
    chrome.setDeviations(new Map([["nodes/Wheel_FL", "12.0 mm"]]));
    const row = container.byAttr("data-field", "max deviation")[0]!;
    expect(row.allText()).toContain("12.0 mm");
    // A change the heatmap never measured shows nothing rather than a zero.
    chrome.selectChange("nodes/Mirror_L");
    expect(container.byAttr("data-field", "max deviation")).toHaveLength(0);
  });
});

describe("the frame-all control", () => {
  const frameButton = (container: FakeElement): FakeElement => container.byAttr("data-frame", "1")[0]!;

  it("reports a click rather than moving any camera itself", () => {
    // Same rule as the queue's rows: the chrome is DOM, the camera is the
    // scene's, and the mount is the only thing that can reach both.
    const { container, calls } = setup();
    frameButton(container).fire("click");
    frameButton(container).fire("click");
    expect(calls.frames).toBe(2);
  });

  it("is there in every mode, and in a view with no changes at all", () => {
    // Unlike the split and deviation toggles, there is no state in which
    // "put the camera back on the model" has nothing to do — and the state a
    // reviewer reaches for it from is the one where a control that moved or
    // vanished is worst.
    const { container, chrome } = setup();
    for (const mode of MODE_ORDER) {
      chrome.setMode(mode);
      expect(frameButton(container).getAttribute("hidden")).toBeNull();
    }
    expect(frameButton(setup({ queue: [], modes: ["structural"] }).container)).toBeDefined();
  });

  it("says what it does, for a control whose label is two words", () => {
    const { container } = setup();
    expect(frameButton(container).textContent).toBe(FRAME_LABEL);
    expect(frameButton(container).getAttribute("title")).toBe(FRAME_TITLE);
  });

  it("stops reporting once the chrome is disposed", () => {
    const { container, chrome, calls } = setup();
    const button = frameButton(container);
    chrome.dispose();
    button.fire("click");
    expect(calls.frames).toBe(0);
  });
});

describe("collapsing", () => {
  it("collapses a region to a rail when the viewer asks", () => {
    const { container } = setup();
    container.byAttr("data-collapse", "tree")[0]!.fire("click");
    expect(stateOf(container, "tree")).toBe("collapsed");
    // A rail the viewer can click to bring it back.
    expect(container.byAttr("data-collapse", "tree")).toHaveLength(1);
    container.byAttr("data-collapse", "tree")[0]!.fire("click");
    expect(stateOf(container, "tree")).toBe("open");
  });

  it("auto-collapses the tree first as the container narrows", () => {
    const { container, chrome } = setup();
    chrome.applyWidth(TREE_MIN_WIDTH - 1);
    expect(stateOf(container, "tree")).toBe("hidden");
    expect(stateOf(container, "queue")).toBe("open");
    expect(chrome.layout).toEqual({ tree: false, queue: true });
  });

  it("auto-collapses the queue too once even that doesn't fit", () => {
    const { container, chrome } = setup();
    chrome.applyWidth(QUEUE_MIN_WIDTH - 1);
    expect(stateOf(container, "queue")).toBe("hidden");
    expect(chrome.layout).toEqual({ tree: false, queue: false });
  });

  it("gives back exactly what was open when the width returns", () => {
    const { container, chrome } = setup();
    container.byAttr("data-collapse", "tree")[0]!.fire("click");
    chrome.applyWidth(400);
    chrome.applyWidth(TREE_MIN_WIDTH + 200);
    // The tree stays closed because the viewer closed it, not the width.
    expect(stateOf(container, "tree")).toBe("collapsed");
    expect(stateOf(container, "queue")).toBe("open");
  });

  it("keeps the queue's selection through a collapse and back", () => {
    const { container, chrome } = setup();
    chrome.selectChange("nodes/Mirror_L");
    chrome.applyWidth(200);
    chrome.applyWidth(TREE_MIN_WIDTH + 200);
    expect(selectedStops(container)).toEqual(["nodes/Mirror_L"]);
  });
});

describe("selection survives a mode switch", () => {
  it("keeps the queue row, the panel and the tree row across every mode", () => {
    const { container, chrome } = setup();
    chrome.selectChange("nodes/Wheel_FL");
    chrome.highlightNode("Wheel_FL");
    const treeRegion = region(container, "tree");
    const queueRegion = region(container, "queue");

    for (const mode of MODE_ORDER) {
      chrome.setMode(mode);
      expect(selectedStops(container)).toEqual(["nodes/Wheel_FL"]);
      expect(nodeRow(container, "Wheel_FL").getAttribute("aria-selected")).toBe("true");
      expect(container.byAttr("data-panel", "1")[0]!.allText()).toContain("translation");
      // The chrome persists across every mode: only the centre changes, so the
      // very elements holding the selection are the ones still on screen.
      expect(region(container, "tree")).toBe(treeRegion);
      expect(region(container, "queue")).toBe(queueRegion);
    }
    expect(container.byAttr("data-mode", "side-by-side")[0]).toBeDefined();
  });
});

describe("the structure tree", () => {
  it("reports a node click, including an unchanged one", () => {
    const { container, calls } = setup();
    nodeRow(container, "Wheel_FR").fire("click");
    expect(calls.nodes).toEqual(["Wheel_FR"]);
  });

  it("moves its highlight when told, and shrugs at a name it doesn't have", () => {
    const { container, chrome } = setup();
    chrome.highlightNode("Wheel_FL");
    expect(nodeRow(container, "Wheel_FL").getAttribute("aria-selected")).toBe("true");
    chrome.highlightNode("NotInThisFile");
    expect(nodeRow(container, "Wheel_FL").getAttribute("aria-selected")).toBe("false");
  });
});

describe("dispose", () => {
  it("takes the chrome out of the container and stops listening", () => {
    const { container, chrome, calls } = setup();
    const row = stopRow(container, "nodes/Wheel_FL");
    chrome.dispose();
    expect(container.childNodes).toEqual([]);
    row.fire("click");
    expect(calls.selected).toEqual([]);
  });
});
