import { defineRenderer, renderDiffTree } from "@fhr/renderer-sdk";
import type { MountProps } from "@fhr/types";

// Replaced at bundle-build time with the release's short commit SHA (see build.mjs).
declare const __BUILD__: string;

/**
 * Reference renderer for glTF/GLB scene diffs.
 *
 * This ships the "lite" DOM change-tree view (SPEC-RENDERING.md §4 default
 * tier) — it renders a StructuredDiff on any client without a 3D viewport,
 * which is what all compute tiers can guarantee. The interactive three.js
 * viewport (`view` mode) is a later phase; it will be ported from ForgeHub's
 * existing GltfSceneView and dropped in behind the same mount() contract.
 */
export default defineRenderer({
  handlerId: "gltf-scene",
  extensions: [".gltf", ".glb"],
  build: __BUILD__,
  render(container: HTMLElement, props: MountProps) {
    if (props.mode === "view") {
      renderViewPlaceholder(container, props);
      return;
    }
    // diff + merge both use the change tree for now.
    renderDiffTree(container, props);
  },
});

function renderViewPlaceholder(container: HTMLElement, props: MountProps): void {
  const doc = container.ownerDocument;
  const box = doc.createElement("div");
  box.style.font = "13px/1.5 ui-sans-serif, system-ui, sans-serif";
  box.style.padding = "16px 4px";
  box.style.color = props.theme === "dark" ? "#8b949e" : "#57606a";
  box.textContent = "3D scene preview is not yet available in this renderer.";
  container.appendChild(box);
}
