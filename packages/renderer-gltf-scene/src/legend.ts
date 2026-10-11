// The deviation heatmap's legend.
//
// Not optional chrome. A shaded surface with no scale beside it is a picture
// that cannot be read: green means "more than the blue bits" and nothing else,
// and every metrology tool that ships this encoding ships a legend with it for
// exactly that reason. #56's own note on overlay mode says it outright —
// "deviation shading without a legend or a way to step is worse than not having
// it".
//
// So it carries the three things that turn a colour into a measurement: the ramp
// itself, the numbers at its ends *with units*, and — while the pointer is over
// painted geometry — the reading at that spot, which is what a reviewer actually
// wants to quote in a comment.
//
// DOM over the canvas, like the pane labels and the callout: crisp at any pixel
// ratio, no texture memory, and it can't be occluded by the model it describes.
// No three.js reaches this file, so its structure is tested headlessly.

import { formatDeviation } from "./deviation.js";
import { rampGradientCss } from "./ramp.js";

export type Legend = {
  el: HTMLElement;
  show(on: boolean): void;
  /**
   * The measured range. Both ends are shown — a scale with one end is a hint —
   * but they are the ends of the RAMP, which starts at zero whatever the
   * smallest measured value turns out to be. See `setRange` for why.
   */
  setRange(min: number, max: number): void;
  /** A line above the ramp while the measurement is still running. */
  setStatus(text: string | null): void;
  /** The value under the pointer, or null when it is over nothing measured. */
  setReading(label: string, value: number): void;
  clearReading(): void;
  dispose(): void;
};

export const LEGEND_TITLE = "Deviation from previous version";
/** Shown while the first measurement runs, so the empty ramp isn't a mystery. */
export const LEGEND_MEASURING = "Measuring…";
/**
 * Shown when one measured mesh is instanced at several scales: the ramp is one
 * attribute on one shared geometry, so it can only be right for one of them
 * (heatmap.ts picks the largest). A reviewer quoting the number needs to know.
 */
export const LEGEND_MIXED_SCALE = "Shared mesh at several scales — largest copy";
/** What the readout says before the pointer has been over anything painted. */
const HOVER_PROMPT = "Point at coloured geometry for a reading";
/**
 * How much of the ramp has to go unused before the legend mentions it. Below
 * this the empty foot is a sliver a reviewer cannot see, and a line about it is
 * noise in a panel that has to stay small.
 */
const FLOOR_NOTE_FRACTION = 0.02;

export function createHeatmapLegend(container: HTMLElement, theme: "light" | "dark"): Legend {
  const doc = container.ownerDocument;
  const dark = theme === "dark";
  const paper = dark ? "rgba(13,17,23,0.88)" : "rgba(255,255,255,0.92)";
  const ink = dark ? "#e6edf3" : "#1f2328";
  const muted = dark ? "#8b949e" : "#57606a";
  const line = dark ? "#30363d" : "#d8dee4";

  const el = doc.createElement("div");
  el.setAttribute("data-legend", "1");
  // Clear of the blink hint along the bottom edge, and of the pane labels in the
  // top-left corner — the legend must never sit on top of another affordance.
  el.style.cssText =
    `position:absolute;right:8px;bottom:30px;z-index:2;display:none;min-width:168px;` +
    `padding:6px 8px;border:1px solid ${line};border-radius:6px;background:${paper};color:${ink};` +
    `font:11px/1.5 ui-sans-serif,system-ui,sans-serif;pointer-events:none`;

  const title = doc.createElement("div");
  title.setAttribute("data-legend-title", "1");
  title.style.cssText = "font-weight:600";
  title.textContent = LEGEND_TITLE;

  const status = doc.createElement("div");
  status.setAttribute("data-legend-status", "1");
  status.style.cssText = `color:${muted};display:none`;

  const bar = doc.createElement("div");
  bar.setAttribute("data-legend-ramp", "1");
  bar.style.cssText = `height:8px;margin:4px 0 2px;border-radius:2px;background:${rampGradientCss()}`;

  const scale = doc.createElement("div");
  scale.style.cssText = "display:flex;justify-content:space-between;font-family:ui-monospace,monospace";
  const low = doc.createElement("span");
  low.setAttribute("data-legend-min", "1");
  const high = doc.createElement("span");
  high.setAttribute("data-legend-max", "1");
  // Dashes until the first measurement lands: a blank scale beside a ramp reads
  // as a number that failed to render rather than one that hasn't arrived.
  low.textContent = "—";
  high.textContent = "—";
  scale.append(low, high);

  const floor = doc.createElement("div");
  floor.setAttribute("data-legend-floor", "1");
  floor.style.cssText = `color:${muted};display:none`;

  const reading = doc.createElement("div");
  reading.setAttribute("data-legend-reading", "1");
  reading.style.cssText = `color:${muted};white-space:nowrap;overflow:hidden;text-overflow:ellipsis`;
  reading.textContent = HOVER_PROMPT;

  el.append(title, status, bar, scale, floor, reading);
  container.appendChild(el);

  return {
    el,
    show(on: boolean): void {
      el.style.display = on ? "block" : "none";
    },
    setRange(min: number, max: number): void {
      // The ends label the RAMP, and the ramp starts at zero: heatmap.ts paints
      // every vertex at `value / max`, so the foot of the gradient is deviation
      // nothing, not the smallest value that happened to be measured. Writing
      // `min` there — the obvious reading of "show the measured range" — makes
      // every colour on the model decode to the wrong number, by `min`, and does
      // it silently, since the picture looks the same either way.
      low.textContent = formatDeviation(0);
      high.textContent = formatDeviation(max);
      // Zero is where the scale starts; nothing has to sit there. On a
      // re-tessellated surface every vertex has moved a little, and the foot of
      // the ramp then goes unused — worth one line, because a reviewer who reads
      // "0" as "some of this is unchanged" has drawn the wrong conclusion.
      const note = max > 0 && min > 0 && min / max >= FLOOR_NOTE_FRACTION;
      floor.textContent = note ? `Smallest measured ${formatDeviation(min)}` : "";
      floor.style.display = note ? "block" : "none";
    },
    setStatus(text: string | null): void {
      status.textContent = text ?? "";
      status.style.display = text === null ? "none" : "block";
    },
    setReading(label: string, value: number): void {
      reading.textContent = `${label} · ${formatDeviation(value)}`;
    },
    clearReading(): void {
      reading.textContent = HOVER_PROMPT;
    },
    dispose(): void {
      el.remove();
    },
  };
}
