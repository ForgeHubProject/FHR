import { defineRenderer, renderDiffTree } from "@fhr/renderer-sdk";
import type { MountProps } from "@fhr/types";

// Replaced at bundle-build time with the release's short commit SHA (see
// build.mjs). Guarded with typeof so importing the source directly (e.g. in a
// unit test, where the define isn't applied) doesn't throw.
declare const __BUILD__: string;
const BUILD = typeof __BUILD__ !== "undefined" ? __BUILD__ : "dev";

/**
 * Reference renderer for YAML diffs. Renders the semantic change tree (dotted
 * key paths with added / removed / modified values) produced by the yaml
 * handler. A richer config-aware view — e.g. showing changed keys inline in
 * the surrounding document context — is a planned follow-up (issue #26). Per
 * the FHR model, that view is this renderer's own free choice; the only
 * contracts are mount() and StructuredDiff.
 */
export default defineRenderer({
  handlerId: "yaml",
  extensions: [".yaml", ".yml"],
  build: BUILD,
  render(container: HTMLElement, props: MountProps) {
    renderDiffTree(container, props);
  },
});
