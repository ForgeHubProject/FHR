import { describe, it, expect } from "vitest";
import bundle, { renderPlayer } from "./index.js";

describe("wav renderer bundle", () => {
  it("declares the wav handler and its extension via the mount() contract", () => {
    expect(bundle.handlerId).toBe("wav");
    expect(bundle.extensions).toContain(".wav");
    expect(bundle.fhrVersion).toBe(1);
    expect(typeof bundle.mount).toBe("function");
  });

  it("declares the modes it draws (SPEC-RENDERING §2b)", () => {
    expect(bundle.modes).toEqual(["view", "diff"]);
  });
});

// Just enough DOM for renderPlayer: elements that record attributes and children.
type El = { tag: string; attrs: Record<string, string>; children: El[]; textContent: string; className: string; style: { cssText: string } };
function container(): El & { ownerDocument: { createElement(t: string): El }; appendChild(c: El): void } {
  const make = (tag: string): El & { setAttribute(k: string, v: string): void; appendChild(c: El): void } => {
    const el = {
      tag, attrs: {} as Record<string, string>, children: [] as El[], textContent: "", className: "", style: { cssText: "" },
      setAttribute(k: string, v: string) { el.attrs[k] = v; },
      appendChild(c: El) { el.children.push(c); },
    };
    return el;
  };
  const root = make("div");
  return Object.assign(root, { ownerDocument: { createElement: make } });
}

describe("wav renderer view", () => {
  it("plays the head blob with the browser's audio player", () => {
    const el = container();
    renderPlayer(el as unknown as HTMLElement, { mode: "view", blobs: { head: { url: "blob:x/1", size: 44 } } });
    const audio = el.children[0]!.children[0]!;
    expect(audio.tag).toBe("audio");
    expect(audio.attrs).toMatchObject({ src: "blob:x/1", controls: "", preload: "metadata" });
  });

  it("says so when the host passed no file", () => {
    const el = container();
    renderPlayer(el as unknown as HTMLElement, { mode: "view" });
    expect(el.children[0]!.textContent).toBe("No file provided.");
  });
});
