// The deviation heatmap's colour ramp: viridis, hand-coded.
//
// Sequential and perceptually uniform. Both words are load-bearing:
//
//   *sequential*, because deviation is an unsigned distance with a meaningful
//   zero and no meaningful midpoint. A diverging map would invent one, and a
//   reviewer would read "the middle of the ramp" as a boundary that isn't there.
//   (#46 sketched a diverging map back when the plan was signed deviation along
//   the surface normal; distance-to-surface has no sign to diverge about.)
//
//   *perceptually uniform*, because the whole picture is "which parts moved
//   more". Rainbow ramps introduce banding at the hue transitions that reads as
//   structure in the data — the measured error is up to 7.5% of the data range —
//   and viridis is additionally monotonic in lightness, so it survives greyscale
//   printing and every form of colour-vision deficiency.
//
// Ten anchors, linearly interpolated, rather than a 256-entry table or a
// dependency: viridis is smooth enough that ten stops are indistinguishable from
// the full map at the widths a legend and a shaded surface use, and this way the
// whole ramp is ~200 bytes of the 3D chunk.
//
// Pure data and arithmetic — no three.js, no DOM.

/** viridis sampled at ten even steps, sRGB hex — the ramp's anchors. */
export const RAMP_STOPS: readonly string[] = [
  "#440154",
  "#482878",
  "#3e4a89",
  "#31688e",
  "#26828e",
  "#1f9e89",
  "#35b779",
  "#6dcd59",
  "#b4de2c",
  "#fde725",
];

/** Anchors as sRGB channels in 0..1, unpacked once. */
const ANCHORS: readonly (readonly [number, number, number])[] = RAMP_STOPS.map((hex) => {
  const n = parseInt(hex.slice(1), 16);
  return [((n >> 16) & 0xff) / 255, ((n >> 8) & 0xff) / 255, (n & 0xff) / 255] as const;
});

export type Rgb = { r: number; g: number; b: number };

/**
 * The ramp colour at `t`, in sRGB (0..1 per channel). `t` outside 0..1 clamps —
 * a saturated value is a real reading ("at least this far"), not an error.
 */
export function rampSrgb(t: number): Rgb {
  const clamped = t <= 0 || Number.isNaN(t) ? 0 : t >= 1 ? 1 : t;
  const scaled = clamped * (ANCHORS.length - 1);
  const i = Math.min(Math.floor(scaled), ANCHORS.length - 2);
  const f = scaled - i;
  const a = ANCHORS[i]!;
  const b = ANCHORS[i + 1]!;
  return {
    r: a[0] + (b[0] - a[0]) * f,
    g: a[1] + (b[1] - a[1]) * f,
    b: a[2] + (b[2] - a[2]) * f,
  };
}

/** sRGB transfer function, inverted. */
function toLinear(c: number): number {
  return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
}

/**
 * The ramp colour at `t`, in linear-sRGB.
 *
 * This is the one that goes into a vertex-colour attribute. three.js has treated
 * buffer colour data as being in the *linear* working space since r152, so
 * writing the sRGB values straight in would wash the whole ramp out — a mistake
 * that looks like a lighting problem rather than a colour-space one.
 */
export function rampLinear(t: number): Rgb {
  const c = rampSrgb(t);
  return { r: toLinear(c.r), g: toLinear(c.g), b: toLinear(c.b) };
}

/** `#rrggbb` for the ramp at `t` — what the legend's labels and swatches use. */
export function rampCss(t: number): string {
  const c = rampSrgb(t);
  const byte = (v: number): string =>
    Math.round(Math.min(1, Math.max(0, v)) * 255)
      .toString(16)
      .padStart(2, "0");
  return `#${byte(c.r)}${byte(c.g)}${byte(c.b)}`;
}

/** The ramp as a CSS gradient, left (zero) to right (the top of the range). */
export function rampGradientCss(): string {
  const stops = RAMP_STOPS.map((hex, i) => `${hex} ${((i / (RAMP_STOPS.length - 1)) * 100).toFixed(1)}%`);
  return `linear-gradient(90deg, ${stops.join(", ")})`;
}

/**
 * Relative luminance (Rec. 709) of the ramp at `t`. Exported because "viridis is
 * monotonic in lightness" is the property the ramp was chosen FOR, and a test
 * that can't measure it can't defend the choice against a well-meaning edit.
 */
export function rampLuminance(t: number): number {
  const c = rampLinear(t);
  return 0.2126 * c.r + 0.7152 * c.g + 0.0722 * c.b;
}
