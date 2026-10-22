// The image compare: how a raster image's diff reads — the picture first, the
// metadata on request. One module bundled by each image format's renderer
// (png, jpeg, gif, …), so every format draws the same three views:
//
//   * Image — the new image alone at its true size, as a host shows an added
//     file. The default: most reviews only need to see the result.
//   * Slider — both images stacked, top-left aligned, with a divider to drag:
//     before to its left, after to its right.
//   * Side by side — before and after in two panes that zoom and pan
//     independently of each other.
//
// Two toggles, both off by default: "Show differences" tints every pixel that
// changed (in whichever view is open) and offers to zoom to them; "Show
// metadata" adds the handler's change tree (dimensions, colour model, size).
//
// An animated format hands in a decoder (`decodeAnimation`). Its diff then
// gets a frame strip that marks which frames changed, plays the animation, and
// draws every view — and the differences — for the frame on screen.
//
// An added or deleted file has one side only: just that image.

import type { MountProps } from "@fhr/types";
import type { RenderCleanup } from "../define.js";
import { renderDiffTree } from "../dom.js";
import { createViewport, type Size, type ViewportHandle } from "./viewport.js";
import { describeDiff, diffPixels, overlayPixels, samePixels, type PixelDiff, type Pixels } from "./pixels.js";
import { IMAGE_STYLE } from "./style.js";

/** The views every image diff offers, in toolbar order (for a host picker, FHR #88). */
export const IMAGE_VIEWS = [
  { id: "image", label: "Image" },
  { id: "slider", label: "Slider" },
  { id: "side-by-side", label: "Side by side" },
] as const;
export type ImageViewId = (typeof IMAGE_VIEWS)[number]["id"];

/** A decoded animation: every frame composited onto the full canvas. */
export type AnimatedImage = {
  width: number;
  height: number;
  frameCount: number;
  /** How long frame `index` shows, in milliseconds. */
  delayMs(index: number): number;
  /** Frame `index` as RGBA, width × height × 4 bytes. Valid until the next call. */
  frame(index: number): Uint8ClampedArray;
};

export type ImageDiffOptions = {
  /**
   * Decodes an animated format's frames for the per-frame compare. Return null
   * when the bytes can't be read; the image then compares as a still.
   */
  decodeAnimation?: (bytes: Uint8Array) => AnimatedImage | null;
  /** The view to open in. Default "image". */
  view?: ImageViewId;
};

type Side = "base" | "head";
const SIDES: readonly Side[] = ["base", "head"];
const SIDE_LABEL: Record<Side, string> = { base: "Before", head: "After" };

/** The shortest window, so the zoom controls fit beside a 16 px icon. */
const MIN_PORT = 160;
/** Larger images skip the pixel compare: two RGBA copies of 40 Mpx is 320 MB. */
const MAX_COMPARE_PIXELS = 40_000_000;
/** Pixels of frame compare per slice before yielding, so a long GIF can't freeze the page. */
const SCAN_SLICE = 4_000_000;

type Port = { vp: ViewportHandle; sides: Side[] };
type Overlay = { canvas: HTMLCanvasElement; bounds: HTMLElement; vp: ViewportHandle };

