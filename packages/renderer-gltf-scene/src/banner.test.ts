import { describe, it, expect } from "vitest";
import { createBanners, textureFailureMessage } from "./banner.js";
import { asDocument, createFakeDocument, type FakeElement } from "./fake-dom.js";
import { KIND_CSS } from "./palette.js";

/** The banner list's children, seen through the fake DOM's element shape. */
const kids = (el: HTMLElement): FakeElement[] => (el as unknown as FakeElement).childNodes;

describe("createBanners", () => {
  it("starts empty", () => {
    const banners = createBanners(asDocument(createFakeDocument()));
    expect(banners.count()).toBe(0);
    expect(kids(banners.el)).toHaveLength(0);
  });

  it("renders one element per message, carrying the message text", () => {
    const banners = createBanners(asDocument(createFakeDocument()));
    banners.add("Showing the scene-graph outline instead.");
    banners.add("2 textures live in separate files.");
    expect(banners.count()).toBe(2);
    expect(kids(banners.el)).toHaveLength(2);
    expect(kids(banners.el)[1]!.textContent).toContain("2 textures");
  });

  it("ignores repeats and blank messages", () => {
    const banners = createBanners(asDocument(createFakeDocument()));
    banners.add("same");
    banners.add("same");
    banners.add("   ");
    banners.add("");
    expect(banners.count()).toBe(1);
  });

  it("styles banners like the view's own status text, with a palette accent", () => {
    const banners = createBanners(asDocument(createFakeDocument()));
    banners.add("degraded");
    const item = kids(banners.el)[0]!;
    expect(item.style.cssText).toContain("ui-sans-serif");
    expect(item.style.cssText).toContain("13px");
    expect(item.style.cssText).toContain(KIND_CSS.modified!);
  });
});

describe("textureFailureMessage", () => {
  it("says nothing when nothing failed", () => {
    expect(textureFailureMessage([])).toBeNull();
  });

  it("aggregates failures into one honest sentence", () => {
    const msg = textureFailureMessage(["blob:abc", "blob:def"]);
    expect(msg).toContain("2 textures");
    expect(msg).toContain("untextured");
    expect(msg).toContain("change list are unaffected");
  });

  it("uses the singular for one failure", () => {
    expect(textureFailureMessage(["blob:abc"])).toContain("1 texture ");
  });
});
