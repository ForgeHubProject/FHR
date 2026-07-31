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

export type PresentationMode = "structural" | "overlay" | "side-by-side";

/** Toggle labels, in the order the toggle shows them (the ladder's order). */
export const MODE_ORDER: readonly PresentationMode[] = ["structural", "overlay", "side-by-side"];

export const MODE_LABEL: Record<PresentationMode, string> = {
  structural: "Structural",
  overlay: "Overlay",
  "side-by-side": "Side by side",
};

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
