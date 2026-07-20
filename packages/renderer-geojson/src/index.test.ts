import { describe, it, expect } from "vitest";
import bundle from "./index.js";

describe("geojson renderer bundle", () => {
  it("declares the geojson handler and its extension via the mount() contract", () => {
    expect(bundle.handlerId).toBe("geojson");
    expect(bundle.extensions).toContain(".geojson");
    expect(bundle.fhrVersion).toBe(1);
    expect(typeof bundle.mount).toBe("function");
  });
});
