import { defineRenderer, formatChange, renderDiffTree } from "@fhr/renderer-sdk";
import type { DiffRow, DiffTreeHandle, FormattedChange, RenderCleanup } from "@fhr/renderer-sdk";
import { renderImageDiff } from "@fhr/renderer-sdk/image";
import type { ImageDiffControl } from "@fhr/renderer-sdk/image";
import type { MountProps } from "@fhr/types";
import { readLayout, type Layout } from "./layout.js";
import { markKey, marksOf } from "./marks.js";

// Replaced at bundle-build time with the release's short commit SHA (see
// build.mjs). Guarded with typeof so importing the source directly (e.g. in a
// unit test, where the define isn't applied) doesn't throw.
declare const __BUILD__: string;
const BUILD = typeof __BUILD__ !== "undefined" ? __BUILD__ : "dev";

/**
 * A PSD change's values. The handler writes them in Photoshop's panels' terms:
 * positions and sizes in canvas pixels, colours as [r, g, b, a] under a label
 * naming the colour, angles with their "°". Pixels are not metres, so lengths
 * are shown as the bare numbers they are.
 */
export function formatPsdChange(row: DiffRow): FormattedChange {
  return formatChange(row, { lengthUnit: "none" });
}

const STYLE = `
.fhr-psd { font: 13px/1.5 ui-sans-serif, system-ui, sans-serif; color: #1f2328; --fhr-line: #d0d7de; --fhr-muted: #57606a; }
.fhr-psd[data-theme="dark"] { color: #e6edf3; --fhr-line: #30363d; --fhr-muted: #8b949e; }
.fhr-psd__main { display: grid; grid-template-columns: minmax(0, 1fr) minmax(260px, 340px); gap: 16px; align-items: start; }
@media (max-width: 900px) { .fhr-psd__main { grid-template-columns: minmax(0, 1fr); } }
.fhr-psd__list { min-width: 0; border: 1px solid var(--fhr-line); border-radius: 6px; overflow: hidden; }
.fhr-psd__note { font-size: 12px; color: var(--fhr-muted); padding: 8px 0; }
`;

/**
 * Photoshop diffs. The handler's preview is the image Photoshop flattened the
 * layers into, with every layer's box: this shows the two images in the image
 * compare (the image, a slider, side by side) and, with "Show differences",
 * outlines the layers that changed — yellow changed, green added, red
 * removed. The changes are listed beside it, and pointing at either lights up
 * the other. Without previews, it draws the change tree alone.
 */
export default defineRenderer({
  handlerId: "psd",
  extensions: [".psd", ".psb"],
  build: BUILD,
  // Draws diffs only: a single file stays with the host's own viewer.
  modes: ["diff"],
  render(container: HTMLElement, props: MountProps): RenderCleanup {
    if (!props.previews?.base && !props.previews?.head) {
      renderDiffTree(container, props, { format: formatPsdChange });
      return;
    }
    return renderCompare(container, props);
  },
});

/** The compare once the previews' layouts are read; the change tree if neither image can be shown. */
function renderCompare(container: HTMLElement, props: MountProps): RenderCleanup {
  const doc = container.ownerDocument;
  let alive = true;
  const undo: (() => void)[] = [];
  const loading = doc.createElement("div");
  loading.className = "fhr-psd__note";
  loading.textContent = "Reading the images…";
  container.appendChild(loading);

  const urls = { base: props.previews?.base?.url, head: props.previews?.head?.url };
  void Promise.all([layoutOf(urls.base), layoutOf(urls.head)]).then(([base, head]) => {
    if (!alive) return;
    loading.remove();
    if (!props.diff || (!base.ok && !head.ok)) {
      renderDiffTree(container, props, { format: formatPsdChange });
      return;
    }
    mount(container, props, { base: base.ok ? urls.base : undefined, head: head.ok ? urls.head : undefined }, base.layout, head.layout, undo);
  });

  return () => {
    alive = false;
    for (const u of undo.splice(0)) u();
  };
}

/** Whether a preview loaded, and its layers' boxes (null when it has none). */
async function layoutOf(url: string | undefined): Promise<{ ok: boolean; layout: Layout | null }> {
  if (!url) return { ok: false, layout: null };
  try {
    const res = await fetch(url);
    if (!res.ok) return { ok: false, layout: null };
    return { ok: true, layout: readLayout(new Uint8Array(await res.arrayBuffer())) };
  } catch {
    return { ok: false, layout: null };
  }
}

function mount(
  container: HTMLElement,
  props: MountProps,
  images: { base?: string; head?: string },
  base: Layout | null,
  head: Layout | null,
  undo: (() => void)[],
): void {
  const doc = container.ownerDocument;
  const make = <K extends keyof HTMLElementTagNameMap>(tag: K, className?: string): HTMLElementTagNameMap[K] => {
    const el = doc.createElement(tag);
    if (className) el.className = className;
    return el;
  };
  const root = make("div", "fhr-psd");
  root.setAttribute("data-theme", props.theme ?? "light");
  root.appendChild(Object.assign(make("style"), { textContent: STYLE }));
  const main = make("div", "fhr-psd__main");
  const compare = make("div");
  const list = make("div", "fhr-psd__list");
  main.append(compare, list);
  root.appendChild(main);
  container.appendChild(root);
  undo.push(() => root.remove());

  let control: ImageDiffControl | null = null;
  let tree: DiffTreeHandle | null = null;
  let pinned: string | null = null;

  // A new or deleted file is one image: outlining every layer of it says nothing.
  const both = images.base !== undefined && images.head !== undefined;
  const marks = both ? marksOf(props.diff!, base, head) : [];

  tree = renderDiffTree(list, props, {
    format: formatPsdChange,
    keyboard: false,
    onSelect: (path) => {
      pinned = markKey(path);
      tree?.select(path);
      control?.highlight(pinned);
    },
  });
  const over = (e: Event): void => {
    const row = (e.target as Element | null)?.closest?.("[data-path]");
    const key = row ? markKey(row.getAttribute("data-path") ?? "") : null;
    control?.highlight(key ?? pinned);
  };
  const out = (): void => control?.highlight(pinned);
  list.addEventListener("pointerover", over);
  list.addEventListener("pointerleave", out);
  const t = tree;
  undo.push(() => {
    list.removeEventListener("pointerover", over);
    list.removeEventListener("pointerleave", out);
    t.dispose();
  });

  const blobs = {
    ...(images.base ? { base: { url: images.base, size: props.previews?.base?.size ?? 0 } } : {}),
    ...(images.head ? { head: { url: images.head, size: props.previews?.head?.size ?? 0 } } : {}),
  };
  const done = renderImageDiff(
    compare,
    { mode: "diff", theme: props.theme ?? "light", blobs },
    {
      marks,
      onReady: (c) => (control = c),
      onMark: (key, picked) => {
        if (picked) pinned = key;
        if (key ?? pinned) tree?.select(key ?? pinned);
      },
    },
  );
  if (typeof done === "function") undo.push(done);
}
