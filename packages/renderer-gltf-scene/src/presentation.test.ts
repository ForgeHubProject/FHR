import { describe, it, expect } from "vitest";
import type { HandlerCapabilities } from "@fhr/types";
import { availableModes, createModeState, defaultMode, MODE_ORDER } from "./presentation.js";

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
