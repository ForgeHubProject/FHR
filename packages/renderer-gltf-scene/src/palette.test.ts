import { describe, it, expect } from "vitest";
import { changeTreeCss, hexCss, KIND_COLOR, KIND_CSS, NEUTRAL } from "./palette.js";
import { KIND_COLOR as SCENE_KIND_COLOR, NEUTRAL as SCENE_NEUTRAL } from "./scene-graph.js";

describe("palette", () => {
  it("uses Wong's colour-blind-safe hues, not red/green", () => {
    expect(KIND_COLOR["added"]).toBe(0x0072b2); // blue
    expect(KIND_COLOR["modified"]).toBe(0xe69f00); // orange
    expect(KIND_COLOR["removed"]).toBe(0xcc79a7); // reddish purple
    expect(KIND_COLOR["renamed"]).toBe(0x009e73); // bluish green
    // The pair that made a deletion look like an addition is gone.
    expect(Object.values(KIND_COLOR)).not.toContain(0xcf222e);
    expect(Object.values(KIND_COLOR)).not.toContain(0x2ea043);
  });

  it("gives renamed a hue of its own, and emphatically not the deletion hue", () => {
    // A rename's whole news is that the object was *not* deleted; painting it in
    // or near removed's colour would say the opposite of what the change means.
    expect(KIND_COLOR["renamed"]).not.toBe(KIND_COLOR["removed"]);
    expect(new Set(Object.values(KIND_COLOR)).size).toBe(Object.keys(KIND_COLOR).length);
  });

  it("keeps the numeric and CSS forms of each colour in agreement", () => {
    for (const kind of ["added", "modified", "removed", "renamed"] as const) {
      expect(KIND_CSS[kind]!.toLowerCase()).toBe(hexCss(KIND_COLOR[kind]!));
    }
  });

  it("is the same palette the 3D scene draws with", () => {
    expect(SCENE_KIND_COLOR).toBe(KIND_COLOR);
    expect(SCENE_NEUTRAL).toBe(NEUTRAL);
  });

  it("separates every pair of change colours by a wide margin in blue-yellow space", () => {
    // Deuteranopia and protanopia collapse the red–green axis; what survives is
    // the blue–yellow axis and lightness. Compare the palette on b* alone.
    const bStar = (hex: number): number => {
      const b = (hex & 0xff) / 255;
      const r = ((hex >> 16) & 0xff) / 255;
      const g = ((hex >> 8) & 0xff) / 255;
      return (r + g) / 2 - b; // crude yellow-vs-blue axis
    };
    const values = (["added", "modified", "removed"] as const).map((k) => bStar(KIND_COLOR[k]!));
    for (let i = 0; i < values.length; i++) {
      for (let j = i + 1; j < values.length; j++) {
        expect(Math.abs(values[i]! - values[j]!)).toBeGreaterThan(0.15);
      }
    }
  });

  it("separates renamed from the kinds painted beside it on the same solid model", () => {
    // Hue is the only cue between kinds tinted onto one opaque surface, and
    // renamed joins added and modified there. Removed is the exception this
    // palette has always made — it is drawn as a translucent ghost of the previous
    // version, so it is never told apart by colour alone, which is just as well:
    // bluish green and reddish purple sit closer together on this axis than the
    // solid-model kinds are allowed to.
    const bStar = (hex: number): number => {
      const b = (hex & 0xff) / 255;
      const r = ((hex >> 16) & 0xff) / 255;
      const g = ((hex >> 8) & 0xff) / 255;
      return (r + g) / 2 - b;
    };
    const renamed = bStar(KIND_COLOR["renamed"]!);
    for (const kind of ["added", "modified"] as const) {
      expect(Math.abs(renamed - bStar(KIND_COLOR[kind]!))).toBeGreaterThan(0.15);
    }
  });
});

describe("changeTreeCss (the lite change tree's palette override)", () => {
  const css = changeTreeCss();

  it("restates every change kind's colour for marks and count chips", () => {
    for (const kind of ["added", "modified", "removed", "renamed"] as const) {
      expect(css).toContain(`.fhr-diff__mark--${kind}{color:${KIND_CSS[kind]}}`);
      expect(css).toContain(`.fhr-diff__count--${kind}`);
    }
  });

  it("matches the SDK's dark-theme specificity so it wins in both themes", () => {
    // The SDK styles dark theme as `.fhr-diff[data-theme="dark"] .fhr-diff__mark--x`;
    // an override without the attribute selector would lose there.
    expect(css).toContain('.fhr-diff[data-theme] .fhr-diff__mark--added');
    expect(css).toContain('.fhr-diff[data-theme] .fhr-diff__count--added');
  });

  it("carries no red/green leftovers", () => {
    expect(css.toLowerCase()).not.toContain("#cf222e");
    expect(css.toLowerCase()).not.toContain("#1a7f37");
    expect(css.toLowerCase()).not.toContain("#3fb950");
  });
});
