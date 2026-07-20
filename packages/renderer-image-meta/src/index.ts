import { defineRenderer, renderDiffTree } from "@fhr/renderer-sdk";
import type { MountProps } from "@fhr/types";

// Replaced at bundle-build time with the release's short commit SHA (see
// build.mjs). Guarded with typeof so importing the source directly (e.g. in a
// unit test, where the define isn't applied) doesn't throw.
declare const __BUILD__: string;
const BUILD = typeof __BUILD__ !== "undefined" ? __BUILD__ : "dev";

/**
 * Reference renderer for raster image metadata diffs. Renders the semantic
 * change tree (dimensions / decoded format / color model / byte size) produced
 * by the image-meta handler. A richer side-by-side visual view — the two
 * images blitted in-browser with the metadata changes alongside — is a planned
 * follow-up (issue #28); that view is this renderer's own free choice, since
 * the only contracts are mount() and StructuredDiff.
 */
export default defineRenderer({
  handlerId: "image-meta",
  extensions: [".png", ".jpg", ".jpeg", ".gif"],
  build: BUILD,
  render(container: HTMLElement, props: MountProps) {
    renderDiffTree(container, props);
  },
});
