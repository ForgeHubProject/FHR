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

/**
 * Which field a stop's headline should be about, most significant first: a node's
 * transform, then the geometry metrics a mesh change carries (#50's slice).
 */
const HEADLINE_ORDER: readonly string[] = ["translation", "rotation", "scale", "bounds", "centroid"];
const VERB: Record<string, string> = { translation: "moved", rotation: "rotated", scale: "scaled", centroid: "moved" };
/** Vertex data: a change here is an edit with no single number to report. */
const GEOMETRY_LABEL = /^(geometry|position|normal|tangent|indices|texcoord_\d+|color_\d+|joints_\d+|weights_\d+)$/i;

/** "grew"/"shrank" for a bounds change, whose magnitude is an unsigned distance. */
function verbFor(label: string, dominantDelta: number | undefined): string {
  if (label === "bounds") return (dominantDelta ?? 0) < 0 ? "shrank" : "grew";
  return VERB[label] ?? label;
}

/**
 * One short line for the viewport callout: the number a reviewer wants attached
 * to the thing they are looking at. The panel carries the rest — a callout that
 * grows into a table is a callout nobody reads, and the view-management
 * literature is clear that more than one label at a time occludes the model.
 */
export function headline(stop: ReviewStop): string {
  if (stop.row.kind === "removed") return "removed";
  if (stop.row.kind === "added") return "added";
  // A rename's own news is the pair of names. The evidence the handler matched on
  // rides along in `after` and stays in the panel: it is what a reviewer checks
  // *after* being told, not the thing the callout exists to say.
  if (stop.row.kind === "renamed") return `renamed ${renameFrom(stop)} → ${stop.row.label}`;
  // A reparent's news is where the node went. Read the new parent off the
  // `parent` DETAILS row, whose `after` is the bare parent key — never off the
  // node-level `after`, which carries the pairing evidence note (#42).
  if (stop.row.kind === "reparented") {
    const parentDetail = stop.details.find((d) => d.label === "parent");
    return typeof parentDetail?.after === "string" && parentDetail.after !== ""
      ? `reparented under ${parentDetail.after}`
      : "reparented";
  }

  for (const label of HEADLINE_ORDER) {
    const detail = stop.details.find((d) => d.label === label);
    if (!detail) continue;
    const measured = format(detail);
    if (measured.magnitude) return `${verbFor(label, measured.dominantDelta)} ${measured.magnitude}`;
  }

  if (stop.details.some((d) => format(d).kind === "color")) return "recoloured";

  const anyMagnitude = stop.details.map(format).find((f) => f.magnitude !== undefined && !f.noise);
  if (anyMagnitude?.magnitude) return anyMagnitude.magnitude;

  // Vertex data changed but nothing here measures it (that is #46's heatmap).
  if (stop.details.some((d) => GEOMETRY_LABEL.test(d.label))) return "geometry edited";

  // Only rows that carry a value are changes a reviewer would count: the group
  // headers between an object and its fields ("primitive[0]", "geometry") are
  // structure, and counting them turns one geometry edit into "5 changes".
  const counted = stop.details.filter((d) => carriesValue(d) && !format(d).noise);
  if (counted.length === 1) return `${counted[0]!.label} changed`;
  if (counted.length > 1) return `${counted.length} changes`;
  return "changed";
}

/** A rename's previous name; "?" only if a handler emitted one without a `before`. */
function renameFrom(stop: ReviewStop): string {
  return typeof stop.row.before === "string" && stop.row.before !== "" ? stop.row.before : "?";
}

/** A row with something in it, as opposed to a header above other rows. */
function carriesValue(row: DiffRow): boolean {
  return row.before !== undefined || row.after !== undefined;
}

/** Path → headline for every stop: the map the 3D view's callout reads. */
export function headlines(stops: ReviewStop[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const stop of stops) out[stop.row.path] = headline(stop);
  return out;
}
