import { describe, it, expect } from "vitest";
import bundle, { renderImage } from "./index.js";

describe("jpeg renderer bundle", () => {
  it("declares the jpeg handler and its extensions via the mount() contract", () => {
    expect(bundle.handlerId).toBe("jpeg");
    expect(bundle.extensions).toEqual([".jpg", ".jpeg"]);
    expect(bundle.fhrVersion).toBe(1);
    expect(typeof bundle.mount).toBe("function");
  });

  it("declares the modes it draws (SPEC-RENDERING §2b)", () => {
    expect(bundle.modes).toEqual(["view", "diff"]);
  });
});

// Just enough DOM for renderImage: elements that record attributes and children.
type El = { tag: string; attrs: Record<string, string>; children: El[]; textContent: string; className: string; style: { cssText: string } };
function container(): El {
  const make = (tag: string) => {
    const el = {
      tag, attrs: {} as Record<string, string>, children: [] as El[], textContent: "", className: "", style: { cssText: "" },
      setAttribute(k: string, v: string) { el.attrs[k] = v; },
      appendChild(c: El) { el.children.push(c); },
    };
    return el;
  };
  return Object.assign(make("div"), { ownerDocument: { createElement: make } });
}

describe("jpeg renderer view", () => {
  it("shows the head blob as an image", () => {
    const el = container();
    renderImage(el as unknown as HTMLElement, { mode: "view", blobs: { head: { url: "blob:x/1", size: 9 } } });
    const img = el.children[0]!.children[0]!;
    expect(img.tag).toBe("img");
    expect(img.attrs["src"]).toBe("blob:x/1");
  });

  it("follows the host theme for the transparency checkerboard", () => {
    const el = container();
    renderImage(el as unknown as HTMLElement, { mode: "view", theme: "dark", blobs: { head: { url: "u", size: 1 } } });
    expect(el.children[0]!.style.cssText).toContain("#161b22");
  });

  it("says so when the host passed no file", () => {
    const el = container();
    renderImage(el as unknown as HTMLElement, { mode: "view" });
    expect(el.children[0]!.textContent).toBe("No file provided.");
  });
});
