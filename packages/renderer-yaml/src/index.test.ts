import { describe, it, expect } from "vitest";
import bundle from "./index.js";

describe("yaml renderer bundle", () => {
  it("declares the yaml handler and its extensions via the mount() contract", () => {
    expect(bundle.handlerId).toBe("yaml");
    expect(bundle.extensions).toContain(".yaml");
    expect(bundle.extensions).toContain(".yml");
    expect(bundle.fhrVersion).toBe(1);
    expect(typeof bundle.mount).toBe("function");
  });
});
