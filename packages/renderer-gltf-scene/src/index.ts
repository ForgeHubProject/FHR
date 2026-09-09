import { defineRenderer } from "@fhr/renderer-sdk";
import type { MountProps } from "@fhr/types";
import { createLiveView, type LiveView, type Scene3D, type SceneHooks } from "./live-view.js";

// Replaced at bundle-build time with the release's short commit SHA (see build.mjs).
declare const __BUILD__: string;

// The 3D scene lives in a separate, heavier chunk (it inlines three.js). This
// lite bundle stays tiny and loads the chunk on demand — so viewing the change
// tree never pays for three.js. Types are declared locally so referencing them
// can't pull three.js into this bundle.
type SceneChunk = {
  mount3d(el: HTMLElement, props: MountProps, hooks?: SceneHooks): Promise<Scene3D>;
};

// Resolve the 3D chunk as a sibling of this module's URL. Built as a string
// (not `new URL(literal, ...)`) so esbuild leaves it a runtime dynamic import
// rather than trying to bundle three.js in here.
function chunkUrl(): string {
  return import.meta.url.replace(/[^/]*(?:\?.*)?$/, "renderer-gltf-scene-3d.js");
}
let chunkPromise: Promise<SceneChunk> | null = null;
function loadChunk(): Promise<SceneChunk> {
  if (!chunkPromise) chunkPromise = import(/* @vite-ignore */ chunkUrl()) as Promise<SceneChunk>;
  return chunkPromise;
}

/**
 * The live view per container, so an update can find what a render built. A
 * WeakMap rather than a field on the element: the DOM is the host's, and a
 * renderer that hangs state off it leaks into someone else's tree.
 */
const views = new WeakMap<HTMLElement, LiveView>();

/**
 * Reference renderer for glTF/GLB scene diffs. The change-tree view is the
 * always-available default; the interactive 3D scene loads on demand behind the
 * same mount() contract, and the two are one linked review surface (see
 * live-view.ts). three.js is this renderer's private choice of how to draw its
 * scene — it is not a shared FHR contract.
 */
export default defineRenderer({
  handlerId: "gltf-scene",
  extensions: [".gltf", ".glb"],
  build: __BUILD__,
  render(container: HTMLElement, props: MountProps) {
    const view = createLiveView(container, props, (host, sceneProps, hooks) =>
      loadChunk().then((chunk) => chunk.mount3d(host, sceneProps, hooks)),
    );
    views.set(container, view);
    return () => {
      views.delete(container);
      view.dispose();
    };
  },
  /**
   * Non-destructive update (the #45 contract addition). A host that pushes a new
   * selection — the other half of the `select` event this renderer emits — must
   * not pay for a teardown: that would drop the WebGL context, re-fetch and
   * re-parse both models, and put the camera back where it started. The live view
   * patches what it can and declines anything else, which returns the caller to
   * the default teardown path for that push only.
   */
  update(container: HTMLElement, props: MountProps, prev: MountProps) {
    const view = views.get(container);
    if (!view) return false;
    return view.update(props, prev);
  },
});
