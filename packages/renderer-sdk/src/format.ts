// Presentation rules for a change's before/after values.
//
// The panel owns the numbers, and a number a reviewer can't judge is worse than
// no number. Three rules, from the prior-art survey in FHR #40/#45:
//
//   * before → after is not enough on its own. The quantity a reviewer judges is
//     the *delta and its magnitude* — "Δ(0, 0.05, 0) = 50 mm" answers "did it
//     move enough to matter?", while "[0 1 0] → [0 1.05 0]" makes them do
//     arithmetic in their head.
//   * a colour is a swatch, never a float tuple. Nobody reads
//     "[0.8 0.1 0.1 1] → [0.1 0.1 0.8 1]" as "red became blue".
//   * suppress noise: full float precision (2 decimals of trailing zeros from a
//     formatter), and array-index churn ("mesh[3] → mesh[5]"), which a re-export
//     produces by the dozen.
//
// This module *parses display strings back into numbers*. It deliberately does
// no geometry: handlers already emit values in whatever space the reviewer
// thinks in (the glTF handler converts to Blender space and euler degrees before
// formatting), so recomputing anything here would mean re-deriving a coordinate
// convention this layer does not own. Parse, subtract, format.
//
// Pure — no DOM, no format-specific knowledge beyond label vocabulary that is
// common to every 3D-ish diff. Renderers that need different rules pass their
// own formatter to renderDiffTree.

import { formatValue } from "./diff.js";

/** What kind of value a change carries, once its text has been parsed. */
export type ValueClass = "text" | "number" | "vector" | "angle" | "color" | "index";

/** A colour, ready to draw as a chip. */
export type Swatch = {
  /** CSS colour for the chip's background. */
  css: string;
  /** Short human-readable form, e.g. "#CC1A1A" or "#CC1A1A 50%". */
  label: string;
};

export type FormattedChange = {
  kind: ValueClass;
  /** Display text for the previous value ("—" when there wasn't one). */
  before: string;
  /** Display text for the new value ("—" when there isn't one). */
  after: string;
  beforeSwatch?: Swatch;
  afterSwatch?: Swatch;
  /** Per-component difference, e.g. "Δ(0, 0.05, 0)". */
  deltaText?: string;
  /** The one number a reviewer judges, e.g. "50 mm", "45°", "×1.2", "+0.05". */
  magnitude?: string;
  /** deltaText plus magnitude when the magnitude adds something: the delta cell. */
  deltaCell?: string;
  /**
   * Array-index churn ("mesh[3] → mesh[5]"). True means "de-emphasise this":
   * it is a real difference in the file, but on its own it usually says only
   * that an exporter renumbered an array.
   */
  noise: boolean;
};

export type FormatOptions = {
  /**
   * Colour space the numbers are in. glTF factors are linear, so a linear 0.5
   * must be shown as sRGB ~0.74 or the chip won't match the rendered model.
   * Default "srgb": no conversion, because most formats' colours already are.
   */
  colorSpace?: "linear" | "srgb";
};

/** Labels whose vectors are positions in metres (glTF's unit convention). */
const LENGTH_LABEL = /translat|position|location|offset|origin|pivot|min|max/i;
/** Labels whose vectors are multipliers, judged as a ratio not a distance. */
const SCALE_LABEL = /scale/i;
/** Labels whose vectors are colours. */
const COLOR_LABEL = /colou?r|emissive|albedo|diffuse|specular|tint/i;
/** "mesh[3]", "material[12]" — a reference by array index. */
const INDEXED = /^([A-Za-z_][\w.\-]*)\[(\d+)\]$/;

const ABSENT = "—";

/** Numbers parsed out of a formatted value, plus whether they carried degrees. */
export type Numeric = { values: number[]; degrees: boolean };

/**
 * Parse a formatted value back to numbers: "[1.00 2.00 -3.00]", "(0.00° 45.00°
 * 0.00°)", "[1, 2, 3]", "0.50". Returns null unless every token is numeric —
 * a value this can't parse is text, and text is shown as text.
 */
export function parseNumeric(text: string): Numeric | null {
  const inner = text.trim().replace(/^[[({]/, "").replace(/[\])}]$/, "").trim();
  if (inner === "") return null;
  const tokens = inner.split(/[\s,]+/);
  const values: number[] = [];
  let degrees = false;
  for (const token of tokens) {
    const bare = token.endsWith("°") ? token.slice(0, -1) : token;
    if (token.endsWith("°")) degrees = true;
    if (!/^[+-]?(\d+\.?\d*|\.\d+)(e[+-]?\d+)?$/i.test(bare)) return null;
    values.push(Number(bare));
  }
  return { values, degrees };
}

