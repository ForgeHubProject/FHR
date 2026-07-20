import { describe, it, expect } from "vitest";
import bundle from "./index.js";

describe("obj renderer bundle", () => {
  it("declares the obj handler and its extension via the mount() contract", () => {
    expect(bundle.handlerId).toBe("obj");
    expect(bundle.extensions).toContain(".obj");
    expect(bundle.fhrVersion).toBe(1);
    expect(typeof bundle.mount).toBe("function");
  });
});
