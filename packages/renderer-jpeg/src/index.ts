import { defineRenderer, renderDiffTree } from "@fhr/renderer-sdk";
import type { MountProps } from "@fhr/types";

// Replaced at bundle-build time with the release's short commit SHA (see
// build.mjs). Guarded with typeof so importing the source directly (e.g. in a
// unit test, where the define isn't applied) doesn't throw.
declare const __BUILD__: string;
const BUILD = typeof __BUILD__ !== "undefined" ? __BUILD__ : "dev";

/**
 * Renderer for JPEG images. A single file is the image itself; a diff is the
 * semantic change tree (dimensions / color model / byte size) produced by the
 * jpeg handler.
 */
export default defineRenderer({
  handlerId: "jpeg",
  extensions: [".jpg", ".jpeg"],
  build: BUILD,
  // A single file is drawn too: the image, not its bytes as text.
  modes: ["view", "diff"],
  render(container: HTMLElement, props: MountProps) {
    if (props.mode === "view") return renderImage(container, props);
    renderDiffTree(container, props);
  },
});

/**
 * One file: the head blob, which the host serves same-origin, as an <img> on a
 * checkerboard so transparent pixels read as transparent.
 */
export function renderImage(container: HTMLElement, props: MountProps): void {
  const doc = container.ownerDocument;
  const url = props.blobs?.head?.url;
  const [a, b] = props.theme === "dark" ? ["#161b22", "#21262d"] : ["#ffffff", "#eaeef2"];
  const wrap = doc.createElement("div");
  wrap.className = "fhr-image";
  wrap.style.cssText =
    "padding:16px;text-align:center;" +
    `background:repeating-conic-gradient(${a} 0 25%,${b} 0 50%) 50%/16px 16px;`;
  if (!url) {
    wrap.textContent = "No file provided.";
  } else {
    const img = doc.createElement("img");
    img.setAttribute("src", url);
    img.setAttribute("alt", "");
    img.style.cssText = "max-width:100%;height:auto;";
    wrap.appendChild(img);
  }
  container.appendChild(wrap);
}
