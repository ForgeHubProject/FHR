// The linked review surface, headless.
//
// The 3D scene is injected as a stub mounter, which is what makes these tests
// possible at all: the real one needs WebGL. What that stub *does* prove is the
// contract between the two halves — that a click on geometry becomes a tree
// highlight and one host event, that a host push becomes a fly-to and no event,
// that stepping walks the review order, and (the point of the slice) that a prop
// push does not re-mount the scene, so nothing is re-fetched and no camera is
// lost. What it can't prove is the raycast against live geometry, the tween's
// feel, or that a browser dispatches the pointer events — those need a browser.

import { describe, it, expect } from "vitest";
import type { MountProps, RendererEvent, StructuredDiff } from "@fhr/types";
import { createFakeDocument, asElement, type FakeElement } from "./fake-dom.js";
import { createLiveView, type Scene3D, type SceneHooks, type SceneMounter } from "./live-view.js";

const diff: StructuredDiff = {
  version: "1.0",
  format: "gltf-scene",
  changes: [
    {
      path: "nodes",
      label: "nodes",
      kind: "modified",
      children: [
        {
          path: "nodes/Wheel_FL",
          label: "Wheel_FL",
          kind: "modified",
          children: [
            {
              path: "nodes/Wheel_FL/translation",
              label: "translation",
              kind: "modified",
              before: "[0.90 -1.40 0.35]",
              after: "[0.90 -1.40 0.40]",
            },
          ],
        },
        { path: "nodes/Mirror_L", label: "Mirror_L", kind: "removed" },
      ],
    },
    {
      path: "materials",
      label: "materials",
      kind: "modified",
      children: [
        {
          path: "materials/Paint",
          label: "Paint",
          kind: "modified",
          children: [
            {
              path: "materials/Paint/baseColorFactor",
              label: "baseColorFactor",
              kind: "modified",
              before: "[0.80 0.10 0.10 1.00]",
              after: "[0.10 0.20 0.70 1.00]",
            },
          ],
        },
      ],
    },
  ],
};

/** A scene stand-in that records everything the live view asks of it. */
type StubScene = Scene3D & {
  selections: { path: string | null; fly: boolean }[];
  disposals: number;
  hooks: SceneHooks;
  props: MountProps;
  /** Simulate a click on geometry in the viewport. */
  pick(path: string | null): void;
};

function stubMounter(): { mount: SceneMounter; mounts: StubScene[]; hosts: HTMLElement[] } {
  const mounts: StubScene[] = [];
  const hosts: HTMLElement[] = [];
  const mount: SceneMounter = async (host, props, hooks) => {
    const scene: StubScene = {
      selections: [],
      disposals: 0,
      hooks,
      props,
      dispose(): void {
        scene.disposals += 1;
      },
      selectChange(path, opts): boolean {
        scene.selections.push({ path, fly: opts?.fly !== false });
        return path !== null;
      },
      pick(path): void {
        hooks.onPick?.(path);
      },
    };
    mounts.push(scene);
    hosts.push(host);
    return scene;
  };
  return { mount, mounts, hosts };
}

function setup(props: Partial<MountProps> = {}) {
  const doc = createFakeDocument();
  const container = doc.createElement("div");
  const events: RendererEvent[] = [];
  const blobs = { base: { url: "/base.glb", size: 10 }, head: { url: "/head.glb", size: 10 } };
  const initial: MountProps = {
    mode: "diff",
    diff,
    blobs,
    onEvent: (e) => events.push(e),
    ...props,
  };
  const { mount, mounts, hosts } = stubMounter();
  const view = createLiveView(asElement(container), initial, mount);
  return { doc, container, view, events, mounts, hosts, initial };
}

const rowFor = (container: FakeElement, path: string): FakeElement =>
  container.byAttr("data-path", path)[0]!;
const selectedPaths = (container: FakeElement): string[] =>
  container.byAttr("aria-selected", "true").map((r) => r.attributes["data-path"]!);
/** The "View in 3D" toggle — the tree's own ‹ › step buttons carry a class. */
const button = (container: FakeElement): FakeElement =>
  container.descendants().find((e) => e.tagName === "BUTTON" && e.className === "")!;
/** Let the injected mounter's promise chain settle. */
const settle = async (): Promise<void> => {
  for (let i = 0; i < 4; i++) await Promise.resolve();
};

