import { defineRenderer, renderDiffTree } from "@fhr/renderer-sdk";
import type { MountProps } from "@fhr/types";

// Replaced at bundle-build time with the release's short commit SHA (see
// build.mjs). Guarded with typeof so importing the source directly (e.g. in a
// unit test, where the define isn't applied) doesn't throw.
declare const __BUILD__: string;
const BUILD = typeof __BUILD__ !== "undefined" ? __BUILD__ : "dev";

/**
 * Reference renderer for Jupyter notebook diffs. Renders the semantic change
 * tree (per-cell add / remove / modify, with type + source changes) produced by
 * the ipynb handler. A richer view that renders markdown/code cells with an
 * in-place source diff is a planned follow-up (issue #18) — and that view is
 * this renderer's own free choice; the only contracts are mount() and
 * StructuredDiff.
 */
export default defineRenderer({
  handlerId: "ipynb",
  extensions: [".ipynb"],
  build: BUILD,
  render(container: HTMLElement, props: MountProps) {
    renderDiffTree(container, props);
  },
});
