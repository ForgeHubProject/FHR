// One palette for both views of this renderer: the 3D scene and the lite change
// tree. Pure data — no three.js, no DOM — so the lite bundle can import it
// without pulling the 3D chunk's dependencies in.
//
// Red/green (the previous `KIND_COLOR`) is the worst possible pair for
// deuteranopia — ~8% of men see the "added" and "removed" markers as the same
// hue, which in a review tool means missing a deletion. These are hues from
// Wong's colour-blind-safe palette (Nature Methods 8, 441 (2011)), which stay
// distinct under all three common forms of colour-vision deficiency:
//
//   added     #0072B2  blue            — new geometry, on the head model
//   modified  #E69F00  orange          — changed geometry, on the head model
//   removed   #CC79A7  reddish purple  — drawn from the base model, translucent
//   renamed   #009E73  bluish green    — same object, new name (#47)
//
// Blue/orange is the issue's headline pair and goes to the two kinds that are
// painted side by side on the *same* solid model, where hue is the only cue.
// "Removed" takes the third Wong hue because it is additionally distinguished by
// how it is drawn (translucent ghost of what was), not by colour alone.
//
// "Renamed" takes bluish green rather than a tint of an existing hue, and
// emphatically not removed's: the whole point of reporting a rename is that the
// object was *not* deleted, and painting it anywhere near the deletion colour
// would say the opposite of what the change means. The three remaining Wong hues
// are vermillion (too close to orange at small sizes), sky blue (too close to
// blue), and yellow (unreadable on a light background) — bluish green is the one
// that stays distinct from all three kinds already in use.
//
// "Reparented" (#42) REUSES modified's orange, deliberately. The overlay must
// paint a reparented node the way a reviewer thinks of it — the object
// survives, something about it changed — and reusing KIND_COLOR[kind] gets that
// with zero overlay code. A hue of its own is not available: the three unused
// Wong hues are already rejected above, and renamed's green would wrongly claim
// name-identity news. Hue reuse is safe here because the kind is never conveyed
// by hue alone — the tree chip carries the kind text, and the callout says
// "reparented under X".

/** Change-kind colours as three.js-ready 24-bit ints. */
export const KIND_COLOR: Record<string, number> = {
  added: 0x0072b2,
  modified: 0xe69f00,
  removed: 0xcc79a7,
  renamed: 0x009e73,
  reparented: 0xe69f00,
};

/** Unchanged geometry: desaturated grey, so changes are the only saturated thing. */
export const NEUTRAL = 0x8b98a5;

/** The same colours as CSS strings. */
export const KIND_CSS: Record<string, string> = {
  added: "#0072B2",
  modified: "#E69F00",
  removed: "#CC79A7",
  renamed: "#009E73",
  reparented: "#E69F00",
};

/** Low-alpha background tints for chips/badges, matching KIND_CSS hues. */
export const KIND_TINT_CSS: Record<string, string> = {
  added: "rgba(0,114,178,0.12)",
  modified: "rgba(230,159,0,0.14)",
  removed: "rgba(204,121,167,0.14)",
  renamed: "rgba(0,158,115,0.14)",
  reparented: "rgba(230,159,0,0.14)",
};

/** 0x0072b2 → "#0072b2" (for stylesheet text built from the numeric palette). */
export function hexCss(color: number): string {
  return `#${color.toString(16).padStart(6, "0")}`;
}

/**
 * Palette override for the SDK's change tree, so the lite view and the 3D view
 * name changes with the same colours.
 *
 * The SDK owns `.fhr-diff`'s stylesheet and hardcodes GitHub's red/green there;
 * this renderer restates just the colour declarations at equal specificity
 * (`.fhr-diff[data-theme] …` matches the SDK's dark-theme rules) and relies on
 * being injected *after* the SDK's <style> to win the cascade. Restating rather
 * than editing the SDK keeps the palette a property of this renderer — another
 * format's renderer may legitimately want different colours.
 *
 * "renamed" is included even though the SDK's own stylesheet has no rules for it:
 * the SDK writes the kind straight into the row's mark class, so a colour stated
 * here reaches the row, while its summary chip falls to the unknown-kind style —
 * which is the forward-compatibility path working as designed, not a gap.
 */
export function changeTreeCss(): string {
  const rules: string[] = [];
  for (const kind of ["added", "modified", "removed", "renamed", "reparented"] as const) {
    const color = KIND_CSS[kind];
    rules.push(
      `.fhr-diff .fhr-diff__mark--${kind},.fhr-diff[data-theme] .fhr-diff__mark--${kind}{color:${color}}`,
      `.fhr-diff .fhr-diff__count--${kind},.fhr-diff[data-theme] .fhr-diff__count--${kind}` +
        `{color:${color};background:${KIND_TINT_CSS[kind]}}`,
    );
  }
  return rules.join("\n");
}
