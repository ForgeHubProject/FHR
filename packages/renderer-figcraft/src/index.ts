import { defineRenderer, formatChange, renderDiffTree } from "@fhr/renderer-sdk";
import type { DiffRow, DiffTreeHandle, FormattedChange, RenderCleanup } from "@fhr/renderer-sdk";
import { renderImageDiff } from "@fhr/renderer-sdk/image";
import type { ImageDiffControl, ImageMark } from "@fhr/renderer-sdk/image";
import type { DiffChange, MountProps } from "@fhr/types";
import { readSheet, type Sheet, type SheetScreen } from "./sheet.js";
import { markKey, screenViews, type ScreenView } from "./screens.js";

// Replaced at bundle-build time with the release's short commit SHA (see
// build.mjs). Guarded with typeof so importing the source directly (e.g. in a
// unit test, where the define isn't applied) doesn't throw.
declare const __BUILD__: string;
const BUILD = typeof __BUILD__ !== "undefined" ? __BUILD__ : "dev";

/**
 * A FigCraft change's values. The handler writes them in the design panel's
 * terms: positions and sizes in canvas pixels, colours as [r, g, b, a] under a
 * label naming the colour, angles with their "°". Pixels are not metres, so
 * lengths are shown as the bare numbers they are.
 */
export function formatFigcraftChange(row: DiffRow): FormattedChange {
  return formatChange(row, { lengthUnit: "none" });
}

const STYLE = `
.fhr-fig { font: 13px/1.5 ui-sans-serif, system-ui, sans-serif; color: #1f2328; --fhr-line: #d0d7de; --fhr-muted: #57606a;
  --fhr-sel: #eaeef2; --fhr-accent: #0969da; }
.fhr-fig[data-theme="dark"] { color: #e6edf3; --fhr-line: #30363d; --fhr-muted: #8b949e; --fhr-sel: #21262d; --fhr-accent: #2f81f7; }
.fhr-fig__screens { display: flex; flex-wrap: wrap; gap: 6px; align-items: center; margin-bottom: 10px; }
.fhr-fig__screen { font: inherit; font-size: 12px; color: inherit; padding: 3px 10px; border: 1px solid var(--fhr-line);
  border-radius: 6px; background: transparent; cursor: pointer; }
.fhr-fig__screen[aria-pressed="true"] { background: var(--fhr-sel); border-color: var(--fhr-muted); font-weight: 600; }
.fhr-fig__count { color: var(--fhr-muted); font-weight: 400; margin-left: 6px; }
.fhr-fig__tag { font-size: 11px; border-radius: 4px; padding: 0 5px; margin-left: 6px; font-weight: 400; }
.fhr-fig__tag--added { background: rgba(26,127,55,0.15); color: #1a7f37; }
.fhr-fig__tag--removed { background: rgba(207,34,46,0.15); color: #cf222e; }
.fhr-fig[data-theme="dark"] .fhr-fig__tag--added { color: #3fb950; }
.fhr-fig[data-theme="dark"] .fhr-fig__tag--removed { color: #f85149; }
.fhr-fig__layout { display: inline-flex; gap: 6px; align-items: center; font-size: 12px; color: var(--fhr-muted);
  margin-left: auto; cursor: pointer; user-select: none; }
.fhr-fig__main { display: grid; grid-template-columns: minmax(0, 1fr) minmax(260px, 340px); gap: 16px; align-items: start; }
.fhr-fig__main[data-only="list"] { grid-template-columns: minmax(0, 1fr); }
@media (max-width: 900px) { .fhr-fig__main { grid-template-columns: minmax(0, 1fr); } }
.fhr-fig__compare:empty { display: none; }
.fhr-fig__list { min-width: 0; border: 1px solid var(--fhr-line); border-radius: 6px; overflow: hidden; }
.fhr-fig__note { font-size: 12px; color: var(--fhr-muted); padding: 8px 0; }
`;

