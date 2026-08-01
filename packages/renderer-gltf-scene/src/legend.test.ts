// The legend's structure and text, against the fake DOM. What it proves is that
// a reviewer can read a number off it — not how it looks.

import { describe, it, expect } from "vitest";
import { asElement, createFakeDocument, type FakeElement } from "./fake-dom.js";
import { createHeatmapLegend, LEGEND_MEASURING, LEGEND_TITLE } from "./legend.js";
import { RAMP_STOPS } from "./ramp.js";

function mount(): { host: FakeElement; legend: ReturnType<typeof createHeatmapLegend> } {
  const doc = createFakeDocument();
  const host = doc.createElement("div");
  return { host, legend: createHeatmapLegend(asElement(host), "light") };
}

const only = (host: FakeElement, attr: string): FakeElement => host.byAttr(attr, "1")[0]!;

describe("the heatmap legend", () => {
  it("stays out of the way until the heatmap is on", () => {
    const { host, legend } = mount();
    const el = host.byAttr("data-legend", "1")[0]!;
    expect(el.style.cssText).toContain("display:none");
    legend.show(true);
    expect(el.style["display"]).toBe("block");
    legend.show(false);
    expect(el.style["display"]).toBe("none");
  });

  it("names what it is measuring", () => {
    const { host } = mount();
    expect(only(host, "data-legend-title").textContent).toBe(LEGEND_TITLE);
  });

  it("carries the ramp itself, so a colour can be looked up", () => {
    const { host } = mount();
    const ramp = only(host, "data-legend-ramp");
    for (const stop of RAMP_STOPS) expect(ramp.style.cssText).toContain(stop);
  });

  it("shows both ends of the range with units", () => {
    const { host, legend } = mount();
    // Before anything is measured the scale says so, rather than sitting blank
    // beside a ramp — which reads as a number that failed to render.
    expect(only(host, "data-legend-min").textContent).toBe("—");
    expect(only(host, "data-legend-max").textContent).toBe("—");
    legend.setRange(0, 0.012);
    expect(only(host, "data-legend-min").textContent).toBe("0.0 mm");
    expect(only(host, "data-legend-max").textContent).toBe("12.0 mm");
    // A re-tessellated surface has no zero end; the legend has to say so rather
    // than draw a scale that starts somewhere it doesn't.
    legend.setRange(0.0004, 0.0031);
    expect(only(host, "data-legend-min").textContent).toBe("0.40 mm");
    expect(only(host, "data-legend-max").textContent).toBe("3.10 mm");
  });

  it("says it is measuring rather than showing an empty scale", () => {
    const { host, legend } = mount();
    const status = only(host, "data-legend-status");
    expect(status.style.cssText).toContain("display:none");
    legend.setStatus(LEGEND_MEASURING);
    expect(status.textContent).toBe(LEGEND_MEASURING);
    expect(status.style["display"]).toBe("block");
    legend.setStatus(null);
    expect(status.style["display"]).toBe("none");
  });

  it("reads out the value under the pointer, and prompts when there isn't one", () => {
    const { host, legend } = mount();
    const reading = only(host, "data-legend-reading");
    const prompt = reading.textContent;
    expect(prompt.length).toBeGreaterThan(0);
    legend.setReading("HoodMesh", 0.012);
    expect(reading.textContent).toBe("HoodMesh · 12.0 mm");
    legend.clearReading();
    expect(reading.textContent).toBe(prompt);
  });

  it("takes itself off the viewport when disposed", () => {
    const { host, legend } = mount();
    expect(host.childNodes.length).toBe(1);
    legend.dispose();
    expect(host.childNodes.length).toBe(0);
  });
});
