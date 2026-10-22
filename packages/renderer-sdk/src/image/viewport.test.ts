// The zoom-and-pan window: the view arithmetic, and the window's own controls
// driven through the fake document (structure and listeners, never layout —
// a fake element has no size, so its window is 0 × 0).

import { describe, it, expect } from "vitest";
import { clampView, createViewport, fitZoom, rectView, stepZoom, zoomAt, zoomLabel, MAX_ZOOM, MIN_ZOOM } from "./viewport.js";
import { createFakeDocument, type FakeElement } from "../fake-dom.js";

describe("clampView", () => {
  it("centres an image smaller than the window", () => {
    expect(clampView({ zoom: 1, x: -40, y: 900 }, { w: 100, h: 50 }, { w: 300, h: 150 })).toEqual({ zoom: 1, x: 100, y: 50 });
  });

  it("keeps a larger image's edges outside the window", () => {
    const content = { w: 1000, h: 800 };
    const port = { w: 300, h: 200 };
    expect(clampView({ zoom: 1, x: 50, y: 20 }, content, port)).toEqual({ zoom: 1, x: 0, y: 0 });
    expect(clampView({ zoom: 1, x: -5000, y: -5000 }, content, port)).toEqual({ zoom: 1, x: -700, y: -600 });
    expect(clampView({ zoom: 1, x: -123, y: -45 }, content, port)).toEqual({ zoom: 1, x: -123, y: -45 });
  });

  it("judges each axis on its own, at the current zoom", () => {
    // 2× makes the 200 px width 400 px — wider than the window — but the
    // 50 px height only 100 px, still shorter.
    expect(clampView({ zoom: 2, x: 10, y: 0 }, { w: 200, h: 50 }, { w: 300, h: 300 })).toEqual({ zoom: 2, x: 0, y: 100 });
  });
});

describe("zoomAt", () => {
  it("keeps the image point under the cursor where it was", () => {
    const before = { zoom: 1, x: -100, y: -50 };
    const after = zoomAt(before, 4, 200, 120);
    // Image point under (200, 120): ((200 + 100) / 1, (120 + 50) / 1) = (300, 170).
    expect(after.zoom).toBe(4);
    expect(after.x + 300 * 4).toBeCloseTo(200);
    expect(after.y + 170 * 4).toBeCloseTo(120);
  });

  it("stays within the zoom limits", () => {
    expect(zoomAt({ zoom: 1, x: 0, y: 0 }, 1e6, 0, 0).zoom).toBe(MAX_ZOOM);
    expect(zoomAt({ zoom: 1, x: 0, y: 0 }, 0, 0, 0).zoom).toBe(MIN_ZOOM);
  });
});

describe("zoom steps and labels", () => {
  it("steps through the preset levels from wherever the zoom is", () => {
    expect(stepZoom(1, 1)).toBe(1.5);
    expect(stepZoom(1, -1)).toBeCloseTo(2 / 3);
    expect(stepZoom(1.2, 1)).toBe(1.5); // from a wheel zoom between steps
    expect(stepZoom(1.2, -1)).toBe(1);
    expect(stepZoom(MAX_ZOOM, 1)).toBe(MAX_ZOOM);
    expect(stepZoom(MIN_ZOOM, -1)).toBe(MIN_ZOOM);
  });

  it("fits the whole image, less padding, enlarging a small one", () => {
    expect(fitZoom({ w: 2000, h: 1000 }, { w: 516, h: 316 }, 16)).toBe(0.25);
    expect(fitZoom({ w: 16, h: 16 }, { w: 176, h: 176 }, 16)).toBe(10);
    expect(fitZoom({ w: 0, h: 0 }, { w: 100, h: 100 })).toBe(1);
  });

  it("labels the zoom as a percentage", () => {
    expect([1, 1.5, 1 / 3, 64, 1 / 32].map(zoomLabel)).toEqual(["100%", "150%", "33%", "6400%", "3.1%"]);
  });
});

