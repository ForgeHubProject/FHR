// A zoom-and-pan viewport for raster images: a fixed-height window onto a
// "stage" that holds one or more stacked layers (the image, the other side of a
// slider, a difference overlay) and is moved and scaled as one.
//
// Images open at their true size — one image pixel to one CSS pixel — centred
// when smaller than the window and anchored top-left when larger. Dragging
// pans; Ctrl/⌘ + wheel (and a trackpad pinch, which browsers report as one)
// zooms about the cursor; the corner controls step the zoom, return to 100%,
// or fit the whole image. Every viewport is independent: two of them side by
// side zoom and pan separately.

/** Where the stage sits: its scale, and its top-left corner in window pixels. */
export type View = { zoom: number; x: number; y: number };
export type Size = { w: number; h: number };
export type Rect = { x: number; y: number; w: number; h: number };

export const MIN_ZOOM = 1 / 32;
export const MAX_ZOOM = 64;

/** The zoom levels the − / + controls step through. */
export const ZOOM_STEPS = [
  1 / 32, 1 / 16, 1 / 8, 1 / 4, 1 / 3, 1 / 2, 2 / 3, 1, 1.5, 2, 3, 4, 6, 8, 12, 16, 24, 32, 48, 64,
];

export function clampZoom(z: number): number {
  return Number.isFinite(z) ? Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, z)) : 1;
}

/**
 * Keep the stage where it can be seen: along an axis where the scaled image is
 * no bigger than the window it is centred; where it is bigger, its edges may
 * not come inside the window's.
 */
export function clampView(v: View, content: Size, port: Size): View {
  const axis = (offset: number, extent: number, room: number): number =>
    extent <= room ? (room - extent) / 2 : Math.min(0, Math.max(room - extent, offset));
  return { zoom: v.zoom, x: axis(v.x, content.w * v.zoom, port.w), y: axis(v.y, content.h * v.zoom, port.h) };
}

/** Rescale so the image point under window point (px, py) stays under it. */
export function zoomAt(v: View, zoom: number, px: number, py: number): View {
  const z = clampZoom(zoom);
  const cx = (px - v.x) / v.zoom;
  const cy = (py - v.y) / v.zoom;
  return { zoom: z, x: px - cx * z, y: py - cy * z };
}

/** The next zoom step in `dir` from `z` (z itself when there is none). */
export function stepZoom(z: number, dir: 1 | -1): number {
  if (dir > 0) return ZOOM_STEPS.find((s) => s > z * 1.001) ?? MAX_ZOOM;
  return [...ZOOM_STEPS].reverse().find((s) => s < z / 1.001) ?? MIN_ZOOM;
}

/** The zoom that shows all of `content` inside `port`, less `pad` pixels. */
export function fitZoom(content: Size, port: Size, pad = 0): number {
  if (content.w <= 0 || content.h <= 0 || port.w <= pad || port.h <= pad) return 1;
  return clampZoom(Math.min((port.w - pad) / content.w, (port.h - pad) / content.h));
}

/** "100%", "33%", "6400%", "3.1%". */
export function zoomLabel(z: number): string {
  const pct = z * 100;
  return `${pct >= 10 ? Math.round(pct) : Math.round(pct * 10) / 10}%`;
}

/**
 * A view that centres `rect` (an image region) with room around it: zoomed in
 * on a small change, never past `maxZoom`, and out far enough to show a large
 * one whole.
 */
export function rectView(rect: Rect, port: Size, maxZoom = 16): View {
  const w = Math.max(rect.w, 1);
  const h = Math.max(rect.h, 1);
  const zoom = clampZoom(Math.min(maxZoom, port.w / (w * 1.6), port.h / (h * 1.6)));
  return { zoom, x: port.w / 2 - (rect.x + w / 2) * zoom, y: port.h / 2 - (rect.y + h / 2) * zoom };
}

export type ViewportHandle = {
  /** The window; place it in the layout. */
  el: HTMLElement;
  /** Where layers go, each absolutely positioned at the stage's top-left. */
  stage: HTMLElement;
  /** The image area the view clamps to. A change re-clamps, keeping the zoom. */
  setContent(size: Size): void;
  setHeight(px: number): void;
  readonly view: View;
  /** Window size in CSS pixels (0 × 0 before layout). */
  port(): Size;
  zoomTo(zoom: number): void;
  fit(): void;
  /** Frame an image region (see rectView). */
  focus(rect: Rect): void;
  /** The image point at the window's centre, and the zoom: a place another window can take up. */
  center(): { x: number; y: number; zoom: number };
  /** Put image point (x, y) at the window's centre, at `zoom`. */
  centerOn(x: number, y: number, zoom: number): void;
  /** Called after every move, zoom or resize. */
  onChange(fn: (v: View) => void): void;
  destroy(): void;
};

/** Pointer events the viewport reads; a subset, so tests can hand in plain objects. */
type PointerLike = {
  button?: number;
  clientX: number;
  clientY: number;
  pointerId?: number;
  stopPropagation?: () => void;
  preventDefault?: () => void;
};
type WheelLike = PointerLike & { deltaY: number; deltaMode?: number; ctrlKey?: boolean; metaKey?: boolean };

