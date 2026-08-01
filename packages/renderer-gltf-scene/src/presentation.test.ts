import { describe, it, expect } from "vitest";
import type { HandlerCapabilities } from "@fhr/types";
import {
  availableModes,
  createModeState,
  defaultMode,
  HEATMAP_MODE,
  MODE_ORDER,
  versionLayers,
} from "./presentation.js";

const caps = (semanticCompare: boolean): HandlerCapabilities => ({ semanticCompare, semanticMerge: false });

describe("defaultMode — the one contract addition, put to work", () => {
  it("opens on structural when the handler compares semantically", () => {
    expect(defaultMode(caps(true))).toBe("structural");
  });

  it("opens on side-by-side when it says it cannot", () => {
    // Regenerated topology: a structural diff would report "everything changed",
    // which is true and useless. Showing both versions is the honest default.
    expect(defaultMode(caps(false))).toBe("side-by-side");
  });

  it("opens on structural when the host didn't plumb capabilities through", () => {
    expect(defaultMode(undefined)).toBe("structural");
    expect(defaultMode({} as HandlerCapabilities)).toBe("structural");
  });
});

describe("availableModes", () => {
  it("offers the whole ladder when both versions are resident", () => {
    expect(availableModes({ bothVersionsResident: true })).toEqual([...MODE_ORDER]);
  });

  it("offers structural alone when the previous version isn't loaded", () => {
    // Overlay and side-by-side both draw the previous version; a toggle to a
    // mode that can only show what is already on screen is a broken promise.
    expect(availableModes({ bothVersionsResident: false })).toEqual(["structural"]);
  });
});

describe("versionLayers", () => {
  it("gives a side-by-side pane its version and nothing else", () => {
    // Including the paint. It is the only part of the grammar that is not a
    // group, so it is the only part a pane cannot drop by hiding something —
    // and a "Current version" pane showing the diff's tint shows the reviewer
    // the highlight instead of the new colour underneath it.
    expect(versionLayers({ side: "head", grammar: false, mode: "side-by-side" })).toEqual({
      head: true,
      baseSolid: false,
      baseGhost: false,
      removed: false,
      moved: false,
      paint: false,
    });
    expect(versionLayers({ side: "base", grammar: false, mode: "side-by-side" })).toEqual({
      head: false,
      baseSolid: true,
      baseGhost: false,
      removed: false,
      moved: false,
      paint: false,
    });
  });

  it("carries the whole grammar in a single viewport", () => {
    expect(versionLayers({ side: "head", grammar: true, mode: "structural" })).toEqual({
      head: true,
      baseSolid: false,
      baseGhost: false,
      removed: true,
      moved: true,
      paint: true,
    });
  });

  it("adds the previous version underneath in overlay, and only there", () => {
    expect(versionLayers({ side: "head", grammar: true, mode: "overlay" }).baseGhost).toBe(true);
    expect(versionLayers({ side: "head", grammar: true, mode: "structural" }).baseGhost).toBe(false);
  });

  it("keeps the paint on the version the blink hides, so the swap back is instant", () => {
    // Holding Space in a single viewport shows the previous version; the current
    // one is still painted, just not drawn, so releasing costs no re-materialise.
    expect(versionLayers({ side: "base", grammar: true, mode: "structural" })).toMatchObject({
      head: false,
      baseSolid: true,
      paint: true,
    });
  });
});

describe("createModeState", () => {
  it("falls back when the preferred mode isn't available here", () => {
    const state = createModeState({ initial: "side-by-side", available: ["structural"] });
    expect(state.mode).toBe("structural");
  });

  it("switches and notifies", () => {
    const seen: string[] = [];
    const state = createModeState({ initial: "structural", available: [...MODE_ORDER] });
    state.onChange((mode) => seen.push(mode));
    expect(state.set("overlay")).toBe(true);
    expect(state.mode).toBe("overlay");
    expect(seen).toEqual(["overlay"]);
  });

  it("refuses a mode it doesn't offer, and a no-op", () => {
    const seen: string[] = [];
    const state = createModeState({ initial: "structural", available: ["structural"] });
    state.onChange((mode) => seen.push(mode));
    expect(state.set("side-by-side")).toBe(false);
    expect(state.set("structural")).toBe(false);
    expect(state.mode).toBe("structural");
    expect(seen).toEqual([]);
  });

  it("never ends up pointing at nothing", () => {
    const state = createModeState({ initial: "overlay", available: [] });
    expect(state.mode).toBe("structural");
  });
});

describe("the heatmap's home on the ladder", () => {
  it("is a sub-view of overlay, and of a mode that actually exists", () => {
    // Overlay is already "where, and how far"; the heatmap is that question in
    // numbers. A fourth ladder position would have to answer what the ghost and
    // the motion vectors do while it is on, and there is no good answer.
    expect(HEATMAP_MODE).toBe("overlay");
    expect(MODE_ORDER).toContain(HEATMAP_MODE);
  });

  it("only ever appears where the previous version is resident", () => {
    // The heatmap's own gate needs a base model; so does its host mode. A mount
    // without one offers neither, so the toggle has nowhere to appear.
    expect(availableModes({ bothVersionsResident: false })).not.toContain(HEATMAP_MODE);
    expect(availableModes({ bothVersionsResident: true })).toContain(HEATMAP_MODE);
  });
});
