import { defineRenderer, renderDiffTree } from "@fhr/renderer-sdk";
import type { MountProps } from "@fhr/types";

// Replaced at bundle-build time with the release's short commit SHA (see
// build.mjs). Guarded with typeof so importing the source directly (e.g. in a
// unit test, where the define isn't applied) doesn't throw.
declare const __BUILD__: string;
const BUILD = typeof __BUILD__ !== "undefined" ? __BUILD__ : "dev";

/**
 * Reference renderer for SVG diffs. Renders the semantic change tree
 * (elements added / removed / modified, attribute and text changes) produced
 * by the svg handler. A richer view that renders the SVG image itself and
 * tints changed elements in place — with the sanitization that untrusted SVG
 * demands — is a planned follow-up (issue #19). Per the FHR model, that view
 * is this renderer's own free choice; the only contracts are mount() and
 * StructuredDiff.
 */
export default defineRenderer({
  handlerId: "svg",
  extensions: [".svg"],
  build: BUILD,
  render(container: HTMLElement, props: MountProps) {
    renderDiffTree(container, props);
  },
});
