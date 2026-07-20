import { describe, it, expect } from "vitest";
import bundle from "./index.js";

describe("image-meta renderer bundle", () => {
  it("declares the image-meta handler and its extensions via the mount() contract", () => {
    expect(bundle.handlerId).toBe("image-meta");
    expect(bundle.extensions).toEqual([".png", ".jpg", ".jpeg", ".gif"]);
    expect(bundle.fhrVersion).toBe(1);
    expect(typeof bundle.mount).toBe("function");
  });
});
