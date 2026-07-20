import { describe, it, expect } from "vitest";
import bundle from "./index.js";

describe("stl renderer bundle", () => {
  it("declares the stl handler and its extension via the mount() contract", () => {
    expect(bundle.handlerId).toBe("stl");
    expect(bundle.extensions).toContain(".stl");
    expect(bundle.fhrVersion).toBe(1);
    expect(typeof bundle.mount).toBe("function");
  });
});
