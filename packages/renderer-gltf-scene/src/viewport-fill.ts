// How tall the viewport is, given what the host container offers.
//
// The renderer mounts into a container somebody else laid out — a padded column
// on a ForgeHub review page, the `<main>` of `forge diff --web` — and neither of
// them gives it a height today. A fixed pixel height was the only thing that
// worked at all, and it is why #24 opens with the scene being "a thin strip
// under the list".
//
// There are two situations, and telling them apart is the whole job:
//
//   the container has a height of its own → fill it. The host decided how much
//                                           room this renderer gets, and a
//                                           renderer that grew past it would be
//                                           drawing outside someone else's box.
//   the container is auto-height          → there is nothing to fill: whatever
//                                           height is asked for is the height it
//                                           will have. The renderer has to name
//                                           a size, and the only honest
//                                           reference left is the window — a
//                                           share of it grows with the display
//                                           instead of freezing at the number
//                                           that looked right on one laptop.
//
// Which of the two it is cannot be decided in CSS. `height:100%` against an
// auto-height parent silently computes to `auto` (so the picture collapses to
// nothing), and a `vh` floor written for the auto case would overflow a short
// definite container by most of a window. So the caller measures — one empty
// probe element, one `clientHeight` read — and this decides.
//
// Pure: no DOM, no three.js. A string of CSS and the two numbers in it.

/**
 * Below this the viewport stops being a scene and goes back to being a strip.
 * It is a floor, not a target: on a container shorter than this the renderer
 * overflows rather than draw something nobody can orbit in — the same call the
 * fixed heights this replaces were already making, minus the ceiling.
 */
export const MIN_VIEWPORT_HEIGHT = 420;

/**
 * The share of the window the viewport takes when the container offers no
 * height of its own. Short of the whole window on purpose: this is a panel on
 * someone's review page, and a reviewer who cannot see any of the page around
 * it has lost the file list, the rest of the diff and the comment box.
 */
export const AUTO_VIEWPORT_VH = 70;

/**
 * The viewport host's sizing CSS, given the height its container offers in CSS
 * pixels — 0 for a container that offers none. Measure it, don't assume it:
 * both hosts shipping today are auto-height, and a definite one is exactly the
 * case a window-relative height would break.
 */
export function viewportFillCss(offered: number): string {
  const height = offered > 0 ? "100%" : `${AUTO_VIEWPORT_VH}vh`;
  return `width:100%;height:${height};min-height:${MIN_VIEWPORT_HEIGHT}px`;
}
