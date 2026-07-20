import { describe, it, expect } from "vitest";
import bundle from "./index.js";

describe("svg renderer bundle", () => {
  it("declares the svg handler and its extension via the mount() contract", () => {
    expect(bundle.handlerId).toBe("svg");
    expect(bundle.extensions).toContain(".svg");
    expect(bundle.fhrVersion).toBe(1);
    expect(typeof bundle.mount).toBe("function");
  });
});
