// The panel formatting rules, on the exact value strings the glTF handler emits
// (Blender-space vectors "[1.00 2.00 -3.00]", euler degrees "(0.00° 45.00° 0.00°)",
// colour factors "[0.80 0.10 0.10 1.00]", index references "mesh[3]").

import { describe, it, expect } from "vitest";
import { formatChange, parseNumeric, formatLength, trimNumber } from "./format.js";

describe("parseNumeric", () => {
  it("reads the handler's bracketed vectors", () => {
    expect(parseNumeric("[1.00 2.00 -3.00]")).toEqual({ values: [1, 2, -3], degrees: false });
  });

  it("reads euler degrees and remembers the unit", () => {
    expect(parseNumeric("(0.00° 45.00° 0.00°)")).toEqual({ values: [0, 45, 0], degrees: true });
  });

  it("reads comma-separated and bare numbers", () => {
    expect(parseNumeric("[1, 2, 3]")?.values).toEqual([1, 2, 3]);
    expect(parseNumeric("0.50")?.values).toEqual([0.5]);
  });

  it("refuses anything that isn't all numbers", () => {
    for (const text of ["mesh[3]", "<none>", "OPAQUE", "true", "", "[1 x 3]"]) {
      expect(parseNumeric(text), text).toBeNull();
    }
  });
});

describe("trimNumber / formatLength", () => {
  it("drops the trailing zeros a fixed-precision formatter left behind", () => {
    expect(trimNumber(1)).toBe("1");
    expect(trimNumber(-3.0)).toBe("-3");
    expect(trimNumber(0.123456)).toBe("0.123");
    expect(trimNumber(-0)).toBe("0");
  });

  it("shows sub-metre distances in millimetres", () => {
    expect(formatLength(0.05)).toBe("50 mm");
    expect(formatLength(0.0005)).toBe("0.5 mm");
    expect(formatLength(-0.012)).toBe("-12 mm");
    expect(formatLength(1.5)).toBe("1.5 m");
    expect(formatLength(0)).toBe("0");
  });
});

describe("formatChange — translations", () => {
  it("gives the delta and its magnitude, the issue's headline example", () => {
    const f = formatChange({
      label: "translation",
      before: "[0.00 0.00 0.00]",
      after: "[0.00 0.05 0.00]",
    });
    expect(f.kind).toBe("vector");
    expect(f.before).toBe("(0, 0, 0)");
    expect(f.after).toBe("(0, 0.05, 0)");
    expect(f.deltaText).toBe("Δ(0, 0.05, 0)");
    expect(f.magnitude).toBe("50 mm");
    expect(f.deltaCell).toBe("Δ(0, 0.05, 0) = 50 mm");
    expect(f.noise).toBe(false);
  });

  it("measures a diagonal move as a distance, not per axis", () => {
    const f = formatChange({
      label: "translation",
      before: "[0.00 0.00 0.00]",
      after: "[0.03 0.04 0.00]",
    });
    expect(f.magnitude).toBe("50 mm");
  });
});

describe("formatChange — rotations", () => {
  it("keeps the handler's euler degrees and never shows a quaternion", () => {
    const f = formatChange({
      label: "rotation",
      before: "(0.00° 0.00° 0.00°)",
      after: "(0.00° 45.00° 0.00°)",
    });
    expect(f.kind).toBe("angle");
    expect(f.after).toBe("(0°, 45°, 0°)");
    expect(f.magnitude).toBe("45°");
    // The magnitude would only repeat the one component that moved.
    expect(f.deltaCell).toBe("Δ(0°, 45°, 0°)");
  });
});

describe("formatChange — scale", () => {
  it("reports a uniform scale as a ratio, which is how it is judged", () => {
    const f = formatChange({ label: "scale", before: "[1.00 1.00 1.00]", after: "[1.20 1.20 1.20]" });
    expect(f.magnitude).toBe("×1.2");
    expect(f.deltaCell).toBe("Δ(0.2, 0.2, 0.2) = ×1.2");
  });

  it("falls back to the largest component when the scale isn't uniform", () => {
    const f = formatChange({ label: "scale", before: "[1.00 1.00 1.00]", after: "[1.00 1.00 2.00]" });
    expect(f.magnitude).toBe("+1");
  });
});