export function renderImageDiff(container: HTMLElement, props: MountProps, opts: ImageDiffOptions = {}): RenderCleanup {
  const doc = container.ownerDocument;
  const make = <K extends keyof HTMLElementTagNameMap>(tag: K, className?: string, text?: string): HTMLElementTagNameMap[K] => {
    const el = doc.createElement(tag);
    if (className) el.className = className;
    if (text !== undefined) el.textContent = text;
    return el;
  };

  const root = make("div", "fhr-img");
  root.setAttribute("data-theme", props.theme ?? "light");
  const style = make("style");
  style.textContent = IMAGE_STYLE;
  root.appendChild(style);
  container.appendChild(root);

  const urls: Partial<Record<Side, string>> = { base: props.blobs?.base?.url, head: props.blobs?.head?.url };
  const shown = SIDES.filter((s) => urls[s]);
  if (shown.length === 0) {
    // No image reached the renderer: the change tree is all there is to show.
    renderDiffTree(root, props);
    return;
  }
  const both = shown.length === 2;

  let alive = true;
  const timers = new Set<ReturnType<typeof setTimeout>>();
  const later = (fn: () => void, ms: number): ReturnType<typeof setTimeout> => {
    const t = setTimeout(() => {
      timers.delete(t);
      if (alive) fn();
    }, ms);
    timers.add(t);
    return t;
  };
  const cancel = (t: ReturnType<typeof setTimeout> | undefined): void => {
    if (t === undefined) return;
    clearTimeout(t);
    timers.delete(t);
  };

  let view: ImageViewId = both ? opts.view ?? "image" : "image";
  let showDiff = false;

  // ---- toolbar ----
  const bar = make("div", "fhr-img__bar");
  const tabs = new Map<ImageViewId, HTMLElement>();
  const toggle = (label: string, onChange: (on: boolean) => void): HTMLElement => {
    const wrap = make("label", "fhr-img__toggle");
    const input = make("input");
    input.setAttribute("type", "checkbox");
    input.addEventListener("change", () => onChange(input.checked));
    wrap.append(input, label);
    return wrap;
  };
  if (both) {
    const group = make("div", "fhr-img__views");
    group.setAttribute("role", "tablist");
    group.setAttribute("aria-label", "View");
    for (const v of IMAGE_VIEWS) {
      const b = make("button", "fhr-img__view", v.label);
      b.setAttribute("type", "button");
      b.setAttribute("role", "tab");
      b.setAttribute("data-view", v.id);
      b.addEventListener("click", () => {
        if (view === v.id) return;
        view = v.id;
        build();
      });
      tabs.set(v.id, b);
      group.appendChild(b);
    }
    bar.appendChild(group);
    bar.appendChild(
      toggle("Show differences", (on) => {
        // On the windows already open, so the zoom, pan and divider stay put.
        showDiff = on;
        if (on) for (const p of ports) attachOverlay(p.vp);
        else {
          for (const o of overlays) {
            o.canvas.remove();
            o.bounds.remove();
          }
          overlays = [];
        }
        for (const p of ports) p.vp.setContent(contentOf(p.sides));
        refreshDiff();
      }),
    );
  }
  if (props.diff) {
    bar.appendChild(
      toggle("Show metadata", (on) => {
        clear(meta);
        if (on) renderDiffTree(meta, props);
      }),
    );
  }
  if (bar.childNodes.length) root.appendChild(bar);

  const frames = make("div", "fhr-img__frames");
  const note = make("div", "fhr-img__note");
  note.setAttribute("aria-live", "polite");
  const body = make("div", "fhr-img__body");
  const meta = make("div", "fhr-img__meta");
  root.append(frames, note, body, meta);

  // ---- image sizes, learnt as the images load ----
  const sizes: Record<Side, Size | null> = { base: null, head: null };
  const failed = new Set<Side>();
  const union = (sides: readonly Side[]): Size => ({
    w: Math.max(0, ...sides.map((s) => sizes[s]?.w ?? 0)),
    h: Math.max(0, ...sides.map((s) => sizes[s]?.h ?? 0)),
  });
  const portHeight = (): number => {
    const tall = Math.max(320, Math.round(((globalThis as { innerHeight?: number }).innerHeight ?? 900) * 0.7));
    return Math.min(tall, Math.max(MIN_PORT, union(shown).h + 32));
  };
  // With the overlay on, every window spans both images so the tint lines up.
  const contentOf = (sides: readonly Side[]): Size => union(showDiff ? shown : sides);

  let ports: Port[] = [];
  // The window the reviewer last zoomed or panned, and the place (image point
  // at the centre, zoom) a new view opens at: theirs, once they have moved.
  let moved: ViewportHandle | null = null;
  let place: { x: number; y: number; zoom: number } | null = null;
  // The slider's divider, as a fraction of the window: it outlives a view switch.
  let sliderAt = 0.5;
  let painters: { side: Side; canvas: HTMLCanvasElement }[] = [];
  let overlays: Overlay[] = [];
  let dimLabels: { side: Side; el: HTMLElement }[] = [];

  const sized = (side: Side, w: number, h: number): void => {
    const known = sizes[side];
    if (known && known.w === w && known.h === h) return;
    sizes[side] = { w, h };
    relayout();
  };
  const relayout = (): void => {
    for (const p of ports) {
      p.vp.setHeight(portHeight());
      p.vp.setContent(contentOf(p.sides));
    }
    for (const d of dimLabels) d.el.textContent = dims(sizes[d.side]);
  };

  // ---- animation state (an animated format's diff only) ----
  let anim: Record<Side, AnimatedImage> | null = null;
  let current = 0;
  let frameCount = 0;
  let playing = false;
  let playTimer: ReturnType<typeof setTimeout> | undefined;
  const pixelsSame: (boolean | undefined)[] = [];
  let cells: HTMLElement[] = [];
  const frameInfo = make("span");
  const frameSummary = make("span");
  const playButton = make("button", "fhr-img__play");

  // ---- layers ----
  const layer = (side: Side): HTMLElement => {
    if (anim) {
      const a = anim[side];
      const c = make("canvas", "fhr-img__layer");
      c.width = a.width;
      c.height = a.height;
      c.setAttribute("role", "img");
      c.setAttribute("aria-label", SIDE_LABEL[side]);
      painters.push({ side, canvas: c });
      paint([{ side, canvas: c }]);
      return c;
    }
    const img = make("img", "fhr-img__layer");
    img.setAttribute("alt", both ? SIDE_LABEL[side] : "");
    img.setAttribute("draggable", "false");
    img.addEventListener("load", () => sized(side, img.naturalWidth, img.naturalHeight));
    img.addEventListener("error", () => {
      if (failed.has(side)) return;
      failed.add(side);
      writeNote();
    });
    img.setAttribute("src", urls[side]!);
    return img;
  };

  const paint = (targets: { side: Side; canvas: HTMLCanvasElement }[]): void => {
    if (!anim) return;
    for (const side of SIDES) {
      const own = targets.filter((t) => t.side === side);
      if (!own.length) continue;
      const a = anim[side];
      const present = current < a.frameCount;
      const data = present ? a.frame(current) : null;
      for (const { canvas } of own) {
        canvas.setAttribute("data-absent", present ? "0" : "1");
        if (!data) continue;
        const ctx = canvas.getContext?.("2d");
        if (!ctx) continue;
        const img = ctx.createImageData(a.width, a.height);
        img.data.set(data);
        ctx.putImageData(img, 0, 0);
      }
    }
  };

  const addPort = (sides: Side[]): { vp: ViewportHandle; layers: Map<Side, HTMLElement> } => {
    const vp = createViewport(doc, portHeight());
    const layers = new Map<Side, HTMLElement>();
    for (const s of sides) {
      const l = layer(s);
      layers.set(s, l);
      vp.stage.appendChild(l);
    }
    if (showDiff) attachOverlay(vp);
    vp.setContent(contentOf(sides));
    // Capture phase: the zoom controls stop their own presses from reaching
    // the window (so they don't start a pan), but they still count as a move.
    for (const type of ["pointerdown", "click", "wheel"]) vp.el.addEventListener(type, () => (moved = vp), true);
    ports.push({ vp, sides });
    return { vp, layers };
  };

  /** The differences layer: a tint over the stage, and a box around the changes. */
  const attachOverlay = (vp: ViewportHandle): void => {
    const canvas = make("canvas", "fhr-img__overlay");
    canvas.width = 0;
    canvas.height = 0;
    vp.stage.appendChild(canvas);
    // The box sits in window space, outside the zoomed stage, so its outline
    // stays two pixels wide at any zoom.
    const bounds = make("div", "fhr-img__bounds");
    bounds.style.display = "none";
    vp.el.appendChild(bounds);
    const o = { canvas, bounds, vp };
    overlays.push(o);
    vp.onChange(() => placeBounds(o));
  };

  // ---- views ----
  const build = (): void => {
    // Switching views must not cost the reviewer their place (SPEC-RENDERING
    // §2, chrome convention): the new windows open on the same image point at
    // the same zoom as the window last moved. Side by side, each pane starts
    // there and then goes its own way.
    if (moved && ports.some((p) => p.vp === moved)) place = moved.center();
    moved = null;
    for (const p of ports) p.vp.destroy();
    ports = [];
    painters = [];
    overlays = [];
    dimLabels = [];
    clear(body);
    root.setAttribute("data-view", view);
    for (const [id, b] of tabs) b.setAttribute("aria-selected", String(id === view));

    if (!both) body.appendChild(addPort([shown[0]!]).vp.el);
    else if (view === "slider") buildSlider();
    else if (view === "side-by-side") buildSideBySide();
    else body.appendChild(addPort(["head"]).vp.el);
    if (place) for (const p of ports) p.vp.centerOn(place.x, place.y, place.zoom);
    paintOverlays();
  };

  const buildSideBySide = (): void => {
    const panes = make("div", "fhr-img__panes");
    for (const side of SIDES) {
      const pane = make("div", "fhr-img__pane");
      const label = make("div", "fhr-img__label");
      const size = make("span", undefined, dims(sizes[side]));
      dimLabels.push({ side, el: size });
      label.append(make("strong", undefined, SIDE_LABEL[side]), size);
      pane.append(label, addPort([side]).vp.el);
      panes.appendChild(pane);
    }
    body.appendChild(panes);
  };

  const buildSlider = (): void => {
    const { vp, layers } = addPort(["base", "head"]);
    const after = layers.get("head")!;
    const divider = make("div", "fhr-img__divider");
    divider.setAttribute("role", "slider");
    divider.setAttribute("tabindex", "0");
    divider.setAttribute("aria-label", "Before and after divider");
    divider.setAttribute("aria-valuemin", "0");
    divider.setAttribute("aria-valuemax", "100");
    const before = make("span", "fhr-img__badge", "Before");
    before.style.left = "8px";
    const afterBadge = make("span", "fhr-img__badge", "After");
    afterBadge.style.right = "8px";
    vp.el.append(divider, before, afterBadge);

    // The divider sits in window space: zooming or panning moves the images
    // under it, the way a reviewer scrubs a fixed line across a detail.
    const place = (): void => {
      const w = vp.port().w;
      const x = sliderAt * w;
      divider.style.left = `${x}px`;
      divider.setAttribute("aria-valuenow", String(Math.round(sliderAt * 100)));
      const v = vp.view;
      after.style.clipPath = `inset(0 0 0 ${Math.max(0, (x - v.x) / v.zoom)}px)`;
    };
    vp.onChange(place);
    let dragging = false;
    const setFrom = (clientX: number): void => {
      const r = (vp.el as Partial<HTMLElement>).getBoundingClientRect?.();
      if (!r || r.width <= 0) return;
      sliderAt = Math.min(1, Math.max(0, (clientX - r.left) / r.width));
      place();
    };
    divider.addEventListener("pointerdown", (raw) => {
      const e = raw as PointerEvent;
      e.stopPropagation?.();
      e.preventDefault?.();
      dragging = true;
      if (e.pointerId !== undefined) (divider as Partial<HTMLElement>).setPointerCapture?.(e.pointerId);
    });
    divider.addEventListener("pointermove", (raw) => {
      if (dragging) setFrom((raw as PointerEvent).clientX);
    });
    const release = (): void => {
      dragging = false;
    };
    divider.addEventListener("pointerup", release);
    divider.addEventListener("pointercancel", release);
    divider.addEventListener("keydown", (raw) => {
      const e = raw as KeyboardEvent;
      const step = e.shiftKey ? 0.1 : 0.02;
      const next =
        e.key === "ArrowLeft" ? sliderAt - step : e.key === "ArrowRight" ? sliderAt + step : e.key === "Home" ? 0 : e.key === "End" ? 1 : null;
      if (next === null) return;
      e.preventDefault?.();
      sliderAt = Math.min(1, Math.max(0, next));
      place();
    });
    place();
    body.appendChild(vp.el);
  };

  // ---- differences ----
  let diff: PixelDiff | null = null;
  let diffProblem = "";
  let stillDiff: Promise<void> | null = null;

  const rasterize = (url: string): Promise<Pixels> =>
    new Promise<HTMLImageElement>((resolve, reject) => {
      const img = make("img");
      img.addEventListener("load", () => resolve(img));
      img.addEventListener("error", () => reject(new Error("the browser couldn't decode it")));
      img.setAttribute("src", url);
    }).then((img) => {
      const w = img.naturalWidth;
      const h = img.naturalHeight;
      if (w * h > MAX_COMPARE_PIXELS) throw new Error("the image is too large to compare pixel by pixel");
      const canvas = make("canvas");
      canvas.width = w;
      canvas.height = h;
      const ctx = canvas.getContext?.("2d", { willReadFrequently: true });
      if (!ctx) throw new Error("this browser has no canvas to compare on");
      ctx.drawImage(img, 0, 0);
      return { width: w, height: h, data: ctx.getImageData(0, 0, w, h).data };
    });

  const frameDiff = (i: number): PixelDiff => {
    const a = anim!.base;
    const b = anim!.head;
    const pa = i < a.frameCount ? { width: a.width, height: a.height, data: a.frame(i) } : null;
    const pb = i < b.frameCount ? { width: b.width, height: b.height, data: b.frame(i) } : null;
    return diffPixels(pa, pb);
  };

  const refreshDiff = (): void => {
    if (!showDiff || !both) {
      writeNote();
      return;
    }
    if (anim) {
      diff = frameDiff(current);
      paintOverlays();
      writeNote();
      return;
    }
    if (diff || diffProblem) {
      paintOverlays();
      writeNote();
      return;
    }
    writeNote("Comparing pixels…");
    stillDiff ??= Promise.all([rasterize(urls.base!), rasterize(urls.head!)])
      .then(([a, b]) => {
        if (!alive) return;
        // The open view may show one side only; the note names both sizes.
        sized("base", a.width, a.height);
        sized("head", b.width, b.height);
        diff = diffPixels(a, b);
      })
      .catch((e: unknown) => {
        diffProblem = `Pixels can't be compared here: ${e instanceof Error ? e.message : String(e)}.`;
      })
      .then(() => {
        if (!alive) return;
        paintOverlays();
        writeNote();
      });
  };

  const paintOverlays = (): void => {
    const d = showDiff ? diff : null;
    const tint = d && d.count ? overlayPixels(d) : null;
    for (const o of overlays) {
      placeBounds(o);
      o.canvas.width = tint ? d!.width : 0;
      o.canvas.height = tint ? d!.height : 0;
      if (!tint) continue;
      const ctx = o.canvas.getContext?.("2d");
      if (!ctx) continue;
      const img = ctx.createImageData(d!.width, d!.height);
      img.data.set(tint);
      ctx.putImageData(img, 0, 0);
    }
  };

  const placeBounds = (o: Overlay): void => {
    if (!overlays.includes(o)) return; // detached by the toggle
    const b = showDiff ? diff?.bounds : null;
    o.bounds.style.display = b ? "" : "none";
    if (!b) return;
    const v = o.vp.view;
    o.bounds.style.left = `${v.x + b.x * v.zoom}px`;
    o.bounds.style.top = `${v.y + b.y * v.zoom}px`;
    o.bounds.style.width = `${b.w * v.zoom}px`;
    o.bounds.style.height = `${b.h * v.zoom}px`;
  };

  const writeNote = (status?: string): void => {
    clear(note);
    const problems = [...failed].map((s) => `The browser can't display the ${s === "base" ? "before" : "after"} image.`);
    if (problems.length) note.append(problems.join(" "));
    if (!showDiff || !both) return;
    if (status) return note.append(status);
    if (diffProblem) return note.append(diffProblem);
    if (!diff) return;
    note.append(anim ? `Frame ${current + 1}: ${describeDiff(diff)}` : describeDiff(diff));
    const a = sizes.base;
    const b = sizes.head;
    if (a && b && (a.w !== b.w || a.h !== b.h)) note.append(make("span", undefined, `${dims(a)} → ${dims(b)}, compared from the top-left corner`));
    const bounds = diff.bounds;
    if (bounds) {
      const zoom = make("button", "fhr-img__link", diff.count === 1 ? "Zoom to it" : "Zoom to them");
      zoom.setAttribute("type", "button");
      zoom.addEventListener("click", () => {
        for (const p of ports) p.vp.focus(bounds);
        moved = ports[0]?.vp ?? null; // a move like any other: a view switch keeps it
      });
      note.appendChild(zoom);
    }
  };

  // ---- frames (animated formats) ----
  const go = (i: number): void => {
    current = i;
    paint(painters);
    cells.forEach((c, k) => c.setAttribute("aria-current", String(k === i)));
    revealCell(i);
    describeFrame();
    if (showDiff) refreshDiff();
  };

  // Keep the current frame's cell in sight by scrolling the strip itself —
  // scrollIntoView would also scroll the page to the strip on every frame.
  let cellList: HTMLElement | null = null;
  const revealCell = (i: number): void => {
    const c = cells[i];
    if (!c || !cellList) return;
    const left = c.offsetLeft;
    const room = cellList.clientWidth;
    if (left < cellList.scrollLeft || left + c.offsetWidth > cellList.scrollLeft + room) {
      cellList.scrollLeft = Math.max(0, left - room / 2);
    }
  };

  const delayOf = (i: number): number => {
    const { base, head } = anim!;
    return i < head.frameCount ? head.delayMs(i) : base.delayMs(i);
  };

  const describeFrame = (): void => {
    const { base, head } = anim!;
    const parts = [`Frame ${current + 1} of ${frameCount}`];
    const inBase = current < base.frameCount;
    const inHead = current < head.frameCount;
    if (inBase && inHead) {
      const da = base.delayMs(current);
      const db = head.delayMs(current);
      parts.push(da === db ? `${db} ms` : `${da} → ${db} ms`);
      const same = pixelsSame[current];
      if (same !== undefined) parts.push(same ? "same pixels" : "pixels differ");
    } else {
      parts.push(`${delayOf(current)} ms`, inHead ? "only after" : "only before");
    }
    frameInfo.textContent = parts.join(" · ");
  };

  const setPlaying = (on: boolean): void => {
    playing = on;
    playButton.textContent = on ? "❚❚" : "▶";
    playButton.setAttribute("aria-label", on ? "Pause" : "Play");
    playButton.setAttribute("title", on ? "Pause" : "Play");
    cancel(playTimer);
    playTimer = undefined;
    if (on) schedule();
  };
  const schedule = (): void => {
    playTimer = later(() => {
      go((current + 1) % frameCount);
      if (playing) schedule();
    }, delayOf(current));
  };

  const startFrames = (): void => {
    const { base, head } = anim!;
    frameCount = Math.max(base.frameCount, head.frameCount);
    const strip = make("div", "fhr-img__strip");
    playButton.setAttribute("type", "button");
    playButton.addEventListener("click", () => setPlaying(!playing));
    const list = make("div", "fhr-img__cells");
    list.setAttribute("aria-label", "Frames");
    cellList = list;
    cells = [];
    for (let i = 0; i < frameCount; i++) {
      const cell = make("button", "fhr-img__cell", String(i + 1));
      cell.setAttribute("type", "button");
      if (i >= base.frameCount) cell.className += " fhr-img__cell--added";
      else if (i >= head.frameCount) cell.className += " fhr-img__cell--removed";
      cell.addEventListener("click", () => {
        setPlaying(false);
        go(i);
      });
      cells.push(cell);
      list.appendChild(cell);
    }
    strip.append(playButton, list);
    const info = make("div", "fhr-img__frameinfo");
    info.append(frameInfo, frameSummary);
    frames.append(strip, info);

    // Mark the changed frames, a slice at a time.
    const common = Math.min(base.frameCount, head.frameCount);
    let i = 0;
    let changed = frameCount - common;
    frameSummary.textContent = "Comparing frames…";
    const scan = (): void => {
      let work = 0;
      while (i < common && work < SCAN_SLICE) {
        const a = { width: base.width, height: base.height, data: base.frame(i) };
        const same = samePixels(a, { width: head.width, height: head.height, data: head.frame(i) });
        pixelsSame[i] = same;
        if (!same || base.delayMs(i) !== head.delayMs(i)) {
          cells[i]!.className += " fhr-img__cell--changed";
          changed++;
        }
        work += Math.max(base.width * base.height, head.width * head.height, 1);
        i++;
      }
      if (i < common) {
        later(scan, 0);
        return;
      }
      const counts = base.frameCount === head.frameCount ? `${frameCount} frames` : `${base.frameCount} → ${head.frameCount} frames`;
      frameSummary.textContent = changed ? `${counts}, ${changed} differ` : `${counts}, none differ`;
      describeFrame();
    };
    scan();
    go(0);
    const reduce = (globalThis as { matchMedia?: (q: string) => { matches: boolean } }).matchMedia?.(
      "(prefers-reduced-motion: reduce)",
    ).matches;
    setPlaying(!reduce);
  };

  const loadAnimations = async (): Promise<boolean> => {
    try {
      const [a, b] = await Promise.all(
        SIDES.map(async (s) => {
          const res = await fetch(urls[s]!);
          return opts.decodeAnimation!(new Uint8Array(await res.arrayBuffer()));
        }),
      );
      if (!a || !b || (a.frameCount <= 1 && b.frameCount <= 1)) return false;
      anim = { base: a, head: b };
      sizes.base = { w: a.width, h: a.height };
      sizes.head = { w: b.width, h: b.height };
      return true;
    } catch {
      return false;
    }
  };

  if (both && opts.decodeAnimation) {
    void loadAnimations().then((animated) => {
      if (!alive) return;
      build();
      if (animated) startFrames();
    });
  } else {
    build();
  }

  return () => {
    alive = false;
    for (const t of timers) clearTimeout(t);
    timers.clear();
    for (const p of ports) p.vp.destroy();
  };
}

function dims(s: Size | null): string {
  return s ? `${s.w} × ${s.h}` : "";
}

function clear(el: HTMLElement): void {
  for (const c of Array.from(el.childNodes)) (c as ChildNode).remove();
  el.textContent = "";
}
