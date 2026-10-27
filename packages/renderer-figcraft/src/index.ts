import { defineRenderer, formatChange, renderDiffTree } from "@fhr/renderer-sdk";
import type { DiffRow, FormattedChange } from "@fhr/renderer-sdk";
import type { MountProps } from "@fhr/types";

// Replaced at bundle-build time with the release's short commit SHA (see
// build.mjs). Guarded with typeof so importing the source directly (e.g. in a
// unit test, where the define isn't applied) doesn't throw.
declare const __BUILD__: string;
const BUILD = typeof __BUILD__ !== "undefined" ? __BUILD__ : "dev";

/**
 * A FigCraft change's values. The handler writes them in the design panel's
 * terms: positions and sizes in canvas pixels, colours as [r, g, b, a] under a
 * label naming the colour, angles with their "°". Pixels are not metres, so
 * lengths are shown as the bare numbers they are.
 */
export function formatFigcraftChange(row: DiffRow): FormattedChange {
  return formatChange(row, { lengthUnit: "none" });
}

/**
 * Renderer for FigCraft design files (.figcraft). Draws the handler's change
 * tree: page, screen, layer, property, with colour chips and position deltas,
 * and what auto layout moved kept in its own group. A visual compare of the
 * changed screens is a planned follow-up.
 */
export default defineRenderer({
  handlerId: "figcraft",
  extensions: [".figcraft"],
  build: BUILD,
  // Draws diffs only: a single file stays with the host's own viewer.
  modes: ["diff"],
  render(container: HTMLElement, props: MountProps) {
    renderDiffTree(container, props, { format: formatFigcraftChange });
  },
});
