// What "fill the available height" resolves to, per container.
//
// The decision is a string of CSS, so this is the whole of it: there is no
// layout to assert, and a browser is the only thing that could tell you the
// numbers were the right ones. What these pin is the part that has been wrong
// twice — that a container offering a height is filled and not exceeded, and
// that one offering none gets a size measured against the window rather than a
// constant somebody typed.

import { describe, it, expect } from "vitest";
import { AUTO_VIEWPORT_VH, MIN_VIEWPORT_HEIGHT, viewportFillCss } from "./viewport-fill.js";

describe("viewportFillCss", () => {
  it("fills a container that has a height of its own", () => {
    // The host laid this out; taking exactly its height is the contract. A
    // renderer that asked for a window share here would draw outside the box.
    const css = viewportFillCss(900);
    expect(css).toContain("height:100%");
    expect(css).not.toContain("vh");
  });

  it("takes a share of the window when the container offers no height", () => {
    // Both hosts today: a padded column on a review page, and forge's <main>.
    // `height:100%` there computes to `auto` and the scene collapses to nothing.
    const css = viewportFillCss(0);
    expect(css).toContain(`height:${AUTO_VIEWPORT_VH}vh`);
    expect(css).not.toContain("height:100%");
  });

  it("floors both cases, so the scene is never a strip again", () => {
    for (const offered of [0, 1, 120, 900]) {
      expect(viewportFillCss(offered)).toContain(`min-height:${MIN_VIEWPORT_HEIGHT}px`);
    }
  });

  it("leaves a share of the window for the page around it", () => {
    // A panel, not a takeover: a reviewer who cannot see the file list or the
    // comment box has lost the review the viewport is part of.
    expect(AUTO_VIEWPORT_VH).toBeGreaterThan(50);
    expect(AUTO_VIEWPORT_VH).toBeLessThan(100);
  });

  it("never hands out a fixed pixel height", () => {
    // The regression #24 is about: 420px in view mode, 560px behind "View in 3D",
    // both of them the same number on a phone and on a 32-inch display.
    // The floor is a `min-height`, which is why the boundary is in the pattern.
    for (const offered of [0, 900]) {
      expect(viewportFillCss(offered)).not.toMatch(/(^|;)height:\d+px/);
    }
  });
});