export function createViewport(doc: Document, height: number): ViewportHandle {
  const el = doc.createElement("div");
  el.className = "fhr-img__port";
  const stage = doc.createElement("div");
  stage.className = "fhr-img__stage";
  el.appendChild(stage);

  const controls = doc.createElement("div");
  controls.className = "fhr-img__zoom";
  const button = (text: string, title: string, act: () => void): HTMLButtonElement => {
    const b = doc.createElement("button");
    b.setAttribute("type", "button");
    b.setAttribute("title", title);
    b.setAttribute("aria-label", title);
    b.textContent = text;
    b.addEventListener("click", act);
    controls.appendChild(b);
    return b;
  };
  button("−", "Zoom out", () => zoomTo(stepZoom(view.zoom, -1)));
  const actual = button("100%", "Actual size", () => zoomTo(1));
  button("+", "Zoom in", () => zoomTo(stepZoom(view.zoom, 1)));
  button("Fit", "Fit to window", () => fit());
  // The controls are clicked, not dragged: a press on them must not start a pan.
  controls.addEventListener("pointerdown", (e) => (e as unknown as PointerLike).stopPropagation?.());
  el.appendChild(controls);

  let content: Size = { w: 0, h: 0 };
  let view: View = { zoom: 1, x: 0, y: 0 };
  const listeners: ((v: View) => void)[] = [];

  const port = (): Size => ({ w: el.clientWidth || 0, h: el.clientHeight || 0 });

  const apply = (next: View): void => {
    view = clampView(next, content, port());
    stage.style.transform = `translate(${view.x}px, ${view.y}px) scale(${view.zoom})`;
    // Read by the layers' CSS: the checkerboard stays 16 screen pixels a square
    // whatever the zoom.
    stage.style.setProperty("--fhr-check", `${16 / view.zoom}px`);
    // Past 2× a reviewer is looking at pixels: show them as squares, not blur.
    stage.setAttribute("data-pixelated", view.zoom >= 2 ? "1" : "0");
    const p = port();
    el.setAttribute("data-pan", content.w * view.zoom > p.w + 0.5 || content.h * view.zoom > p.h + 0.5 ? "1" : "0");
    actual.textContent = zoomLabel(view.zoom);
    for (const fn of listeners) fn(view);
  };

  const center = (): { x: number; y: number } => {
    const p = port();
    return { x: p.w / 2, y: p.h / 2 };
  };
  const zoomTo = (z: number): void => {
    const c = center();
    apply(zoomAt(view, z, c.x, c.y));
  };
  const fit = (): void => apply({ zoom: fitZoom(content, port(), 16), x: 0, y: 0 });

  // Drag to pan. Pointer capture keeps the drag alive outside the window.
  let drag: { x: number; y: number; vx: number; vy: number } | null = null;
  const down = (raw: Event): void => {
    const e = raw as unknown as PointerLike;
    if ((e.button ?? 0) !== 0) return;
    drag = { x: e.clientX, y: e.clientY, vx: view.x, vy: view.y };
    if (e.pointerId !== undefined) (el as Partial<HTMLElement>).setPointerCapture?.(e.pointerId);
    el.setAttribute("data-dragging", "1");
    e.preventDefault?.();
  };
  const move = (raw: Event): void => {
    if (!drag) return;
    const e = raw as unknown as PointerLike;
    apply({ zoom: view.zoom, x: drag.vx + e.clientX - drag.x, y: drag.vy + e.clientY - drag.y });
  };
  const up = (): void => {
    drag = null;
    el.setAttribute("data-dragging", "0");
  };
  const wheel = (raw: Event): void => {
    const e = raw as unknown as WheelLike;
    // Plain wheel scrolls the page, as everywhere else; Ctrl/⌘ (or a pinch) zooms.
    if (!e.ctrlKey && !e.metaKey) return;
    e.preventDefault?.();
    const r = (el as Partial<HTMLElement>).getBoundingClientRect?.() ?? { left: 0, top: 0 };
    const step = e.deltaMode === 1 ? 0.05 : 0.002; // lines vs pixels
    apply(zoomAt(view, view.zoom * Math.exp(-e.deltaY * step), e.clientX - r.left, e.clientY - r.top));
  };
  el.addEventListener("pointerdown", down);
  el.addEventListener("pointermove", move);
  el.addEventListener("pointerup", up);
  el.addEventListener("pointercancel", up);
  el.addEventListener("wheel", wheel, { passive: false });

  // Re-clamp when the window changes size (a resized browser, a wrapped pane).
  const RO = (globalThis as { ResizeObserver?: typeof ResizeObserver }).ResizeObserver;
  const observer = RO ? new RO(() => apply(view)) : undefined;
  observer?.observe(el);

  el.style.height = `${height}px`;
  apply(view);

  return {
    el,
    stage,
    setContent(size: Size): void {
      if (size.w === content.w && size.h === content.h) return;
      content = size;
      stage.style.width = `${size.w}px`;
      stage.style.height = `${size.h}px`;
      apply(view);
    },
    setHeight(px: number): void {
      el.style.height = `${px}px`;
      apply(view);
    },
    get view(): View {
      return view;
    },
    port,
    zoomTo,
    fit,
    focus(rect: Rect): void {
      apply(rectView(rect, port()));
    },
    center() {
      const p = port();
      return { x: (p.w / 2 - view.x) / view.zoom, y: (p.h / 2 - view.y) / view.zoom, zoom: view.zoom };
    },
    centerOn(x: number, y: number, zoom: number): void {
      const p = port();
      apply({ zoom: clampZoom(zoom), x: p.w / 2 - x * zoom, y: p.h / 2 - y * zoom });
    },
    onChange(fn: (v: View) => void): void {
      listeners.push(fn);
    },
    destroy(): void {
      observer?.disconnect();
      listeners.length = 0;
    },
  };
}
