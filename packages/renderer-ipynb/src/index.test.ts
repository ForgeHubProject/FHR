import { describe, it, expect } from "vitest";
import bundle from "./index.js";

describe("ipynb renderer bundle", () => {
  it("declares the ipynb handler and its extension via the mount() contract", () => {
    expect(bundle.handlerId).toBe("ipynb");
    expect(bundle.extensions).toContain(".ipynb");
    expect(bundle.fhrVersion).toBe(1);
    expect(typeof bundle.mount).toBe("function");
  });
});
