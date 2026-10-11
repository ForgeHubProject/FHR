import { describe, it, expect } from "vitest";
import bundle from "./index.js";

describe("csv renderer bundle", () => {
  it("declares the csv handler and its extension via the mount() contract", () => {
    expect(bundle.handlerId).toBe("csv");
    expect(bundle.extensions).toContain(".csv");
    expect(bundle.fhrVersion).toBe(1);
    expect(typeof bundle.mount).toBe("function");
  });

  it("draws diffs only, so a host keeps its own viewer for a single file", () => {
    expect(bundle.modes).toEqual(["diff"]);
  });
});
