import { defineRenderer, renderDiffTree } from "@fhr/renderer-sdk";
import type { MountProps } from "@fhr/types";

// Replaced at bundle-build time with the release's short commit SHA (see
// build.mjs). Guarded with typeof so importing the source directly (e.g. in a
// unit test, where the define isn't applied) doesn't throw.
declare const __BUILD__: string;
const BUILD = typeof __BUILD__ !== "undefined" ? __BUILD__ : "dev";

/**
 * Reference renderer for Wavefront OBJ diffs. Renders the semantic change tree
 * the obj handler produces — the scene engine's objects, geometry and
 * materials, plus the OBJ-only material-library and uninterpreted-statement
 * rows. The 3D view arrives by mounting the handler's GLB preview in the
 * gltf-scene viewport (#67); the tree is the floor every host can show.
 */
export default defineRenderer({
  handlerId: "obj",
  extensions: [".obj"],
  build: BUILD,
  render(container: HTMLElement, props: MountProps) {
    renderDiffTree(container, props);
  },
});
