import { describe, it, expect } from "vitest";
import bundle, { formatPsdChange } from "./index.js";

const row = (label: string, before: unknown, after: unknown) => ({
  path: "layers/1/x",
  kind: "modified" as const,
  label,
  depth: 2,
  before,
  after,
  hasChildren: false,
});

describe("psd renderer bundle", () => {
  it("declares the psd handler and its extensions via the mount() contract", () => {
    expect(bundle.handlerId).toBe("psd");
    expect(bundle.extensions).toEqual([".psd", ".psb"]);
    expect(bundle.fhrVersion).toBe(1);
    expect(typeof bundle.mount).toBe("function");
  });

  it("draws diffs only, so a host keeps its own viewer for a single file", () => {
    expect(bundle.modes).toEqual(["diff"]);
  });

  it("shows a text colour as chips", () => {
    const f = formatPsdChange(row("Colour", [1, 1, 1, 1], [1, 0.5, 0, 1]));
    expect(f.kind).toBe("color");
    expect(f.before).toBe("#FFFFFF");
    expect(f.after).toBe("#FF8000");
  });

  it("shows a move in canvas pixels, not metres", () => {
    const f = formatPsdChange(row("Position (centre)", [200, 150], [250, 162.5]));
    expect(f.kind).toBe("vector");
    expect(f.deltaCell).toBe("Δ(50, 12.5) = 51.54");
  });

  it("shows a turn in degrees", () => {
    const f = formatPsdChange(row("Rotation", "0°", "15°"));
    expect(f.kind).toBe("angle");
    expect(f.deltaCell).toBe("Δ +15°");
  });
});
