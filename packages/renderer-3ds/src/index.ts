import type { MountProps } from "@fhr/types";
import { defineSceneRenderer, type SceneRendererOptions } from "@fhr/renderer-gltf-scene/scene-renderer";

// Replaced at bundle-build time with the release's short commit SHA (see
// build.mjs). Guarded with typeof so importing the source directly (e.g. in a
// unit test, where the define isn't applied) doesn't throw.
declare const __BUILD__: string;
const BUILD = typeof __BUILD__ !== "undefined" ? __BUILD__ : "dev";

/**
 * 3DS is a 3D family member (#67), so it renders on the gltf-scene
 * review surface — the linked change tree and the lazy 3D viewport — rather
 * than a viewer of its own.
 *
 * The browser never parses 3DS. The 3ds handler diffs 3DS by converting it to a
 * glTF document, and its `preview` call returns that document as a GLB
 * (SPEC.md §7), so the viewport draws `previews` and every change path in the
 * diff names a node that exists in what it draws. With no preview from the
 * host, the change tree is the whole view.
 */
export const sceneOptions: SceneRendererOptions = {
  handlerId: "3ds",
  extensions: [".3ds"],
  build: BUILD,
  chunk: "renderer-3ds-3d.js",
  geometry: (props: MountProps) => props.previews,
};

export default defineSceneRenderer(sceneOptions);