/**
 * Renderer for FigCraft design files (.figcraft). With the handler's previews
 * (every screen drawn, before and after), it shows one changed screen at a
 * time in the image compare (the image, a slider, side by side), with "Show
 * differences" outlining each change: yellow changed, green added, red
 * removed, grey dashed for what auto layout moved. The screen's changes are
 * listed beside it, and pointing at either lights up the other. Without
 * previews, it draws the change tree alone.
 */
export default defineRenderer({
  handlerId: "figcraft",
  extensions: [".figcraft"],
  build: BUILD,
  // Draws diffs only: a single file stays with the host's own viewer.
  modes: ["diff"],
  render(container: HTMLElement, props: MountProps): RenderCleanup {
    if (!props.previews?.base && !props.previews?.head) {
      renderDiffTree(container, props, { format: formatFigcraftChange });
      return;
    }
    return renderScreens(container, props);
  },
});

/** The screens view, once both previews have loaded; the change tree until then, and if they can't be read. */
function renderScreens(container: HTMLElement, props: MountProps): RenderCleanup {
  const doc = container.ownerDocument;
  let alive = true;
  const urls: string[] = [];
  const cleanups: (() => void)[] = [];
  const loading = doc.createElement("div");
  loading.className = "fhr-fig__note";
  loading.textContent = "Drawing the screens…";
  container.appendChild(loading);

  void Promise.all([sheetOf(props.previews?.base?.url), sheetOf(props.previews?.head?.url)]).then(([base, head]) => {
    if (!alive) return;
    loading.remove();
    if (!props.diff || (!base && !head)) {
      renderDiffTree(container, props, { format: formatFigcraftChange });
      return;
    }
    const { screens, other } = screenViews(props.diff, base?.sheet ?? null, head?.sheet ?? null);
    if (!screens.length) {
      renderDiffTree(container, props, { format: formatFigcraftChange });
      return;
    }
    mountScreens(container, props, { base, head }, screens, other, urls, cleanups, () => alive);
  });

  return () => {
    alive = false;
    for (const c of cleanups.splice(0)) c();
    for (const u of urls) URL.revokeObjectURL(u);
  };
}

type Loaded = { sheet: Sheet; image: HTMLImageElement };

async function sheetOf(url: string | undefined): Promise<Loaded | null> {
  if (!url) return null;
  try {
    const bytes = new Uint8Array(await (await fetch(url)).arrayBuffer());
    const sheet = readSheet(bytes);
    if (!sheet) return null;
    const image = new Image();
    image.src = url;
    await image.decode();
    return { sheet, image };
  } catch {
    return null;
  }
}

/** One screen cut out of a sheet, as an image URL. */
function cut(loaded: Loaded, s: SheetScreen, urls: string[]): Promise<string | undefined> {
  const canvas = document.createElement("canvas");
  canvas.width = s.w;
  canvas.height = s.h;
  canvas.getContext("2d")?.drawImage(loaded.image, s.x, s.y, s.w, s.h, 0, 0, s.w, s.h);
  return new Promise((resolve) =>
    canvas.toBlob((blob) => {
      if (!blob) return resolve(undefined);
      const url = URL.createObjectURL(blob);
      urls.push(url);
      resolve(url);
    }, "image/png"),
  );
}