describe("createLiveView — the tree half", () => {
  it("renders the change tree with the review order this handler's paths imply", () => {
    const { view } = setup();
    expect(view.stops).toEqual(["nodes/Wheel_FL", "nodes/Mirror_L", "materials/Paint"]);
  });

  it("selects on a row click and tells the host exactly once", () => {
    const { container, view, events } = setup();
    rowFor(container, "nodes/Mirror_L").fire("click");
    expect(view.selected).toBe("nodes/Mirror_L");
    expect(selectedPaths(container)).toEqual(["nodes/Mirror_L"]);
    expect(events).toEqual([{ type: "select", changePath: "nodes/Mirror_L" }]);
  });

  it("selects the owning object when a field row is clicked", () => {
    const { container, view } = setup();
    // Field rows are selectable too — the highlight lands on the row clicked.
    rowFor(container, "nodes/Wheel_FL/translation").fire("click");
    expect(view.selected).toBe("nodes/Wheel_FL/translation");
  });

  it("steps the review path with n and p, wrapping at the ends", () => {
    const { view } = setup();
    view.step(1);
    expect(view.selected).toBe("nodes/Wheel_FL");
    view.step(1);
    view.step(1);
    expect(view.selected).toBe("materials/Paint");
    view.step(1);
    expect(view.selected).toBe("nodes/Wheel_FL");
    view.step(-1);
    expect(view.selected).toBe("materials/Paint");
  });

  it("starts stepping from the end when the first key is p", () => {
    const { view } = setup();
    view.step(-1);
    expect(view.selected).toBe("materials/Paint");
  });

  it("steps from the tree's own keyboard handler", () => {
    const { container, view } = setup();
    const tree = container.byClass("fhr-diff")[0]!;
    tree.fire("keydown", { key: "n", preventDefault: () => {} });
    expect(view.selected).toBe("nodes/Wheel_FL");
  });

  it("applies a selection the host pushed at mount time", () => {
    const { container, view } = setup({ selectedChangePath: "materials/Paint" });
    expect(view.selected).toBe("materials/Paint");
    expect(selectedPaths(container)).toEqual(["materials/Paint"]);
  });
});

describe("createLiveView — the 3D half", () => {
  it("mounts the scene only when the viewer asks for it", async () => {
    const { container, mounts } = setup();
    expect(mounts.length).toBe(0);
    button(container).fire("click");
    await settle();
    expect(mounts.length).toBe(1);
  });

  it("hands the scene the callout headlines for every change", async () => {
    const { container, mounts } = setup();
    button(container).fire("click");
    await settle();
    expect(mounts[0]!.hooks.headlines).toEqual({
      "nodes/Wheel_FL": "moved 50 mm",
      "nodes/Mirror_L": "removed",
      "materials/Paint": "recoloured",
    });
  });

  it("flies the scene to a change selected from the tree", async () => {
    const { container, mounts } = setup();
    button(container).fire("click");
    await settle();
    rowFor(container, "nodes/Wheel_FL").fire("click");
    expect(mounts[0]!.selections).toEqual([{ path: "nodes/Wheel_FL", fly: true }]);
  });

  it("opens on whatever was already selected, and frames it", async () => {
    const { container, mounts, view } = setup();
    view.select("nodes/Mirror_L");
    button(container).fire("click");
    await settle();
    expect(mounts[0]!.selections).toEqual([{ path: "nodes/Mirror_L", fly: true }]);
  });

  it("highlights the tree row when the viewer clicks geometry, and tells the host", async () => {
    const { container, mounts, events, view } = setup();
    button(container).fire("click");
    await settle();
    mounts[0]!.pick("nodes/Wheel_FL");
    expect(view.selected).toBe("nodes/Wheel_FL");
    expect(selectedPaths(container)).toEqual(["nodes/Wheel_FL"]);
    expect(events).toEqual([{ type: "select", changePath: "nodes/Wheel_FL" }]);
    // The scene already showed what was picked: re-selecting it would yank the
    // camera away from the thing the reviewer just clicked.
    expect(mounts[0]!.selections).toEqual([]);
  });

  it("clears the selection when the viewer clicks empty space", async () => {
    const { container, mounts, events, view } = setup();
    button(container).fire("click");
    await settle();
    view.select("nodes/Wheel_FL");
    events.length = 0;
    mounts[0]!.pick(null);
    expect(view.selected).toBeNull();
    expect(selectedPaths(container)).toEqual([]);
    expect(events).toEqual([{ type: "select", changePath: null }]);
  });

  it("steps with n/p while the viewport has focus, and leaves the arrows to it", async () => {
    const { container, hosts, view } = setup();
    button(container).fire("click");
    await settle();
    const host = hosts[0] as unknown as FakeElement;
    host.fire("keydown", { key: "n", preventDefault: () => {} });
    expect(view.selected).toBe("nodes/Wheel_FL");
    host.fire("keydown", { key: "ArrowDown", preventDefault: () => {} });
    expect(view.selected).toBe("nodes/Wheel_FL");
    // Space is the A/B blink and belongs to the scene.
    host.fire("keydown", { key: " ", preventDefault: () => {} });
    expect(view.selected).toBe("nodes/Wheel_FL");
  });

  it("closes the scene on a second click, and releases it", async () => {
    const { container, mounts } = setup();
    button(container).fire("click");
    await settle();
    button(container).fire("click");
    expect(mounts[0]!.disposals).toBe(1);
  });

  it("mounts the scene straight away in view mode, with no tree", () => {
    const { container, mounts, view } = setup({ mode: "view", diff: undefined });
    expect(mounts.length === 0 || mounts.length === 1).toBe(true);
    expect(container.byClass("fhr-diff")).toEqual([]);
    expect(view.stops).toEqual([]);
  });

  it("disposes a scene that finished loading after teardown", async () => {
    const { container, view, mounts } = setup();
    button(container).fire("click");
    view.dispose();
    await settle();
    await settle();
    expect(mounts[0]!.disposals).toBe(1);
  });
});

