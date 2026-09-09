// Which side regions fit, at this width.
//
// One layout has to serve both a full-window local view (forge diff --web) and a
// narrow column on a host's review page, or the two diverge and one of them ends
// up unmaintained. That means the regions have to fold rather than the layout
// having to fork.
//
// The tree folds first. The change queue is the region a reviewer cannot work
// without — it is the worklist and the position in it — while the tree is
// context. Two thresholds, with the tree's higher, is the whole mechanism.
//
// A collapse the viewer asked for is remembered separately from one the width
// forced: widening the container brings back exactly what they had open, and a
// region they closed by hand stays closed.
//
// Pure: no three.js, no DOM.

/** Region widths, in CSS pixels. The viewport takes whatever is left. */
export const TREE_WIDTH = 208;
export const QUEUE_WIDTH = 268;
/** Below this the centre stops being a viewport and starts being a stripe. */
export const MIN_VIEWPORT_WIDTH = 300;

/** Total width at which the queue can still be shown beside the viewport. */
export const QUEUE_MIN_WIDTH = QUEUE_WIDTH + MIN_VIEWPORT_WIDTH;
/** Total width at which the tree can be shown too — necessarily the larger. */
export const TREE_MIN_WIDTH = TREE_WIDTH + QUEUE_MIN_WIDTH;

/** What the viewer asked for; both start open. */
export type RegionPreference = { tree: boolean; queue: boolean };

/** What is actually on screen. */
export type ChromeLayout = { tree: boolean; queue: boolean };

/**
 * Resolve the preference against the available width. A width that cannot hold a
 * region hides it whatever the preference says — the preference is restored, not
 * lost, the moment the width comes back.
 *
 * A width of 0 (a container measured before layout) is treated as "unknown" and
 * leaves the preference alone, so the first frame does not collapse everything
 * and then visibly unfold.
 */
export function regionLayout(width: number, wanted: RegionPreference): ChromeLayout {
  if (!(width > 0)) return { tree: wanted.tree, queue: wanted.queue };
  return {
    tree: wanted.tree && width >= TREE_MIN_WIDTH,
    queue: wanted.queue && width >= QUEUE_MIN_WIDTH,
  };
}

/** True when the width, not the viewer, is what is hiding `region`. */
export function autoCollapsed(
  region: keyof ChromeLayout,
  width: number,
  wanted: RegionPreference,
): boolean {
  return wanted[region] && !regionLayout(width, wanted)[region];
}
