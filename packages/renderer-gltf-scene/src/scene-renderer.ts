import { defineRenderer } from "@fhr/renderer-sdk";
import type { MountProps, RendererBlobs } from "@fhr/types";
import { createLiveView, type LiveView, type Scene3D, type SceneHooks } from "./live-view.js";

// The 3D scene lives in a separate, heavier chunk (it inlines three.js). The
// lite bundle stays tiny and loads the chunk on demand — so viewing the change
// tree never pays for three.js. Types are declared locally so referencing them
// can't pull three.js into the lite bundle.
type SceneChunk = {
  mount3d(el: HTMLElement, props: MountProps, hooks?: SceneHooks): Promise<Scene3D>;
};

export type SceneRendererOptions = {
  handlerId: string;
  extensions: string[];
  build: string;
  /**
   * File name of this renderer's lazy 3D chunk, resolved as a sibling of the
   * lite bundle's own URL (the host's /renderers proxy serves siblings).
   */
  chunk: string;
  /**
   * Where the 3D view's glTF bytes come from. gltf-scene draws the files
   * themselves (`blobs`, the default). A 3D family member whose files are not
   * glTF draws its handler's GLB preview (`previews`), whose node names are the
   * names its diff paths use — so this viewport needs no per-format mapping.
   * Returning undefined means there is nothing to draw here: the change tree
   * still renders, without the 3D view.
   */
  geometry?: (props: MountProps) => RendererBlobs | undefined;
};

/**
 * A scene renderer: the linked change tree + lazy 3D viewport (live-view.ts)
 * behind the mount() contract. gltf-scene is one; any 3D format that converts
 * to glTF in its handler is another, differing only in its ids, its chunk and
 * where its geometry comes from.
 */
export function defineSceneRenderer(opts: SceneRendererOptions) {
  const geometry = opts.geometry ?? ((props: MountProps) => props.blobs);

  let chunkPromise: Promise<SceneChunk> | null = null;
  // Built as a string (not `new URL(literal, ...)`) so esbuild leaves it a
  // runtime dynamic import rather than trying to bundle three.js in here.
  const loadChunk = (): Promise<SceneChunk> =>
    (chunkPromise ??= import(
      /* @vite-ignore */ import.meta.url.replace(/[^/]*(?:\?.*)?$/, opts.chunk)
    ) as Promise<SceneChunk>);

  /**
   * The live view per container, so an update can find what a render built. A
   * WeakMap rather than a field on the element: the DOM is the host's, and a
   * renderer that hangs state off it leaks into someone else's tree.
   */
  const views = new WeakMap<HTMLElement, LiveView>();

  return defineRenderer({
    handlerId: opts.handlerId,
    extensions: opts.extensions,
    build: opts.build,
    render(container: HTMLElement, props: MountProps) {
      const mountScene = geometry(props)
        ? (host: HTMLElement, sceneProps: MountProps, hooks: SceneHooks) =>
            // The viewport reads `blobs`; hand it the geometry this renderer draws.
            loadChunk().then((chunk) =>
              chunk.mount3d(host, { ...sceneProps, blobs: geometry(sceneProps) }, hooks),
            )
        : null;
      const view = createLiveView(container, props, mountScene);
      views.set(container, view);
      return () => {
        views.delete(container);
        view.dispose();
      };
    },
    /**
     * Non-destructive update (the #45 contract addition). A host that pushes a
     * new selection — the other half of the `select` event this renderer emits —
     * must not pay for a teardown: that would drop the WebGL context, re-fetch
     * and re-parse both models, and put the camera back where it started. The
     * live view patches what it can and declines anything else, which returns
     * the caller to the default teardown path for that push only.
     */
    update(container: HTMLElement, props: MountProps, prev: MountProps) {
      const view = views.get(container);
      if (!view) return false;
      return view.update(props, prev);
    },
  });
}
