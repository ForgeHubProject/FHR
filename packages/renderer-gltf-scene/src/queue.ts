// The review queue: the ordered worklist the right region is built around.
//
// The divergence from the authoring tools whose outliner/viewport/properties
// arrangement this chrome borrows. Their right-hand region is a properties
// editor — passive detail about whatever is selected. A *review* tool's job is
// sequencing: get me through N changes, let me judge each one, tell me when I am
// done. So the region is an ordered list with a position in it, and selection
// follows the queue rather than the other way round.
//
// The data is prepared on the lite side (live-view.ts), where the SDK's
// formatting already lives, and handed to the 3D chunk as plain values. That is
// deliberate: the chunk gets a worklist to render, not a diff to re-interpret,
// and no formatting rule ends up implemented twice.
//
// Pure: no three.js, no DOM, and no SDK *values* — only its types.

import type { DiffRow, FormattedChange, ReviewStop } from "@fhr/renderer-sdk";

/** One formatted field row of the per-change panel. */
export type QueueDetail = {
  label: string;
  before: string;
  after: string;
  /** The delta cell, when the change has a number worth judging it by. */
  delta?: string;
  /** An index churn row: shown, but not what the reviewer is here for. */
  noise?: boolean;
};

/** One stop on the review path. */
export type QueueEntry = {
  /** The selection key: a `DiffChange.path` exactly as the handler wrote it. */
  path: string;
  label: string;
  kind: string;
  /** The one-line summary the viewport callout also shows (review.ts). */
  headline: string;
  details: QueueDetail[];
};

/**
 * Format the review stops into queue entries.
 *
 * `format` and `headlineOf` are injected rather than imported so this module
 * stays free of the SDK's code: it is bundled into both the lite bundle and the
 * 3D chunk, and a value import here would be paid for twice.
 */
export function buildQueue(
  stops: readonly ReviewStop[],
  format: (row: DiffRow) => FormattedChange,
  headlineOf: (stop: ReviewStop) => string,
): QueueEntry[] {
  return stops.map((stop) => ({
    path: stop.row.path,
    label: stop.row.label,
    kind: stop.row.kind,
    headline: headlineOf(stop),
    details: stop.details.filter(carriesValue).map((row) => {
      const shown = format(row);
      const detail: QueueDetail = { label: row.label, before: shown.before, after: shown.after };
      if (shown.deltaCell !== undefined) detail.delta = shown.deltaCell;
      if (shown.noise) detail.noise = true;
      return detail;
    }),
  }));
}

/** A row with something in it, as opposed to a header above other rows. */
function carriesValue(row: DiffRow): boolean {
  return row.before !== undefined || row.after !== undefined;
}

export type QueuePosition = {
  /** Zero-based position of the selection in the queue, or -1 for none. */
  index: number;
  total: number;
  /** The readout: "N changes" until the reviewer is standing somewhere. */
  label: string;
};

/**
 * Where the reviewer is in the queue. The unselected state reports the *size* of
 * the job rather than a fake "0 / N": nothing has been judged yet, and a position
 * of zero would say otherwise.
 */
export function queuePosition(entries: readonly QueueEntry[], selected: string | null): QueuePosition {
  const total = entries.length;
  const index = selected === null ? -1 : entries.findIndex((e) => e.path === selected);
  if (index < 0) {
    return { index: -1, total, label: `${total} ${total === 1 ? "change" : "changes"}` };
  }
  return { index, total, label: `${index + 1} / ${total}` };
}
