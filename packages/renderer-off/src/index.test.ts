import { describe, it, expect } from "vitest";
import type { MountProps } from "@fhr/types";
import bundle, { sceneOptions } from "./index.js";

describe("off renderer bundle", () => {
  it("declares the off handler and its extension via the mount() contract", () => {
    expect(bundle.handlerId).toBe("off");
    expect(bundle.extensions).toEqual([".off"]);
    expect(bundle.fhrVersion).toBe(1);
    expect(typeof bundle.mount).toBe("function");
  });

  it("draws the handler's GLB previews, never the OFF bytes", () => {
    const blobs = { base: { url: "/a.off", size: 10 }, head: { url: "/b.off", size: 12 } };
    const previews = { base: { url: "/a.glb", size: 20 }, head: { url: "/b.glb", size: 24 } };
    const props: MountProps = { mode: "diff", blobs, previews };
    expect(sceneOptions.geometry?.(props)).toBe(previews);
    // No preview from the host: nothing to draw, so no 3D view is offered.
    expect(sceneOptions.geometry?.({ mode: "diff", blobs })).toBeUndefined();
  });

  it("lazy-loads its own 3D chunk, a sibling the host proxy resolves under off", () => {
    expect(sceneOptions.chunk).toBe("renderer-off-3d.js");
  });
});
