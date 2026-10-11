import { describe, it, expect } from "vitest";
import {
  QUEUE_MIN_WIDTH,
  TREE_MIN_WIDTH,
  autoCollapsed,
  regionLayout,
} from "./chrome-layout.js";

const both = { tree: true, queue: true };

describe("regionLayout", () => {
  it("keeps both regions when there is room", () => {
    expect(regionLayout(TREE_MIN_WIDTH, both)).toEqual({ tree: true, queue: true });
  });

  it("folds the tree first", () => {
    // The queue is the worklist and the position in it — the region a reviewer
    // cannot work without. The tree is context, so it is the one that goes.
    expect(regionLayout(TREE_MIN_WIDTH - 1, both)).toEqual({ tree: false, queue: true });
    expect(TREE_MIN_WIDTH).toBeGreaterThan(QUEUE_MIN_WIDTH);
  });

  it("folds the queue too once even that doesn't fit", () => {
    expect(regionLayout(QUEUE_MIN_WIDTH - 1, both)).toEqual({ tree: false, queue: false });
  });

  it("never re-opens something the viewer collapsed by hand", () => {
    expect(regionLayout(2000, { tree: false, queue: true })).toEqual({ tree: false, queue: true });
  });

  it("restores the preference when the width comes back", () => {
    const wanted = { tree: true, queue: true };
    expect(regionLayout(400, wanted).tree).toBe(false);
    expect(regionLayout(2000, wanted).tree).toBe(true);
  });

  it("treats an unmeasured container as unknown rather than as zero", () => {
    // A container measured before layout reports 0. Collapsing everything and
    // then visibly unfolding on the next frame is worse than waiting one frame.
    expect(regionLayout(0, both)).toEqual({ tree: true, queue: true });
  });
});

describe("autoCollapsed", () => {
  it("distinguishes a width fold from a viewer's own collapse", () => {
    expect(autoCollapsed("tree", 400, both)).toBe(true);
    expect(autoCollapsed("tree", 400, { tree: false, queue: true })).toBe(false);
    expect(autoCollapsed("tree", 2000, both)).toBe(false);
  });
});
