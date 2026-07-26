// The bundle boundary: mount / update / unmount on the real default export.
//
// This is where the #45 contract change is proved end to end. Before it,
// `RendererInstance.update` was `cleanup(); container.replaceChildren();
// render()` — so a host that pushed a new selection got the model re-fetched,
// re-parsed, and the camera put back where it started. Here the same push keeps
// the DOM it already built, and a push the renderer *can't* patch still falls
// back to the old teardown, which is what keeps the contract backward compatible.
//
// The 3D chunk is never loaded in these tests (nothing clicks "View in 3D"), so
// the fetch spy is a floor, not the whole story: the seam where blobs are fetched
// is `mount3d`, and live-view.test.ts proves that a patched push does not call it
// again. Headless tests cannot open a WebGL context.

import { describe, it, expect, beforeEach, afterEach } from "vitest";
import type { MountProps, StructuredDiff } from "@fhr/types";
import { createFakeDocument, asElement, type FakeElement } from "./fake-dom.js";

// Stamped by esbuild's `define` in the real build; the module reads it at import.
(globalThis as unknown as { __BUILD__: string }).__BUILD__ = "test-build";
const bundle = (await import("./index.js")).default;

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
              before: "[0.00 0.00 0.00]",
              after: "[0.00 0.05 0.00]",
            },
          ],
        },
        { path: "nodes/Mirror_L", label: "Mirror_L", kind: "removed" },
      ],
    },
  ],
};

const props = (extra: Partial<MountProps> = {}): MountProps => ({
  mode: "diff",
  diff,
  blobs: { base: { url: "/base.glb", size: 8 }, head: { url: "/head.glb", size: 8 } },
  ...extra,
});

let fetches: string[] = [];
const realFetch = globalThis.fetch;

beforeEach(() => {
  fetches = [];
  globalThis.fetch = ((input: unknown) => {
    fetches.push(String(input));
    return Promise.reject(new Error("no network in tests"));
  }) as typeof fetch;
});
afterEach(() => {
  globalThis.fetch = realFetch;
});

function mount(initial: MountProps = props()) {
  const doc = createFakeDocument();
  const container = doc.createElement("div");
  const instance = bundle.mount(asElement(container), initial);
  return { container, instance };
}

const tree = (container: FakeElement): FakeElement | undefined => container.byClass("fhr-diff")[0];
const selectedPaths = (container: FakeElement): string[] =>
  container.byAttr("aria-selected", "true").map((r) => r.attributes["data-path"]!);

describe("the bundle", () => {
  it("declares itself", () => {
    expect(bundle.fhrVersion).toBe(1);
    expect(bundle.handlerId).toBe("gltf-scene");
    expect(bundle.extensions).toEqual([".gltf", ".glb"]);
    expect(bundle.build).toBe("test-build");
  });

  it("renders the change tree on mount, and fetches nothing for it", () => {
    const { container } = mount();
    expect(tree(container)).toBeDefined();
    expect(fetches).toEqual([]);
  });
});

describe("update() is non-destructive for a selection push", () => {
  it("keeps the DOM it built and moves the highlight", () => {
    const { container, instance } = mount();
    const before = tree(container)!;
    const childCount = container.childNodes.length;

    instance.update(props({ selectedChangePath: "nodes/Mirror_L" }));

    // Same tree element: the container was never emptied, so nothing that hangs
    // off it (a live scene, a camera, a WebGL context) was torn down either.
    expect(tree(container)).toBe(before);
    expect(container.childNodes.length).toBe(childCount);
    expect(selectedPaths(container)).toEqual(["nodes/Mirror_L"]);
    expect(fetches).toEqual([]);
  });

  it("keeps working across several pushes", () => {
    const { container, instance } = mount();
    const before = tree(container)!;
    instance.update(props({ selectedChangePath: "nodes/Wheel_FL" }));
    instance.update(props({ selectedChangePath: "nodes/Mirror_L" }));
    instance.update(props({ selectedChangePath: null }));
    expect(tree(container)).toBe(before);
    expect(selectedPaths(container)).toEqual([]);
  });
});

describe("update() falls back to a redraw when it has to", () => {
  it("rebuilds for a theme change", () => {
    const { container, instance } = mount();
    const before = tree(container)!;
    instance.update(props({ theme: "dark" }));
    const after = tree(container)!;
    expect(after).not.toBe(before);
    expect(after.attributes["data-theme"]).toBe("dark");
  });

  it("rebuilds for new blobs", () => {
    const { container, instance } = mount();
    const before = tree(container)!;
    instance.update(props({ blobs: { head: { url: "/head-2.glb", size: 8 } } }));
    expect(tree(container)).not.toBe(before);
  });

  it("rebuilds for a new diff", () => {
    const { container, instance } = mount();
    const before = tree(container)!;
    instance.update(props({ diff: { ...diff, changes: [] } }));
    expect(tree(container)).not.toBe(before);
    expect(container.allText()).toContain("No changes.");
  });
});

describe("unmount", () => {
  it("empties the container", () => {
    const { container, instance } = mount();
    instance.unmount();
    expect(container.childNodes).toEqual([]);
  });

  it("leaves later pushes to rebuild rather than patching a dead view", () => {
    const { container, instance } = mount();
    instance.unmount();
    instance.update(props({ selectedChangePath: "nodes/Wheel_FL" }));
    expect(tree(container)).toBeDefined();
    expect(selectedPaths(container)).toEqual(["nodes/Wheel_FL"]);
  });
});
