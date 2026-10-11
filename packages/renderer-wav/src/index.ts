import { defineRenderer, renderDiffTree } from "@fhr/renderer-sdk";
import type { MountProps } from "@fhr/types";

// Replaced at bundle-build time with the release's short commit SHA (see
// build.mjs). Guarded with typeof so importing the source directly (e.g. in a
// unit test, where the define isn't applied) doesn't throw.
declare const __BUILD__: string;
const BUILD = typeof __BUILD__ !== "undefined" ? __BUILD__ : "dev";

/**
 * Reference renderer for WAV audio diffs. Renders the semantic change tree
 * (sample rate / channels / bit depth / audio format / duration / data size)
 * produced by the wav handler. A richer view — rendered waveforms with changed
 * regions highlighted in place — is a planned follow-up (issue #30). Per the
 * FHR model, that view is this renderer's own free choice; the only contracts
 * are mount() and StructuredDiff.
 */
export default defineRenderer({
  handlerId: "wav",
  extensions: [".wav"],
  build: BUILD,
  // A single file is drawn too: hosts have no player of their own for audio.
  modes: ["view", "diff"],
  render(container: HTMLElement, props: MountProps) {
    if (props.mode === "view") return renderPlayer(container, props);
    renderDiffTree(container, props);
  },
});

/**
 * One file: the browser's own audio player on the head blob, which the host
 * serves same-origin. WAV (PCM) plays natively in every current browser.
 */
export function renderPlayer(container: HTMLElement, props: MountProps): void {
  const doc = container.ownerDocument;
  const url = props.blobs?.head?.url;
  const wrap = doc.createElement("div");
  wrap.className = "fhr-wav";
  wrap.style.cssText = "padding:16px;";
  if (!url) {
    wrap.textContent = "No file provided.";
  } else {
    const audio = doc.createElement("audio");
    audio.setAttribute("controls", "");
    audio.setAttribute("preload", "metadata");
    audio.setAttribute("src", url);
    audio.style.cssText = "width:100%;max-width:40rem;";
    wrap.appendChild(audio);
  }
  container.appendChild(wrap);
}
