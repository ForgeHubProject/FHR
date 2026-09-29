import { defineSceneRenderer } from "./scene-renderer.js";

// Replaced at bundle-build time with the release's short commit SHA (see build.mjs).
declare const __BUILD__: string;

/**
 * Reference renderer for glTF/GLB scene diffs. The change-tree view is the
 * always-available default; the interactive 3D scene loads on demand behind the
 * same mount() contract, and the two are one linked review surface (see
 * live-view.ts). three.js is this renderer's private choice of how to draw its
 * scene — it is not a shared FHR contract.
 *
 * The files are glTF, so the viewport draws them directly (`blobs`). Other 3D
 * formats reuse this whole surface through defineSceneRenderer with their
 * handler's GLB preview instead (renderer-obj).
 */
export default defineSceneRenderer({
  handlerId: "gltf-scene",
  extensions: [".gltf", ".glb"],
  build: __BUILD__,
  chunk: "renderer-gltf-scene-3d.js",
});