describe("formatChange — colours", () => {
  it("is a swatch pair, never a float tuple", () => {
    const f = formatChange({
      label: "baseColorFactor",
      before: "[1.00 0.00 0.00 1.00]",
      after: "[0.00 0.00 1.00 1.00]",
    });
    expect(f.kind).toBe("color");
    expect(f.beforeSwatch?.css).toBe("rgb(255, 0, 0)");
    expect(f.afterSwatch?.css).toBe("rgb(0, 0, 255)");
    expect(f.before).toBe("#FF0000");
    expect(f.after).toBe("#0000FF");
    // No delta arithmetic on colours: the chips are the comparison.
    expect(f.deltaCell).toBeUndefined();
  });

  it("carries alpha into the chip", () => {
    const f = formatChange({ label: "baseColorFactor", before: "[1 1 1 1]", after: "[1 1 1 0.50]" });
    expect(f.afterSwatch?.css).toBe("rgba(255, 255, 255, 0.5)");
    expect(f.after).toBe("#FFFFFF 50%");
  });

  it("converts linear factors to sRGB when told the values are linear", () => {
    const srgb = formatChange({ label: "baseColorFactor", before: "[0.50 0.50 0.50 1]", after: "[1 1 1 1]" });
    const linear = formatChange(
      { label: "baseColorFactor", before: "[0.50 0.50 0.50 1]", after: "[1 1 1 1]" },
      { colorSpace: "linear" },
    );
    expect(srgb.beforeSwatch?.css).toBe("rgb(128, 128, 128)");
    expect(linear.beforeSwatch?.css).toBe("rgb(188, 188, 188)");
  });

  it("shows a chip for a one-sided colour (an added or removed material)", () => {
    const added = formatChange({ label: "baseColorFactor", after: "[0 1 0 1]" });
    expect(added.kind).toBe("color");
    expect(added.afterSwatch?.css).toBe("rgb(0, 255, 0)");
    expect(added.before).toBe("—");
    expect(added.beforeSwatch).toBeUndefined();
  });

  it("leaves vectors that aren't colours alone", () => {
    const f = formatChange({ label: "translation", before: "[0.10 0.10 0.10]", after: "[0.20 0.20 0.20]" });
    expect(f.kind).toBe("vector");
    expect(f.beforeSwatch).toBeUndefined();
  });
});

describe("formatChange — noise suppression", () => {
  it("flags array-index churn", () => {
    const f = formatChange({ label: "mesh", before: "mesh[3]", after: "mesh[5]" });
    expect(f.kind).toBe("index");
    expect(f.noise).toBe(true);
    expect(f.deltaCell).toBeUndefined();
  });

  it("does not flag a reference appearing or disappearing", () => {
    const f = formatChange({ label: "mesh", before: "<none>", after: "mesh[5]" });
    expect(f.noise).toBe(false);
  });

  it("does not flag two different kinds of reference", () => {
    const f = formatChange({ label: "mesh", before: "mesh[3]", after: "camera[3]" });
    expect(f.noise).toBe(false);
  });
});

describe("formatChange — everything else", () => {
  it("passes plain text through", () => {
    const f = formatChange({ label: "alphaMode", before: "OPAQUE", after: "BLEND" });
    expect(f.kind).toBe("text");
    expect(f.before).toBe("OPAQUE");
    expect(f.deltaCell).toBeUndefined();
  });

  it("gives a signed delta for a scalar", () => {
    const f = formatChange({ label: "metallicFactor", before: "0.20", after: "0.85" });
    expect(f.kind).toBe("number");
    expect(f.deltaText).toBe("Δ +0.65");
    expect(f.deltaCell).toBe("Δ +0.65");
  });

  it("marks an absent side and skips the arithmetic", () => {
    const f = formatChange({ label: "translation", before: undefined, after: "[1.00 2.00 3.00]" });
    expect(f.before).toBe("—");
    expect(f.after).toBe("(1, 2, 3)");
    expect(f.deltaCell).toBeUndefined();
  });

  it("skips the arithmetic when the two sides have different shapes", () => {
    const f = formatChange({ label: "translation", before: "[1.00 2.00]", after: "[1.00 2.00 3.00]" });
    expect(f.deltaText).toBeUndefined();
  });

  it("accepts raw values, not just the handler's strings", () => {
    const f = formatChange({ label: "translation", before: [0, 0, 0], after: [0, 0.05, 0] });
    expect(f.magnitude).toBe("50 mm");
  });
});
