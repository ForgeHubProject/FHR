import { describe, it, expect } from "vitest";
import { Box3, Vector3 } from "three";
import { boxSize, defaultSplit, otherSplit, splitPanes, SPLIT_GAP } from "./split.js";

describe("defaultSplit — the initial choice, from the model's own proportions", () => {
  it("stacks a wide model, so each pane is wide too", () => {
    expect(defaultSplit({ x: 10, y: 2, z: 3 })).toBe("rows");
  });

  it("puts a tall model side by side", () => {
    expect(defaultSplit({ x: 1, y: 8, z: 1 })).toBe("columns");
  });

  it("reads width off whichever ground axis is longer", () => {
    // glTF is Y-up, so a model can be long in X or in Z and be equally "wide".
    expect(defaultSplit({ x: 1, y: 2, z: 9 })).toBe("rows");
  });

  it("falls back to columns when there are no proportions to read", () => {
    expect(defaultSplit({ x: 0, y: 0, z: 0 })).toBe("columns");
    expect(defaultSplit({ x: 5, y: 0, z: 5 })).toBe("columns");
  });
});

describe("boxSize", () => {
  it("measures a box", () => {
    const box = new Box3(new Vector3(-1, 0, -2), new Vector3(3, 4, 2));
    expect(boxSize(box)).toEqual({ x: 4, y: 4, z: 4 });
  });

  it("reports an empty box as nothing, not as -Infinity", () => {
    // An empty Box3 carries ±Infinity bounds; letting that reach defaultSplit
    // would make the initial orientation a coin flip on a NaN comparison.
    expect(boxSize(new Box3())).toEqual({ x: 0, y: 0, z: 0 });
  });
});

describe("splitPanes — scissor geometry", () => {
  it("cuts two columns that add back up to the canvas exactly", () => {
    const [left, right] = splitPanes("columns", { width: 800, height: 400 });
    expect(left!.gl).toEqual({ x: 0, y: 0, width: 399, height: 400 });
    expect(right!.gl).toEqual({ x: 401, y: 0, width: 399, height: 400 });
    expect(left!.gl.width + SPLIT_GAP + right!.gl.width).toBe(800);
  });

  it("gives the remainder to the second pane on an odd width", () => {
    // A rounding leak shows up as an unpainted seam column, which reads as a
    // rendering bug rather than as a gutter.
    const [left, right] = splitPanes("columns", { width: 801, height: 400 });
    expect(left!.gl.width).toBe(399);
    expect(right!.gl.width).toBe(400);
    expect(left!.gl.width + SPLIT_GAP + right!.gl.width).toBe(801);
  });

  it("puts the previous version first — left, and top", () => {
    expect(splitPanes("columns", { width: 800, height: 400 }).map((p) => p.side)).toEqual(["base", "head"]);
    expect(splitPanes("rows", { width: 800, height: 400 }).map((p) => p.side)).toEqual(["base", "head"]);
  });

  it("flips CSS and GL vertical origins for a stacked split", () => {
    // CSS runs top-down and GL bottom-up: the top pane is the one with the
    // *higher* GL y, and getting this backwards silently swaps the two labels.
    const [top, bottom] = splitPanes("rows", { width: 800, height: 400 });
    expect(top!.gl).toEqual({ x: 0, y: 201, width: 800, height: 199 });
    expect(top!.css).toEqual({ x: 0, y: 0, width: 800, height: 199 });
    expect(bottom!.gl).toEqual({ x: 0, y: 0, width: 800, height: 199 });
    expect(bottom!.css).toEqual({ x: 0, y: 201, width: 800, height: 199 });
  });

  it("gives each pane its own aspect", () => {
    const [left] = splitPanes("columns", { width: 800, height: 400 });
    expect(left!.aspect).toBeCloseTo(399 / 400, 6);
    const [top] = splitPanes("rows", { width: 800, height: 400 });
    expect(top!.aspect).toBeCloseTo(800 / 199, 6);
  });

  it("survives a canvas too small to hold a gutter", () => {
    const panes = splitPanes("columns", { width: 1, height: 1 });
    expect(panes).toHaveLength(2);
    for (const pane of panes) expect(pane.gl.width).toBeGreaterThanOrEqual(0);
  });
});

describe("otherSplit", () => {
  it("is the toggle", () => {
    expect(otherSplit("columns")).toBe("rows");
    expect(otherSplit("rows")).toBe("columns");
  });
});
