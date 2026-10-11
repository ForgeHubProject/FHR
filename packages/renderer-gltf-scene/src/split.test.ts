import { describe, it, expect } from "vitest";
import { Box3, Vector3 } from "three";
import { boxSize, defaultSplit, drawSplit, otherSplit, splitPanes, SPLIT_GAP, type SplitTarget } from "./split.js";

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

// The gutter is deliberate; unpainted pixels in it are not. three.js clears
// *inside* render(), and gl.clear obeys the scissor box, so two scissored passes
// write the two panes and nothing else. With alpha:false and no preserved
// drawing buffer the strip between them is undefined after a swap — in practice
// the last full-canvas frame before the mode switch, flickering between buffers.
describe("drawSplit — the gutter is painted, not left to the last frame", () => {
  type Call = { op: string; args: number[] };

  const record = (): { calls: Call[]; target: SplitTarget; scissor: boolean[] } => {
    const calls: Call[] = [];
    const scissor: boolean[] = [];
    let on = false;
    const target: SplitTarget = {
      setScissorTest(next): void {
        on = next;
        calls.push({ op: next ? "scissorOn" : "scissorOff", args: [] });
      },
      setScissor(...args): void {
        calls.push({ op: "scissor", args });
      },
      setViewport(...args): void {
        calls.push({ op: "viewport", args });
      },
      clear(): void {
        scissor.push(on);
        calls.push({ op: "clear", args: [] });
      },
    };
    return { calls, target, scissor };
  };

  const size = { width: 800, height: 400 };

  it("clears the whole canvas with the scissor test off, before any pane", () => {
    const { calls, target, scissor } = record();
    drawSplit(target, size, splitPanes("columns", size), () => calls.push({ op: "pane", args: [] }));
    expect(scissor).toEqual([false]); // exactly one clear, and it was unscissored
    const ops = calls.map((c) => c.op);
    expect(ops.indexOf("clear")).toBeLessThan(ops.indexOf("pane"));
    // The clear covers the canvas, not a pane: the gutter is what needs it.
    expect(calls[ops.indexOf("clear") - 1]).toEqual({ op: "viewport", args: [0, 0, 800, 400] });
  });

  it("draws every pane inside its own scissor rect", () => {
    const { calls, target } = record();
    const panes = splitPanes("rows", size);
    drawSplit(target, size, panes, (pane) => calls.push({ op: "pane", args: [pane.gl.x, pane.gl.y] }));
    const scissored = calls.filter((c) => c.op === "scissor").map((c) => c.args);
    expect(scissored).toEqual(panes.map((p) => [p.gl.x, p.gl.y, p.gl.width, p.gl.height]));
    expect(calls.filter((c) => c.op === "pane")).toHaveLength(2);
  });

  it("leaves the scissor test off and the viewport whole", () => {
    // A mode switch back to a single picture reuses this renderer, and a scissor
    // rect left behind would clip the full-canvas frame to one pane.
    const { calls, target } = record();
    drawSplit(target, size, splitPanes("columns", size), () => {});
    expect(calls[calls.length - 2]).toEqual({ op: "scissorOff", args: [] });
    expect(calls[calls.length - 1]).toEqual({ op: "viewport", args: [0, 0, 800, 400] });
  });
});