describe("rectView", () => {
  it("centres a region and zooms in on it, never past the cap", () => {
    const v = rectView({ x: 2000, y: 1200, w: 1, h: 1 }, { w: 800, h: 400 });
    expect(v.zoom).toBe(16);
    expect(v.x + 2000.5 * 16).toBeCloseTo(400);
    expect(v.y + 1200.5 * 16).toBeCloseTo(200);
  });

  it("zooms out far enough to show a large region whole", () => {
    expect(rectView({ x: 0, y: 0, w: 1000, h: 100 }, { w: 800, h: 400 }).zoom).toBe(0.5);
  });
});

describe("createViewport", () => {
  const setup = () => {
    const doc = createFakeDocument();
    const vp = createViewport(doc as unknown as Document, 300);
    const el = vp.el as unknown as FakeElement;
    const stage = vp.stage as unknown as FakeElement;
    vp.setContent({ w: 2000, h: 1000 });
    const button = (label: string) => el.descendants().find((b) => b.attributes["aria-label"] === label)!;
    return { vp, el, stage, button };
  };

  it("opens at true size and sizes the stage to the image", () => {
    const { el, stage } = setup();
    expect(el.style["height"]).toBe("300px");
    expect(stage.style["width"]).toBe("2000px");
    expect(stage.style["height"]).toBe("1000px");
    expect(stage.style["transform"]).toBe("translate(0px, 0px) scale(1)");
    expect(stage.style["--fhr-check"]).toBe("16px");
  });

  it("steps the zoom from its controls and shows the level", () => {
    const { vp, stage, button } = setup();
    button("Zoom in").fire("click");
    button("Zoom in").fire("click");
    expect(vp.view.zoom).toBe(2);
    expect(button("Actual size").textContent).toBe("200%");
    // Past 2× pixels show as squares, and the checkerboard keeps its screen size.
    expect(stage.attributes["data-pixelated"]).toBe("1");
    expect(stage.style["--fhr-check"]).toBe("8px");
    button("Actual size").fire("click");
    expect(vp.view.zoom).toBe(1);
    expect(stage.attributes["data-pixelated"]).toBe("0");
  });

  it("pans on a drag, and a press on the controls doesn't start one", () => {
    const { vp, el } = setup();
    el.fire("pointerdown", { button: 0, clientX: 500, clientY: 300 });
    el.fire("pointermove", { clientX: 420, clientY: 260 });
    el.fire("pointerup", {});
    expect(vp.view).toMatchObject({ x: -80, y: -40 });
    el.fire("pointermove", { clientX: 0, clientY: 0 }); // released: no longer panning
    expect(vp.view).toMatchObject({ x: -80, y: -40 });

    let stopped = 0;
    el.byClass("fhr-img__zoom")[0]!.fire("pointerdown", { stopPropagation: () => stopped++ });
    expect(stopped).toBe(1);
  });

  it("zooms on Ctrl/⌘ + wheel only; a plain wheel is left to scroll the page", () => {
    const { vp, el } = setup();
    let prevented = 0;
    el.fire("wheel", { deltaY: -100, clientX: 10, clientY: 10, preventDefault: () => prevented++ });
    expect(vp.view.zoom).toBe(1);
    expect(prevented).toBe(0);
    el.fire("wheel", { deltaY: -100, ctrlKey: true, clientX: 10, clientY: 10, preventDefault: () => prevented++ });
    expect(vp.view.zoom).toBeCloseTo(Math.exp(0.2));
    expect(prevented).toBe(1);
  });

  it("tells its listeners about every change", () => {
    const { vp, button } = setup();
    const seen: number[] = [];
    vp.onChange((v) => seen.push(v.zoom));
    button("Zoom in").fire("click");
    vp.zoomTo(3);
    expect(seen).toEqual([1.5, 3]);
    vp.destroy();
    vp.zoomTo(4);
    expect(seen).toEqual([1.5, 3]);
  });
});
