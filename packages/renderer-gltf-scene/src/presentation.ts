// Which presentation the 3D view is showing, and which ones it may offer.
//
// The ladder (SPEC-RENDERING §2e) answers two different reviewer questions:
//
//   structural     what changed          the painted diff — the change grammar
//   overlay        where, and how far    structural + the whole previous version
//                                        as a translucent underlay
//   side-by-side   see for yourself      both versions, two scissored viewports
//
// Blink is on the same ladder but is deliberately *not* a position in this
// toggle: it is held (Space), not selected, and it works inside every mode where
// both versions are resident. A mode you have to leave to blink from would cost
// the reviewer their place, which is the thing this chrome exists to prevent.
//
// Wipe is on the spec's ladder and is *not* implemented for 3D — a wipe line
// lives in screen space and stops meaning anything the moment the camera orbits.
// See the UI decision on #56.
//
// Pure: no three.js, no DOM.

import type { HandlerCapabilities } from "@fhr/types";
import type { PaneSide } from "./split.js";

export type PresentationMode = "structural" | "overlay" | "side-by-side";

/** Toggle labels, in the order the toggle shows them (the ladder's order). */
export const MODE_ORDER: readonly PresentationMode[] = ["structural", "overlay", "side-by-side"];

export const MODE_LABEL: Record<PresentationMode, string> = {
  structural: "Structural",
  overlay: "Overlay",
  "side-by-side": "Side by side",
};

/**
 * The one mode the deviation heatmap (#46) is a sub-view of.
 *
 * Overlay is already the "where, and how far" rung — the previous version
 * underneath the current one — and the heatmap is the same question answered in
 * numbers, so it belongs *inside* that rung rather than as a fourth position on
 * the ladder. A mode of its own would also have to answer what the ghost and the
 * motion vectors do while it is on, and there is no good answer: they are the
 * qualitative form of what it is already showing quantitatively.
 */
export const HEATMAP_MODE: PresentationMode = "overlay";
export const HEATMAP_LABEL = "Deviation";
export const HEATMAP_TITLE = "Colour changed geometry by how far its surface moved";

export const MODE_TITLE: Record<PresentationMode, string> = {
  structural: "The diff painted on the current version",
  overlay: "The previous version underneath, to see how far things moved",
  "side-by-side": "Both versions, cameras locked together",
};

/**
 * The presentation to open on, from what the handler declared about itself.
 *
 * `semanticCompare: false` is a handler saying its diff has no durable entity
 * identity to compare against — regenerated topology, a re-tessellated export.
 * A structural view of that reports "everything changed", which is true and
 * useless, so the view opens on side-by-side and lets the reviewer look.
 *
 * Absent capabilities mean the host didn't plumb the declaration through, which
 * is not an error and must not degrade the common case: structural.
 */
export function defaultMode(capabilities?: HandlerCapabilities): PresentationMode {
  return capabilities?.semanticCompare === false ? "side-by-side" : "structural";
}

/**
 * The modes this mount can honestly offer. Overlay and side-by-side both need
 * the previous version in hand; when it was missing, refused or over the size
 * cap (limits.ts), offering them would be a toggle that shows nothing new.
 */
export function availableModes(input: { bothVersionsResident: boolean }): PresentationMode[] {
  return input.bothVersionsResident ? [...MODE_ORDER] : ["structural"];
}

/**
 * The layers one drawing pass shows. Every mode is the same resident scene with
 * a different answer here; scene-3d.ts only writes these onto the groups.
 */
export type VersionLayers = {
  /** The current version's geometry. */
  head: boolean;
  /** The previous version's geometry, with the materials its own file carries. */
  baseSolid: boolean;
  /** The previous version as a translucent underlay. */
  baseGhost: boolean;
  /** Ghosts of removed geometry, drawn from the previous version. */
  removed: boolean;
  /** Old poses of moved geometry, plus their motion vectors. */
  moved: boolean;
  /**
   * The diff's tint and desaturation, which live on the current version's OWN
   * materials rather than in a group (model-overlay.ts). It has to be switched
   * with the rest of the grammar or it survives everything that hides a group.
   */
  paint: boolean;
};

/**
 * Which layers a pass showing `side` may draw.
 *
 * `grammar` is what separates a single-viewport mode from a side-by-side pane.
 * With it, the current version carries the whole diff grammar — the tint, the
 * ghosts of what was removed, the old poses of what moved, and (in overlay) the
 * entire previous version underneath. Without it, a pane shows one version and
 * only that version, which is the promise its label makes.
 *
 * The paint is in that list deliberately. It is the one part of the grammar that
 * is not a group, so it is the one part a pane cannot opt out of by hiding
 * something — and it is also the loudest: a "Current version" pane that kept it
 * would show the diff's orange instead of the colour the new file actually has,
 * and every unchanged part greyed against a full-colour previous version. Two
 * panes like that are not comparable, which is the whole of what side-by-side is
 * for.
 */