/** A number at display precision: at most `dp` decimals, no trailing zeros. */
export function trimNumber(value: number, dp = 3): string {
  if (!Number.isFinite(value)) return String(value);
  const rounded = Number(value.toFixed(dp));
  return String(rounded === 0 ? 0 : rounded);
}

/**
 * A distance in glTF units (metres by convention) as the unit a reviewer thinks
 * in: sub-metre changes read as millimetres, because "50 mm" is a judgement and
 * "0.05" is a lookup.
 */
export function formatLength(meters: number): string {
  const abs = Math.abs(meters);
  if (abs === 0) return "0";
  if (abs < 1) return `${trimNumber(meters * 1000, 1)} mm`;
  return `${trimNumber(meters, 3)} m`;
}

/** glTF colour factors are linear; screens are sRGB. (KHR: the same transfer
 *  function the renderer applies, so a chip matches the model.) */
function linearToSrgb(c: number): number {
  const x = Math.min(Math.max(c, 0), 1);
  return x <= 0.0031308 ? x * 12.92 : 1.055 * Math.pow(x, 1 / 2.4) - 0.055;
}

const byte = (c: number): number => Math.round(Math.min(Math.max(c, 0), 1) * 255);
const hex2 = (n: number): string => n.toString(16).padStart(2, "0").toUpperCase();

function swatch(values: number[], opts: FormatOptions): Swatch {
  const convert = opts.colorSpace === "linear" ? linearToSrgb : (c: number): number => c;
  const [r, g, b] = [byte(convert(values[0] ?? 0)), byte(convert(values[1] ?? 0)), byte(convert(values[2] ?? 0))];
  const alpha = values.length > 3 ? Math.min(Math.max(values[3] ?? 1, 0), 1) : 1;
  const css = alpha < 1 ? `rgba(${r}, ${g}, ${b}, ${trimNumber(alpha, 2)})` : `rgb(${r}, ${g}, ${b})`;
  const label = `#${hex2(r)}${hex2(g)}${hex2(b)}` + (alpha < 1 ? ` ${trimNumber(alpha * 100, 0)}%` : "");
  return { css, label };
}

/** Display text for a parsed value: a tuple in parens, a scalar bare. */
function numericText(n: Numeric): string {
  const unit = n.degrees ? "°" : "";
  if (n.values.length === 1) return `${trimNumber(n.values[0]!)}${unit}`;
  return `(${n.values.map((v) => trimNumber(v) + unit).join(", ")})`;
}

function isColorLike(label: string, n: Numeric | null): boolean {
  if (!n || n.degrees) return false;
  if (!COLOR_LABEL.test(label)) return false;
  if (n.values.length < 3 || n.values.length > 4) return false;
  return n.values.every((v) => v >= 0 && v <= 1.0001);
}

/** Both sides are the same kind of array reference, at different indices. */
function isIndexChurn(before: string, after: string): boolean {
  const a = INDEXED.exec(before);
  const b = INDEXED.exec(after);
  return a !== null && b !== null && a[1] === b[1] && a[2] !== b[2];
}

/** Uniform ratio between two tuples, or null when they don't share one. */
function uniformRatio(before: number[], after: number[]): number | null {
  let ratio: number | null = null;
  for (let i = 0; i < before.length; i++) {
    const b = before[i]!;
    const a = after[i]!;
    if (b === 0) return null;
    const r = a / b;
    if (ratio === null) ratio = r;
    else if (Math.abs(r - ratio) > 1e-3) return null;
  }
  return ratio;
}

/**
 * Turn one change's raw before/after into display text, a delta with a
 * magnitude, colour swatches and a noise flag. See the module header for why
 * each of those exists.
 */
