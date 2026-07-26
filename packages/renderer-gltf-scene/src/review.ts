// The review path: what `n`/`p` step through, and what the viewport says about
// the change you are standing on.
//
// The SDK has a general heuristic for the first question (`reviewStops`), for
// renderers whose handler's path scheme it can't reason about. This renderer
// knows the scheme (see change-path.ts), so it uses it: a two-segment path is an
// object, full stop — including a removed node that carries no fields of its own,
// which the general heuristic cannot tell apart from a collection wrapper.
//
// Pure: no three.js, no DOM. Lives in the lite bundle, beside the change tree.

import { flattenDiff, formatChange, reviewStops, type DiffRow, type ReviewStop } from "@fhr/renderer-sdk";
import type { StructuredDiff } from "@fhr/types";
import { segmentCount } from "./change-path.js";

/** Colour factors in glTF are linear; the panel has to say so to show them right. */
const format = (row: DiffRow): ReturnType<typeof formatChange> =>
  formatChange(row, { colorSpace: "linear" });

export { format as formatGltfChange };

/**
 * The changes `n`/`p` steps through, in diff order: one stop per changed object,
 * with that object's field rows as its details. Falls back to the SDK's general
 * heuristic if a diff arrives with no object-shaped paths at all — a diff shape
 * this renderer doesn't recognise is better reviewed imperfectly than not at all.
 */
export function entityStops(diff: StructuredDiff | undefined): ReviewStop[] {
  if (!diff) return [];
  const rows = flattenDiff(diff);
  const stops: ReviewStop[] = [];
  for (let i = 0; i < rows.length; i++) {
    const row = rows[i]!;
    const segments = segmentCount(row.path);
    if (segments > 2) continue;
    // A one-segment row is a collection wrapper when it has children; when it
    // doesn't, it is a change in its own right and must stay reachable.
    if (segments === 1 && row.hasChildren) continue;
    const prefix = row.path + "/";
    const details: DiffRow[] = [];
    for (let j = i + 1; j < rows.length && rows[j]!.path.startsWith(prefix); j++) details.push(rows[j]!);
    stops.push({ row, details });
  }
  return stops.length > 0 ? stops : reviewStops(diff);
}

/** Which field a stop's headline should be about, most significant first. */
const HEADLINE_ORDER: readonly string[] = ["translation", "rotation", "scale"];
const VERB: Record<string, string> = { translation: "moved", rotation: "rotated", scale: "scaled" };

/**
 * One short line for the viewport callout: the number a reviewer wants attached
 * to the thing they are looking at. The panel carries the rest — a callout that
 * grows into a table is a callout nobody reads, and the view-management
 * literature is clear that more than one label at a time occludes the model.
 */
export function headline(stop: ReviewStop): string {
  if (stop.row.kind === "removed") return "removed";
  if (stop.row.kind === "added") return "added";

  for (const label of HEADLINE_ORDER) {
    const detail = stop.details.find((d) => d.label === label);
    if (!detail) continue;
    const magnitude = format(detail).magnitude;
    if (magnitude) return `${VERB[label]} ${magnitude}`;
  }

  if (stop.details.some((d) => format(d).kind === "color")) return "recoloured";

  const measured = stop.details.map(format).find((f) => f.magnitude !== undefined && !f.noise);
  if (measured?.magnitude) return measured.magnitude;

  const meaningful = stop.details.filter((d) => !format(d).noise);
  if (meaningful.length === 1) return `${meaningful[0]!.label} changed`;
  if (meaningful.length > 1) return `${meaningful.length} changes`;
  return "changed";
}

/** Path → headline for every stop: the map the 3D view's callout reads. */
export function headlines(stops: ReviewStop[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const stop of stops) out[stop.row.path] = headline(stop);
  return out;
}
