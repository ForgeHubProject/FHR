import { describe, it, expect } from "vitest";
import bundle, { formatFigcraftChange } from "./index.js";

const row = (label: string, before: unknown, after: unknown) => ({
  path: "layers/1/x",
  kind: "modified" as const,
  label,
  depth: 2,
  before,
  after,
  hasChildren: false,
});

describe("figcraft renderer bundle", () => {
  it("declares the figcraft handler and its extension via the mount() contract", () => {
    expect(bundle.handlerId).toBe("figcraft");
    expect(bundle.extensions).toContain(".figcraft");
    expect(bundle.fhrVersion).toBe(1);
    expect(typeof bundle.mount).toBe("function");
  });

  it("draws diffs only, so a host keeps its own viewer for a single file", () => {
    expect(bundle.modes).toEqual(["diff"]);
  });

  it("shows a fill colour as chips", () => {
    const f = formatFigcraftChange(row("Fill colour", [0.851, 0.851, 0.851, 1], [1, 0.5412, 0.2392, 1]));
    expect(f.kind).toBe("color");
    expect(f.before).toBe("#D9D9D9");
    expect(f.after).toBe("#FF8A3D");
  });

  it("shows a move in canvas pixels, not metres", () => {
    const f = formatFigcraftChange(row("Position", [0, 0], [24, 10]));
    expect(f.kind).toBe("vector");
    expect(f.deltaCell).toBe("Δ(24, 10) = 26");
    expect(f.deltaCell).not.toMatch(/m\b/);
  });

  it("shows a turn in degrees", () => {
    const f = formatFigcraftChange(row("Rotation", "0°", "15°"));
    expect(f.kind).toBe("angle");
    expect(f.deltaCell).toBe("Δ +15°");
  });
});