export function formatChange(
  row: { label?: string; before?: unknown; after?: unknown },
  opts: FormatOptions = {},
): FormattedChange {
  const label = row.label ?? "";
  const hasBefore = row.before !== undefined && row.before !== null;
  const hasAfter = row.after !== undefined && row.after !== null;
  const beforeText = hasBefore ? formatValue(row.before) : ABSENT;
  const afterText = hasAfter ? formatValue(row.after) : ABSENT;

  const beforeNum = hasBefore ? parseNumeric(beforeText) : null;
  const afterNum = hasAfter ? parseNumeric(afterText) : null;

  // Colours: chips on both sides, no arithmetic. A one-sided colour (an added or
  // removed material) still gets its chip.
  const beforeColor = isColorLike(label, beforeNum);
  const afterColor = isColorLike(label, afterNum);
  if ((beforeColor && (afterColor || !hasAfter)) || (afterColor && !hasBefore)) {
    return {
      kind: "color",
      before: beforeColor ? swatch(beforeNum!.values, opts).label : beforeText,
      after: afterColor ? swatch(afterNum!.values, opts).label : afterText,
      ...(beforeColor ? { beforeSwatch: swatch(beforeNum!.values, opts) } : {}),
      ...(afterColor ? { afterSwatch: swatch(afterNum!.values, opts) } : {}),
      noise: false,
    };
  }

  // Array-index churn: shown, but marked as the low-signal thing it usually is.
  if (hasBefore && hasAfter && isIndexChurn(beforeText, afterText)) {
    return { kind: "index", before: beforeText, after: afterText, noise: true };
  }

  // One side missing, or unparseable text: display only, nothing to subtract.
  if (!beforeNum || !afterNum || beforeNum.values.length !== afterNum.values.length) {
    const one = beforeNum ?? afterNum;
    return {
      kind: one ? (one.values.length > 1 ? "vector" : "number") : "text",
      before: beforeNum ? numericText(beforeNum) : beforeText,
      after: afterNum ? numericText(afterNum) : afterText,
      noise: false,
    };
  }

  const degrees = beforeNum.degrees || afterNum.degrees;
  const deltas = afterNum.values.map((v, i) => v - beforeNum.values[i]!);
  const unit = degrees ? "°" : "";
  const scalar = deltas.length === 1;
  const deltaText = scalar
    ? `Δ ${signed(deltas[0]!)}${unit}`
    : `Δ(${deltas.map((d) => trimNumber(d) + unit).join(", ")})`;

  const magnitude = magnitudeOf(label, deltas, beforeNum.values, afterNum.values, degrees);
  const kind: ValueClass = degrees ? "angle" : scalar ? "number" : "vector";
  return {
    kind,
    before: numericText(beforeNum),
    after: numericText(afterNum),
    deltaText,
    ...(magnitude ? { magnitude } : {}),
    deltaCell: deltaCell(deltaText, magnitude, deltas, unit),
    noise: false,
  };
}

function signed(value: number): string {
  const text = trimNumber(value);
  return value > 0 ? `+${text}` : text;
}

/**
 * The magnitude a reviewer judges the change by: a distance for a translation,
 * a ratio for a scale, the largest euler component for a rotation.
 */
function magnitudeOf(
  label: string,
  deltas: number[],
  before: number[],
  after: number[],
  degrees: boolean,
): string | undefined {
  const largest = deltas.reduce((m, d) => (Math.abs(d) > Math.abs(m) ? d : m), 0);
  if (degrees) return largest === 0 ? undefined : `${trimNumber(Math.abs(largest), 2)}°`;
  if (LENGTH_LABEL.test(label)) {
    const distance = Math.sqrt(deltas.reduce((sum, d) => sum + d * d, 0));
    return distance === 0 ? undefined : formatLength(distance);
  }
  if (SCALE_LABEL.test(label)) {
    const ratio = uniformRatio(before, after);
    if (ratio !== null && ratio > 0) return `×${trimNumber(ratio, 3)}`;
  }
  return largest === 0 ? undefined : signed(largest);
}

/**
 * "Δ(0, 0.05, 0) = 50 mm", or just the delta when the magnitude would only
 * repeat it — a rotation's "Δ(0°, 45°, 0°) = 45°" says nothing twice.
 */
function deltaCell(deltaText: string, magnitude: string | undefined, deltas: number[], unit: string): string {
  if (!magnitude) return deltaText;
  const nonZero = deltas.filter((d) => d !== 0);
  if (nonZero.length === 1) {
    const only = nonZero[0]!;
    // The magnitude repeats the one component that moved, in the same unit.
    if (magnitude === `${trimNumber(Math.abs(only), 2)}${unit}`) return deltaText;
    if (magnitude === signed(only)) return deltaText;
  }
  return `${deltaText} = ${magnitude}`;
}