export function versionLayers(input: {
  side: PaneSide;
  grammar: boolean;
  mode: PresentationMode;
  /**
   * "Show changes" (FHR#87). Off takes the change marks away — the tint and the
   * ghosts of what was removed and where things moved — and leaves the model in
   * its own materials. It does not take overlay's underlay: that is the mode
   * itself, the previous version to read the current one against, not a mark
   * on the current one. Defaults to on.
   */
  changes?: boolean;
}): VersionLayers {
  const head = input.side === "head";
  const { grammar } = input;
  const marks = grammar && input.changes !== false;
  return {
    head,
    baseSolid: !head,
    // The underlay is overlay's alone: it is what "how far did it move" is read
    // against, and in the other modes it is just a second translucent copy.
    baseGhost: head && grammar && input.mode === "overlay",
    removed: head && marks,
    moved: head && marks,
    paint: marks,
  };
}

// ── "Show changes" (FHR#87) ─────────────────────────────────────────────────────
//
// A layer, not a presentation: it works inside every mode, the way blink does,
// rather than being a position on the ladder a reviewer has to leave their place
// for. Off shows the model as it is — its own materials and textures — with the
// change list still live: selecting a change still frames it, isolates it and
// calls it out, because isolation hides the rest instead of recolouring it.

export const SHOW_CHANGES_LABEL = "Show changes";
export const SHOW_CHANGES_TITLE = "Paint the diff on the model, or show the model in its own materials";

/**
 * Whether changes are shown when nothing has been chosen yet. A diff with only
 * one version — a file added or deleted, or the plain file view — paints every
 * part the same colour, which says nothing the change list doesn't and hides
 * what the model looks like; so it opens on the model. With both versions the
 * paint is the point, so it opens on the diff.
 */
export function defaultShowChanges(input: { oneSided: boolean }): boolean {
  return !input.oneSided;
}

/** The slice of Web Storage the preference uses — sessionStorage in a browser. */
export type PreferenceStore = Pick<Storage, "getItem" | "setItem">;

const PREFERENCE_KEY = { oneSided: "fhr3d.showChanges.oneSided", bothSides: "fhr3d.showChanges.bothSides" };

/**
 * The reviewer's choice, remembered for the session — separately for one-sided
 * and two-sided diffs, since turning the paint on for one added file says
 * nothing about wanting it off for the next modified one. Storage can be absent
 * or refuse (a sandboxed iframe, a private window), so every access is guarded
 * and a failure is just "nothing remembered".
 */
export function changesPreference(store: PreferenceStore | null): {
  get(oneSided: boolean): boolean | null;
  set(oneSided: boolean, on: boolean): void;
} {
  const key = (oneSided: boolean): string => (oneSided ? PREFERENCE_KEY.oneSided : PREFERENCE_KEY.bothSides);
  return {
    get(oneSided) {
      try {
        const value = store?.getItem(key(oneSided));
        return value === "1" ? true : value === "0" ? false : null;
      } catch {
        return null;
      }
    },
    set(oneSided, on) {
      try {
        store?.setItem(key(oneSided), on ? "1" : "0");
      } catch {
        /* nothing remembered */
      }
    },
  };
}

export type ModeState = {
  readonly mode: PresentationMode;
  readonly available: readonly PresentationMode[];
  /** Switch modes. Returns false for an unavailable mode or a no-op. */
  set(mode: PresentationMode): boolean;
  /** Called after every accepted change, with the new mode. */
  onChange(listener: (mode: PresentationMode) => void): void;
};

/**
 * The toggle's state. `initial` is a *preference*: a default of side-by-side on
 * a mount whose previous version never loaded falls back to the first available
 * mode rather than leaving the toggle pointing at nothing.
 */
export function createModeState(input: {
  initial: PresentationMode;
  available: readonly PresentationMode[];
}): ModeState {
  const available = input.available.length > 0 ? [...input.available] : (["structural"] as PresentationMode[]);
  let mode = available.includes(input.initial) ? input.initial : available[0]!;
  const listeners: ((mode: PresentationMode) => void)[] = [];

  return {
    get mode(): PresentationMode {
      return mode;
    },
    get available(): readonly PresentationMode[] {
      return available;
    },
    set(next: PresentationMode): boolean {
      if (next === mode || !available.includes(next)) return false;
      mode = next;
      for (const listener of listeners) listener(mode);
      return true;
    },
    onChange(listener: (mode: PresentationMode) => void): void {
      listeners.push(listener);
    },
  };
}
