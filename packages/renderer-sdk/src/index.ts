export { defineRenderer } from "./define.js";
export type { RenderFn, RenderCleanup, UpdateFn, DefineRendererOptions } from "./define.js";

export { flattenDiff, diffSummary, formatValue, reviewStops, stepIndex } from "./diff.js";
export type { DiffRow, DiffSummary, ReviewStop } from "./diff.js";

export { formatChange, parseNumeric, formatLength, trimNumber } from "./format.js";
export type { FormattedChange, FormatOptions, ValueClass, Swatch, Numeric } from "./format.js";

export { renderDiffTree, stepIntent } from "./dom.js";
export type { DiffTreeOptions, DiffTreeHandle } from "./dom.js";
