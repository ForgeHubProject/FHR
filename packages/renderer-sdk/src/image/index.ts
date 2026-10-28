// @fhr/renderer-sdk/image — the raster image compare, bundled by each image
// format's renderer. A subpath of its own so renderers that draw no images
// never pull it in.

export { renderImageDiff, IMAGE_VIEWS } from "./compare.js";
export type { AnimatedImage, ImageDiffControl, ImageDiffOptions, ImageMark, ImageMarkKind, ImageViewId } from "./compare.js";
export { diffPixels, samePixels, overlayPixels, describeDiff, DIFF_TINT } from "./pixels.js";
export type { Pixels, PixelDiff } from "./pixels.js";
export { clampView, zoomAt, stepZoom, fitZoom, zoomLabel, rectView, createViewport, ZOOM_STEPS, MIN_ZOOM, MAX_ZOOM } from "./viewport.js";
export type { View, Size, Rect, ViewportHandle } from "./viewport.js";
