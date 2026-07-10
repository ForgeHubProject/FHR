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