function mountScreens(
  container: HTMLElement,
  props: MountProps,
  sheets: { base: Loaded | null; head: Loaded | null },
  screens: ScreenView[],
  other: DiffChange[],
  urls: string[],
  cleanups: (() => void)[],
  alive: () => boolean,
): void {
  const doc = container.ownerDocument;
  const make = <K extends keyof HTMLElementTagNameMap>(tag: K, className?: string, text?: string): HTMLElementTagNameMap[K] => {
    const el = doc.createElement(tag);
    if (className) el.className = className;
    if (text !== undefined) el.textContent = text;
    return el;
  };
  const root = make("div", "fhr-fig");
  root.setAttribute("data-theme", props.theme ?? "light");
  root.appendChild(Object.assign(make("style"), { textContent: STYLE }));
  const bar = make("div", "fhr-fig__screens");
  const main = make("div", "fhr-fig__main");
  const compare = make("div", "fhr-fig__compare");
  const list = make("div", "fhr-fig__list");
  main.append(compare, list);
  root.append(bar, main);
  container.appendChild(root);

  let showLayout = false;
  let current: ScreenView | null = null;
  let control: ImageDiffControl | null = null;
  let tree: DiffTreeHandle | null = null;
  let pinned: string | null = null;
  let undo: (() => void)[] = [];
  const cache = new Map<string, Promise<string | undefined>>();
  const image = (side: "base" | "head", s: SheetScreen | null): Promise<string | undefined> => {
    const loaded = sheets[side];
    if (!loaded || !s) return Promise.resolve(undefined);
    const key = `${side}:${s.id}`;
    if (!cache.has(key)) cache.set(key, cut(loaded, s, urls));
    return cache.get(key)!;
  };
  const visible = (v: ScreenView): ImageMark[] => (showLayout ? v.marks : v.marks.filter((m) => m.kind !== "layout"));

  // ---- the screen picker ----
  const buttons: HTMLButtonElement[] = [];
  const pick = (label: string, extra: HTMLElement, go: () => void): void => {
    const b = make("button", "fhr-fig__screen", label);
    b.type = "button";
    b.appendChild(extra);
    b.addEventListener("click", () => {
      for (const x of buttons) x.setAttribute("aria-pressed", String(x === b));
      go();
    });
    buttons.push(b);
    bar.appendChild(b);
  };
  for (const s of screens) {
    const extra =
      s.status === "modified"
        ? make("span", "fhr-fig__count", String(s.count))
        : make("span", `fhr-fig__tag fhr-fig__tag--${s.status}`, s.status === "added" ? "new screen" : "removed");
    pick(s.name, extra, () => void show(s));
  }
  if (other.length) pick("Other changes", make("span", "fhr-fig__count", String(other.length)), () => showOther());
  const layoutToggle = make("label", "fhr-fig__layout");
  const layoutInput = make("input");
  layoutInput.type = "checkbox";
  layoutInput.addEventListener("change", () => {
    showLayout = layoutInput.checked;
    if (current) control?.setMarks(visible(current));
  });
  layoutToggle.append(layoutInput, "Outline layout reflow");
  bar.appendChild(layoutToggle);

  const reset = (): void => {
    for (const u of undo.splice(0)) u();
    control = null;
    tree = null;
    pinned = null;
    compare.textContent = "";
    list.textContent = "";
  };
  cleanups.push(() => {
    for (const u of undo.splice(0)) u();
  });

  // ---- the change list, linked to the outlines ----
  const drawList = (changes: DiffChange[]): void => {
    tree = renderDiffTree(
      list,
      { ...props, diff: { ...props.diff!, changes } },
      {
        format: formatFigcraftChange,
        keyboard: false,
        onSelect: (path) => {
          pinned = markKey(path);
          tree?.select(path);
          control?.highlight(pinned);
        },
      },
    );
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
  };

  const show = async (v: ScreenView): Promise<void> => {
    reset();
    current = v;
    main.removeAttribute("data-only");
    layoutToggle.style.display = v.marks.some((m) => m.kind === "layout") ? "" : "none";
    drawList(v.changes);
    const [base, head] = await Promise.all([image("base", v.base), image("head", v.head)]);
    if (!alive() || current !== v) return;
    const blobs = {
      ...(base ? { base: { url: base, size: 0 } } : {}),
      ...(head ? { head: { url: head, size: 0 } } : {}),
    };
    const done = renderImageDiff(
      compare,
      { mode: "diff", theme: props.theme ?? "light", blobs },
      {
        marks: visible(v),
        onReady: (c) => (control = c),
        onMark: (key, picked) => {
          if (picked) pinned = key;
          if (key ?? pinned) tree?.select(key ?? pinned);
        },
      },
    );
    if (typeof done === "function") undo.push(done);
  };

  const showOther = (): void => {
    reset();
    current = null;
    main.setAttribute("data-only", "list");
    layoutToggle.style.display = "none";
    drawList(other);
  };

  buttons[0]?.setAttribute("aria-pressed", "true");
  void show(screens[0]!);
}
