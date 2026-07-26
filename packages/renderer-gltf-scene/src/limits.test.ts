import { describe, it, expect } from "vitest";
import { allowGhostBase, formatMb, ghostBaseSkippedMessage, GHOST_BASE_MAX_BYTES } from "./limits.js";

describe("ghost-base size gate", () => {
  it("allows a base blob at or under the cap", () => {
    expect(allowGhostBase(1024)).toBe(true);
    expect(allowGhostBase(GHOST_BASE_MAX_BYTES)).toBe(true);
  });

  it("refuses a base blob over the cap", () => {
    expect(allowGhostBase(GHOST_BASE_MAX_BYTES + 1)).toBe(false);
    expect(allowGhostBase(72 * 1024 * 1024)).toBe(false);
  });

  it("allows an unreported size — a host without sizes shouldn't lose the feature", () => {
    expect(allowGhostBase(undefined)).toBe(true);
  });

  it("treats zero, negative and non-finite sizes as no bytes", () => {
    expect(allowGhostBase(0)).toBe(false);
    expect(allowGhostBase(-1)).toBe(false);
    expect(allowGhostBase(Number.NaN)).toBe(false);
    expect(allowGhostBase(Number.POSITIVE_INFINITY)).toBe(false);
  });

  it("honours a caller-supplied cap", () => {
    expect(allowGhostBase(2048, 1024)).toBe(false);
    expect(allowGhostBase(512, 1024)).toBe(true);
  });

  it("formats megabytes for banners", () => {
    expect(formatMb(32 * 1024 * 1024)).toBe("32 MB");
    expect(formatMb(1.5 * 1024 * 1024)).toBe("1.5 MB");
  });

  it("explains the skip in plain language, with both numbers", () => {
    const msg = ghostBaseSkippedMessage(72 * 1024 * 1024);
    expect(msg).toContain("72 MB");
    expect(msg).toContain("32 MB");
    expect(msg).toContain("A/B blink");
    expect(msg).toContain("still painted");
  });
});
