import { describe, it, expect } from "vitest";
import bundle from "./index.js";

describe("json renderer bundle", () => {
  it("declares the json handler and its extension via the mount() contract", () => {
    expect(bundle.handlerId).toBe("json");
    expect(bundle.extensions).toContain(".json");
    expect(bundle.fhrVersion).toBe(1);
    expect(typeof bundle.mount).toBe("function");
  });
});
