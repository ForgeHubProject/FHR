import type { MountProps } from "@fhr/types";
import type { RenderCleanup } from "./define.js";

// The floor for a `view` the renderer does not draw itself (see
// DefineRendererOptions.modes): the file's own text. A host that reads `modes`
// never mounts one; a host that predates the field still does, and a renderer
// built only for diffs would otherwise answer it with an empty panel ("No diff
// provided.") in place of the file.

/** Bytes inspected for a NUL when deciding whether the file is text. */
const BINARY_SNIFF = 8000;

const LIGHT = "color:#1f2328;background:#ffffff;";
const DARK = "color:#e6edf3;background:#0d1117;";

/** True when the start of `text` holds a NUL: a binary file decoded as text. */
export function looksBinary(text: string): boolean {
  return text.slice(0, BINARY_SNIFF).includes("\u0000");
}

/**
 * Show the head blob's text in `container`. Fetches `props.blobs.head.url`
 * (served same-origin by the host, like every blob a renderer reads); the
 * returned cleanup drops a fetch still in flight.
 */
export function renderSourceView(container: HTMLElement, props: MountProps): RenderCleanup {
  const pre = container.ownerDocument.createElement("pre");
  pre.className = "fhr-source";
  pre.style.cssText =
    "margin:0;padding:8px 12px;overflow:auto;white-space:pre;tab-size:4;" +
    "font:12px/1.5 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;" +
    (props.theme === "dark" ? DARK : LIGHT);
  container.appendChild(pre);

  const url = props.blobs?.head?.url;
  if (!url) {
    pre.textContent = "No file provided.";
    return;
  }
  let live = true;
  pre.textContent = "Loading…";
  fetch(url)
    .then((res) => {
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      return res.text();
    })
    .then((text) => {
      if (live) pre.textContent = looksBinary(text) ? "Binary file: no text to show." : text;
    })
    .catch((err: unknown) => {
      if (live) pre.textContent = `Could not load this file (${err instanceof Error ? err.message : String(err)}).`;
    });
  return () => {
    live = false;
  };
}
