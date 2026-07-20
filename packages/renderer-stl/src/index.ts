import { defineRenderer, renderDiffTree } from "@fhr/renderer-sdk";
import type { MountProps } from "@fhr/types";

// Replaced at bundle-build time with the release's short commit SHA (see
// build.mjs). Guarded with typeof so importing the source directly (e.g. in a
// unit test, where the define isn't applied) doesn't throw.
declare const __BUILD__: string;
const BUILD = typeof __BUILD__ !== "undefined" ? __BUILD__ : "dev";

/**
 * Reference renderer for STL mesh diffs. STL is a triangle soup with no
 * object identity, so the handler's diff is geometric/statistical — triangle
 * count, bounding box, surface area, volume, solid name — and this renderer
 * shows that change tree. A real mesh viewer (its own three.js-style bundle)
 * is the planned follow-up from issue #14 — and per the FHR model that view is
 * this renderer's own free choice; the only contracts are mount() and
 * StructuredDiff.
 */
export default defineRenderer({
  handlerId: "stl",
  extensions: [".stl"],
  build: BUILD,
  render(container: HTMLElement, props: MountProps) {
    renderDiffTree(container, props);
  },
});
