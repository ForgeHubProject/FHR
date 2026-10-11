import { describe, it, expect } from "vitest";
import type { MountProps } from "@fhr/types";
import { defineRenderer } from "./define.js";

// Minimal HTMLElement stand-in: only replaceChildren is exercised by the
// lifecycle, so a fake keeps these tests DOM-free.
function fakeEl() {
  return { cleared: 0, replaceChildren() { this.cleared += 1; } };
}

const props = (mode: MountProps["mode"] = "diff"): MountProps => ({ mode });

describe("defineRenderer", () => {
  it("exposes the bundle metadata", () => {
    const b = defineRenderer({ handlerId: "gltf-scene", extensions: [".gltf"], build: "abc123", render: () => {} });
    expect(b.fhrVersion).toBe(1);
    expect(b.handlerId).toBe("gltf-scene");
    expect(b.extensions).toEqual([".gltf"]);
    expect(b.build).toBe("abc123");
  });

  it("renders once on mount", () => {
    let renders = 0;
    const el = fakeEl();
    const b = defineRenderer({ handlerId: "h", extensions: [], render: () => { renders += 1; } });
    b.mount(el as unknown as HTMLElement, props());
    expect(renders).toBe(1);
    expect(el.cleared).toBe(1);
  });

  it("runs cleanup before re-rendering on update", () => {
    const calls: string[] = [];
    const el = fakeEl();
    const b = defineRenderer({
      handlerId: "h",
      extensions: [],
      render: () => { calls.push("render"); return () => calls.push("cleanup"); },
    });
    const inst = b.mount(el as unknown as HTMLElement, props());
    inst.update(props("view"));
    expect(calls).toEqual(["render", "cleanup", "render"]);
  });

  // ── the optional non-destructive update hook ───────────────────────────────
  // The point of the hook is that a prop push does NOT re-run render() (which is
  // where a 3D renderer fetches blobs and builds a WebGL context), so every test
  // here asserts on the render count, not just on the hook being called.

  it("uses the update hook instead of re-rendering when one is given", () => {
    const calls: string[] = [];
    const el = fakeEl();
    const b = defineRenderer({
      handlerId: "h",
      extensions: [],
      render: () => {
        calls.push("render");
        return () => calls.push("cleanup");
      },
      update: () => {
        calls.push("update");
      },
    });
    const inst = b.mount(el as unknown as HTMLElement, props());
    const clearedAfterMount = el.cleared;
    inst.update({ ...props(), selectedChangePath: "nodes/Cube" });
    expect(calls).toEqual(["render", "update"]);
    // No cleanup, no second render, and the container was never emptied.
    expect(el.cleared).toBe(clearedAfterMount);
  });

  it("hands the update hook the new props and the last applied props", () => {
    const seen: { props: MountProps; prev: MountProps }[] = [];
    const el = fakeEl();
    const first = { ...props(), selectedChangePath: "a" };
    const second = { ...props(), selectedChangePath: "b" };
    const third = { ...props(), selectedChangePath: "c" };
    const b = defineRenderer({
      handlerId: "h",
      extensions: [],
      render: () => {},
      update: (_el, p, prev) => {
        seen.push({ props: p, prev });
      },
    });
    const inst = b.mount(el as unknown as HTMLElement, first);
    inst.update(second);
    inst.update(third);
    expect(seen.map((s) => [s.prev.selectedChangePath, s.props.selectedChangePath])).toEqual([
      ["a", "b"],
      ["b", "c"],
    ]);
  });

  it("falls back to teardown when the update hook declines with false", () => {
    const calls: string[] = [];
    const el = fakeEl();
    const b = defineRenderer({
      handlerId: "h",
      extensions: [],
      render: () => {
        calls.push("render");
        return () => calls.push("cleanup");
      },
      update: (_el, p) => {
        // Decline the pushes this renderer can't patch in place.
        if (p.theme === "dark") return false;
        calls.push("update");
        return true;
      },
    });
    const inst = b.mount(el as unknown as HTMLElement, props());
    inst.update({ ...props(), theme: "dark" });
    expect(calls).toEqual(["render", "cleanup", "render"]);
    expect(el.cleared).toBe(2);
  });

  it("re-runs a declined push against the props the redraw applied", () => {
    const seen: (string | null | undefined)[] = [];
    const el = fakeEl();
    const b = defineRenderer({
      handlerId: "h",
      extensions: [],
      render: () => {},
      // Decline once, so the next update's `prev` must be the redrawn props.
      update: (_el, p, prev) => {
        seen.push(prev.selectedChangePath);
        return p.selectedChangePath !== "declined";
      },
    });
    const inst = b.mount(el as unknown as HTMLElement, { ...props(), selectedChangePath: "a" });
    inst.update({ ...props(), selectedChangePath: "declined" });
    inst.update({ ...props(), selectedChangePath: "b" });
    expect(seen).toEqual(["a", "declined"]);
  });

  it("runs cleanup and clears the container on unmount", () => {
    const calls: string[] = [];
    const el = fakeEl();
    const b = defineRenderer({
      handlerId: "h",
      extensions: [],
      render: () => { return () => calls.push("cleanup"); },
    });
    const inst = b.mount(el as unknown as HTMLElement, props());
    const before = el.cleared;
    inst.unmount();
    expect(calls).toEqual(["cleanup"]);
    expect(el.cleared).toBe(before + 1);
  });
});
