// Honest-degradation banners. Every path in this renderer that shows the
// reviewer less than the real model with the real diff has to say so, in plain
// language, right above the view — a silently degraded 3D view is worse than no
// 3D view, because it looks authoritative.
//
// Styling deliberately matches the existing "Loading 3D scene…" status text
// (13px system sans, muted grey) so a banner reads as part of the same view, not
// as a browser error. DOM-only: no three.js, and only the handful of DOM calls
// below, so a plain object stands in for `document` in tests.

import { KIND_CSS } from "./palette.js";

export type BannerList = {
  /** Container element — insert it above the viewport. */
  el: HTMLElement;
  /** Append a message. Repeats of an identical message are ignored. */
  add(message: string): void;
  /** How many distinct messages are shown. */
  count(): number;
};

const LIST_CSS = "display:flex;flex-direction:column;gap:6px;padding:8px 0 4px";
const ITEM_CSS =
  `font:13px/1.5 ui-sans-serif,system-ui,sans-serif;color:#8b949e;` +
  `padding:6px 10px;border-left:3px solid ${KIND_CSS.modified};` +
  `background:rgba(230,159,0,0.08);border-radius:0 6px 6px 0`;

/** Create an (initially empty) banner list in `doc`. */
export function createBanners(doc: Document): BannerList {
  const el = doc.createElement("div");
  el.style.cssText = LIST_CSS;
  const seen = new Set<string>();

  return {
    el,
    add(message: string): void {
      const text = message.trim();
      if (text === "" || seen.has(text)) return;
      seen.add(text);
      const item = doc.createElement("div");
      item.style.cssText = ITEM_CSS;
      item.textContent = text;
      el.appendChild(item);
    },
    count(): number {
      return seen.size;
    },
  };
}

/**
 * Aggregate per-resource loader failures into one honest sentence. Called with
 * the urls a LoadingManager reported; returns null when nothing failed.
 *
 * The common cause in production is not a broken file: it's a host CSP without
 * `blob:`/`data:` in img-src/connect-src, which makes GLB-embedded textures
 * vanish while geometry loads fine (see #44's cross-repo note).
 */
export function textureFailureMessage(urls: readonly string[]): string | null {
  if (urls.length === 0) return null;
  const n = urls.length;
  return (
    `${n} ${n === 1 ? "texture" : "textures"} embedded in this file couldn't be decoded here, ` +
    `so the model is shown untextured. Geometry and the change list are unaffected.`
  );
}