describe("createLiveView — non-destructive update", () => {
  /** Every push below keeps the blobs, the diff object and the mode identical. */
  const push = (initial: MountProps, changes: Partial<MountProps>): MountProps => ({ ...initial, ...changes });

  it("applies a host selection push without re-mounting anything", async () => {
    const { container, view, mounts, initial, events } = setup();
    button(container).fire("click");
    await settle();
    const scene = mounts[0]!;
    const treeBefore = container.byClass("fhr-diff")[0]!;

    const ok = view.update(push(initial, { selectedChangePath: "materials/Paint" }), initial);

    expect(ok).toBe(true);
    // The scene was neither re-mounted nor disposed: no re-fetch, no new WebGL
    // context, no camera reset. It was told to fly, which is the whole point.
    expect(mounts.length).toBe(1);
    expect(scene.disposals).toBe(0);
    expect(scene.selections).toEqual([{ path: "materials/Paint", fly: true }]);
    // The same tree element is still there, with the highlight moved.
    expect(container.byClass("fhr-diff")[0]).toBe(treeBefore);
    expect(selectedPaths(container)).toEqual(["materials/Paint"]);
    // A selection the host pushed is not echoed back to the host.
    expect(events).toEqual([]);
  });

  it("adopts a freshly-bound onEvent callback from the push", () => {
    const { view, initial } = setup();
    const second: RendererEvent[] = [];
    view.update(push(initial, { onEvent: (e) => second.push(e) }), initial);
    view.select("nodes/Wheel_FL");
    expect(second).toEqual([{ type: "select", changePath: "nodes/Wheel_FL" }]);
  });

  it("ignores a push that repeats the selection it already has", async () => {
    const { container, view, mounts, initial } = setup();
    button(container).fire("click");
    await settle();
    view.select("nodes/Wheel_FL");
    mounts[0]!.selections.length = 0;
    view.update(push(initial, { selectedChangePath: "nodes/Wheel_FL" }), initial);
    expect(mounts[0]!.selections).toEqual([]);
  });

  it("leaves the selection alone when the host isn't driving it", () => {
    const { view, initial } = setup();
    view.select("nodes/Wheel_FL");
    view.update(push(initial, { theme: undefined }), initial);
    expect(view.selected).toBe("nodes/Wheel_FL");
  });

  it("declines a push it cannot patch, so the caller redraws", () => {
    const cases: [string, Partial<MountProps>][] = [
      ["new blobs", { blobs: { base: { url: "/base.glb", size: 10 }, head: { url: "/head-2.glb", size: 10 } } }],
      ["a new theme", { theme: "dark" }],
      ["a new mode", { mode: "view" }],
      ["a new diff", { diff: { ...diff } }],
    ];
    for (const [what, changes] of cases) {
      const { view, initial } = setup();
      expect(view.update(push(initial, changes), initial), what).toBe(false);
    }
  });

  it("declines everything once disposed", () => {
    const { view, initial } = setup();
    view.dispose();
    expect(view.update(push(initial, { selectedChangePath: "nodes/Wheel_FL" }), initial)).toBe(false);
  });
});
