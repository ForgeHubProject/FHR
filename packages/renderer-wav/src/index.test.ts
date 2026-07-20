import { describe, it, expect } from "vitest";
import bundle from "./index.js";

describe("wav renderer bundle", () => {
  it("declares the wav handler and its extension via the mount() contract", () => {
    expect(bundle.handlerId).toBe("wav");
    expect(bundle.extensions).toContain(".wav");
    expect(bundle.fhrVersion).toBe(1);
    expect(typeof bundle.mount).toBe("function");
  });
});
