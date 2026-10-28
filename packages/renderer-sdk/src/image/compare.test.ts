// The image compare through the fake document: which views, layers, toggles
// and frame marks it builds, and how they respond. Image decoding, canvas
// painting and layout are a real browser's — a fake image "loads" when a test
// fires its load event, and a fake canvas has no context to paint with.

import { describe, it, expect, vi, afterEach } from "vitest";
import type { MountProps, StructuredDiff } from "@fhr/types";
import { renderImageDiff, IMAGE_VIEWS, type AnimatedImage } from "./compare.js";
import { createFakeDocument, asElement, type FakeElement } from "../fake-dom.js";

const diff: StructuredDiff = {
  version: "1.0",
  format: "png",
  changes: [{ path: "dimensions", kind: "modified", label: "dimensions", before: "300x200", after: "360x200" }],
};

function mount(props: Partial<MountProps>, opts: Parameters<typeof renderImageDiff>[2] = {}) {
  const doc = createFakeDocument();
  const created: FakeElement[] = [];
  const createElement = doc.createElement.bind(doc);
  doc.createElement = (tag: string) => {
    const el = createElement(tag);
    created.push(el);
    return el;
  };
  const container = doc.createElement("div");
  const cleanup = renderImageDiff(asElement(container), { mode: "diff", diff, ...props }, opts);
  const all = (cls: string) => container.byClass(cls);
  const layers = () => all("fhr-img__layer");
  const tab = (label: string) => all("fhr-img__view").find((b) => b.textContent === label)!;
  const toggle = (label: string) => {
    const wrap = all("fhr-img__toggle").find((t) => t.allText().includes(label));
    return wrap?.childNodes.find((c) => c.tagName === "INPUT");
  };
  const flip = (label: string, on: boolean) => {
    const input = toggle(label)! as FakeElement & { checked: boolean };
    input.checked = on;
    input.fire("change");
  };
  const load = (img: FakeElement, w: number, h: number) => {
    Object.assign(img, { naturalWidth: w, naturalHeight: h });
    img.fire("load");
  };
  return { container, cleanup, all, layers, tab, toggle, flip, load, created };
}

const both = { blobs: { base: { url: "blob:base", size: 1 }, head: { url: "blob:head", size: 1 } } };

describe("image compare views", () => {
  it("opens on the new image alone, with the views and toggles in a toolbar", () => {
    const m = mount(both);
    expect(m.all("fhr-img__view").map((b) => b.textContent)).toEqual(IMAGE_VIEWS.map((v) => v.label));
    expect(m.tab("Image").attributes["aria-selected"]).toBe("true");
    expect(m.layers().map((l) => l.attributes["src"])).toEqual(["blob:head"]);
    expect(m.toggle("Show differences")).toBeDefined();
    expect(m.toggle("Show metadata")).toBeDefined();
    // Nothing else: no overlay, no frame strip, no change tree.
    expect(m.all("fhr-img__overlay")).toEqual([]);
    expect(m.all("fhr-img__frames")[0]!.childNodes).toEqual([]);
    expect(m.all("fhr-diff")).toEqual([]);
  });

  it("stacks both images under a divider in the slider, before first", () => {
    const m = mount(both);
    m.tab("Slider").fire("click");
    expect(m.tab("Slider").attributes["aria-selected"]).toBe("true");
    expect(m.tab("Image").attributes["aria-selected"]).toBe("false");
    expect(m.all("fhr-img__port")).toHaveLength(1);
    expect(m.layers().map((l) => l.attributes["src"])).toEqual(["blob:base", "blob:head"]);
    const divider = m.all("fhr-img__divider")[0]!;
    expect(divider.attributes["role"]).toBe("slider");
    expect(divider.attributes["aria-valuenow"]).toBe("50");
    expect(m.all("fhr-img__badge").map((b) => b.textContent)).toEqual(["Before", "After"]);
  });

  it("moves the divider from the keyboard", () => {
    const m = mount(both, { view: "slider" });
    const divider = m.all("fhr-img__divider")[0]!;
    divider.fire("keydown", { key: "ArrowRight", preventDefault() {} });
    expect(divider.attributes["aria-valuenow"]).toBe("52");
    divider.fire("keydown", { key: "Home", preventDefault() {} });
    expect(divider.attributes["aria-valuenow"]).toBe("0");
  });

  it("gives each side its own window side by side, zooming independently", () => {
    const m = mount(both);
    m.tab("Side by side").fire("click");
    const panes = m.all("fhr-img__pane");
    expect(panes.map((p) => p.byClass("fhr-img__label")[0]!.allText())).toEqual(["Before", "After"]);
    expect(m.layers().map((l) => l.attributes["src"])).toEqual(["blob:base", "blob:head"]);
    const zoomIn = panes[1]!.descendants().find((b) => b.attributes["aria-label"] === "Zoom in")!;
    zoomIn.fire("click");
    const stages = m.all("fhr-img__stage").map((s) => s.style["transform"]);
    expect(stages).toEqual(["translate(0px, 0px) scale(1)", "translate(0px, 0px) scale(1.5)"]);
  });

  it("sizes the window to the image once it loads, and labels the sizes", () => {
    const m = mount(both, { view: "side-by-side" });
    const [before, after] = m.layers();
    m.load(before!, 300, 200);
    m.load(after!, 360, 200);
    expect(m.all("fhr-img__label").map((l) => l.allText())).toEqual(["Before 300 × 200", "After 360 × 200"]);
    expect(m.all("fhr-img__stage").map((s) => s.style["width"])).toEqual(["300px", "360px"]);
    // The taller of the two plus a margin, so both panes stand level.
    expect(m.all("fhr-img__port").map((p) => p.style["height"])).toEqual(["232px", "232px"]);
  });

  it("keeps the reviewer's place across a view switch, from the window last moved", () => {
    const m = mount(both);
    const zoomIn = (pane = m.container) => pane.descendants().find((b) => b.attributes["aria-label"] === "Zoom in")!;
    const scales = () => m.all("fhr-img__stage").map((s) => /scale\(([\d.]+)\)/.exec(s.style["transform"]!)![1]);
    m.all("fhr-img__port")[0]!.fire("click");
    zoomIn().fire("click");
    m.tab("Slider").fire("click");
    expect(scales()).toEqual(["1.5"]);
    m.tab("Side by side").fire("click");
    expect(scales()).toEqual(["1.5", "1.5"]);
    // The panes then go their own ways; the one last moved sets the next view.
    const before = m.all("fhr-img__pane")[0]!;
    before.byClass("fhr-img__port")[0]!.fire("click");
    zoomIn(before).fire("click");
    expect(scales()).toEqual(["2", "1.5"]);
    m.tab("Image").fire("click");
    expect(scales()).toEqual(["2"]);
    // The place holds until the reviewer moves again.
    m.tab("Slider").fire("click");
    expect(scales()).toEqual(["2"]);
  });

  it("keeps the slider's divider where it was left", () => {
    const m = mount(both, { view: "slider" });
    m.all("fhr-img__divider")[0]!.fire("keydown", { key: "End", preventDefault() {} });
    m.tab("Image").fire("click");
    m.tab("Slider").fire("click");
    expect(m.all("fhr-img__divider")[0]!.attributes["aria-valuenow"]).toBe("100");
  });

  it("can open in another view", () => {
    const m = mount(both, { view: "side-by-side" });
    expect(m.tab("Side by side").attributes["aria-selected"]).toBe("true");
    expect(m.all("fhr-img__port")).toHaveLength(2);
  });
});

describe("one side only", () => {
  it("shows an added file as just its image, with only the metadata toggle", () => {
    const m = mount({ blobs: { head: { url: "blob:new", size: 1 } } });
    expect(m.all("fhr-img__view")).toEqual([]);
    expect(m.toggle("Show differences")).toBeUndefined();
    expect(m.toggle("Show metadata")).toBeDefined();
    expect(m.layers().map((l) => [l.attributes["src"], l.attributes["alt"]])).toEqual([["blob:new", ""]]);
  });

  it("shows a deleted file as the image it was", () => {
    const m = mount({ blobs: { base: { url: "blob:old", size: 1 } } });
    expect(m.layers().map((l) => l.attributes["src"])).toEqual(["blob:old"]);
  });

  it("falls back to the change tree when no image reached it", () => {
    const m = mount({});
    expect(m.all("fhr-diff")).toHaveLength(1);
    expect(m.layers()).toEqual([]);
  });

  it("has no toolbar at all for an added file without a change tree", () => {
    const m = mount({ blobs: { head: { url: "blob:new", size: 1 } }, diff: undefined });
    expect(m.all("fhr-img__bar")).toEqual([]);
  });
});

describe("toggles", () => {
  it("adds and removes the change tree with Show metadata", () => {
    const m = mount(both);
    m.flip("Show metadata", true);
    expect(m.all("fhr-img__meta")[0]!.byClass("fhr-diff")).toHaveLength(1);
    expect(m.all("fhr-img__meta")[0]!.allText()).toContain("300x200");
    m.flip("Show metadata", false);
    expect(m.all("fhr-img__meta")[0]!.childNodes).toEqual([]);
  });

  it("adds an overlay to every window with Show differences, and says when it can't compare", async () => {
    const m = mount(both, { view: "side-by-side" });
    m.flip("Show differences", true);
    expect(m.all("fhr-img__overlay")).toHaveLength(2);
    expect(m.all("fhr-img__note")[0]!.allText()).toBe("Comparing pixels…");
    // The compare decodes both images off-screen; a fake canvas can't paint
    // them, which is the honest failure a reviewer sees.
    const probes = m.created.filter((e) => e.tagName === "IMG" && !e.className);
    expect(probes.map((p) => p.attributes["src"])).toEqual(["blob:base", "blob:head"]);
    for (const p of probes) m.load(p, 4, 4);
    await vi.waitFor(() => expect(m.all("fhr-img__note")[0]!.allText()).toContain("Pixels can't be compared here"));
    expect(m.all("fhr-img__note")[0]!.allText()).toContain("no canvas");
    m.flip("Show differences", false);
    expect(m.all("fhr-img__overlay")).toEqual([]);
    expect(m.all("fhr-img__note")[0]!.allText()).toBe("");
  });

  it("keeps each window's zoom when differences are switched on and off", () => {
    const m = mount(both, { view: "side-by-side" });
    const zoomIn = m.all("fhr-img__pane")[1]!.descendants().find((b) => b.attributes["aria-label"] === "Zoom in")!;
    zoomIn.fire("click");
    m.flip("Show differences", true);
    expect(m.all("fhr-img__stage").map((s) => s.style["transform"]).at(1)).toContain("scale(1.5)");
    m.flip("Show differences", false);
    expect(m.all("fhr-img__stage").map((s) => s.style["transform"]).at(1)).toContain("scale(1.5)");
    expect(m.all("fhr-img__bounds")).toEqual([]);
  });

  it("says when the browser can't display an image", () => {
    const m = mount(both);
    m.layers()[0]!.fire("error");
    expect(m.all("fhr-img__note")[0]!.allText()).toBe("The browser can't display the after image.");
  });
});

/** A stand-in animation: frame i is a w × 1 strip of one grey level. */
function animation(frames: Array<[level: number, delay: number]>, w = 2): AnimatedImage {
  return {
    width: w,
    height: 1,
    frameCount: frames.length,
    delayMs: (i) => frames[i]![1],
    frame: (i) => {
      const data = new Uint8ClampedArray(w * 4);
      for (let p = 0; p < w; p++) data.set([frames[i]![0], frames[i]![0], frames[i]![0], 255], p * 4);
      return data;
    },
  };
}

describe("animated images", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  const animate = async (base: AnimatedImage | null, head: AnimatedImage | null) => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    // The "bytes" fetched for a blob are its url; the decoder looks them up.
    vi.stubGlobal("fetch", async (url: string) => ({ arrayBuffer: async () => new TextEncoder().encode(url).buffer }));
    const byUrl: Record<string, AnimatedImage | null> = { "blob:base": base, "blob:head": head };
    const decodeAnimation = (bytes: Uint8Array) => byUrl[new TextDecoder().decode(bytes)] ?? null;
    const m = mount(both, { decodeAnimation });
    await vi.advanceTimersByTimeAsync(0);
    const cells = () => m.all("fhr-img__cell");
    const info = () => m.all("fhr-img__frameinfo")[0]?.childNodes.map((c) => c.textContent) ?? [];
    return { ...m, cells, info };
  };

  it("marks which frames changed: pixels, timing, added", async () => {
    const m = await animate(
      animation([[10, 100], [20, 100], [30, 100]]),
      animation([[10, 100], [99, 100], [30, 300], [40, 100]]),
    );
    expect(m.cells().map((c) => c.className.replace("fhr-img__cell", "").trim())).toEqual([
      "",
      "fhr-img__cell--changed",
      "fhr-img__cell--changed",
      "fhr-img__cell--added",
    ]);
    expect(m.info()).toEqual(["Frame 1 of 4 · 100 ms · same pixels", "3 → 4 frames, 3 differ"]);
    // The frames draw on canvases, the after side in the image view.
    expect(m.layers().map((l) => [l.tagName, l.attributes["aria-label"]])).toEqual([["CANVAS", "After"]]);
  });

  it("marks frames only the before side has as removed", async () => {
    const m = await animate(animation([[1, 50], [2, 50]]), animation([[1, 50]]));
    expect(m.cells()[1]!.className).toContain("fhr-img__cell--removed");
    expect(m.info()[1]).toBe("2 → 1 frames, 1 differ");
  });

  it("plays at the frames' own delays, and a frame click pauses on that frame", async () => {
    const m = await animate(
      animation([[10, 100], [20, 100], [30, 100]]),
      animation([[10, 100], [99, 100], [30, 300], [40, 100]]),
    );
    const play = m.all("fhr-img__play")[0]!;
    expect(play.attributes["aria-label"]).toBe("Pause");
    await vi.advanceTimersByTimeAsync(100);
    expect(m.info()[0]).toBe("Frame 2 of 4 · 100 ms · pixels differ");
    expect(m.cells()[1]!.attributes["aria-current"]).toBe("true");
    await vi.advanceTimersByTimeAsync(100);
    expect(m.info()[0]).toBe("Frame 3 of 4 · 100 → 300 ms · same pixels");
    await vi.advanceTimersByTimeAsync(299); // the after side's 300 ms
    expect(m.info()[0]).toContain("Frame 3 of 4");
    await vi.advanceTimersByTimeAsync(1);
    expect(m.info()[0]).toBe("Frame 4 of 4 · 100 ms · only after");

    m.cells()[1]!.fire("click");
    expect(play.attributes["aria-label"]).toBe("Play");
    await vi.advanceTimersByTimeAsync(1000);
    expect(m.info()[0]).toContain("Frame 2 of 4");
    play.fire("click");
    await vi.advanceTimersByTimeAsync(100);
    expect(m.info()[0]).toContain("Frame 3 of 4");
  });

  it("hides a side that has no such frame, and compares the frame on screen", async () => {
    const m = await animate(animation([[10, 100]]), animation([[10, 100], [40, 100]]));
    m.tab("Slider").fire("click");
    m.cells()[1]!.fire("click");
    expect(m.layers().map((l) => l.attributes["data-absent"])).toEqual(["1", "0"]);
    m.flip("Show differences", true);
    expect(m.all("fhr-img__note")[0]!.allText()).toContain("Frame 2: 2 pixels differ (100%)");
    m.cells()[0]!.fire("click");
    expect(m.all("fhr-img__note")[0]!.allText()).toBe("Frame 1: No pixel differs");
  });

  it("stops playing when torn down", async () => {
    const m = await animate(animation([[1, 100], [2, 100]]), animation([[1, 100], [3, 100]]));
    (m.cleanup as () => void)();
    await vi.advanceTimersByTimeAsync(1000);
    expect(m.info()[0]).toContain("Frame 1 of 2");
  });

  it("compares two single-frame images as stills", async () => {
    const m = await animate(animation([[1, 100]]), animation([[2, 100]]));
    expect(m.cells()).toEqual([]);
    expect(m.layers().map((l) => l.tagName)).toEqual(["IMG"]);
  });

  it("compares as stills when a side can't be decoded", async () => {
    const m = await animate(null, animation([[1, 100], [2, 100]]));
    expect(m.cells()).toEqual([]);
    expect(m.layers().map((l) => l.attributes["src"])).toEqual(["blob:head"]);
  });
});

describe("image compare marks (a structured format's outlines)", () => {
  const marks = [
    { key: "layers/4", side: "base" as const, x: 10, y: 20, w: 30, h: 40, kind: "modified" as const, label: "Sun" },
    { key: "layers/4", side: "head" as const, x: 12, y: 20, w: 30, h: 40, kind: "modified" as const, label: "Sun" },
    { key: "layers/9", side: "head" as const, x: 0, y: 0, w: 5, h: 5, kind: "added" as const },
    { key: "layers/7", side: "base" as const, x: 50, y: 50, w: 10, h: 10, kind: "removed" as const },
  ];
  const keysOf = (m: ReturnType<typeof mount>) => m.all("fhr-img__mark").map((b) => `${b.attributes["data-key"]}:${b.className.split(" ")[1]}`);

  it("outlines instead of tinting pixels, only once differences are shown", () => {
    const m = mount(both, { marks });
    expect(m.all("fhr-img__mark")).toEqual([]);
    m.flip("Show differences", true);
    // The after image: its own marks, and where the removed layer was.
    expect(keysOf(m)).toEqual(["layers/4:fhr-img__mark--modified", "layers/9:fhr-img__mark--added", "layers/7:fhr-img__mark--removed"]);
    expect(m.all("fhr-img__overlay")).toEqual([]);
    expect(m.all("fhr-img__note")[0]!.allText()).toContain("3 changes outlined.");
    m.flip("Show differences", false);
    expect(m.all("fhr-img__mark")).toEqual([]);
  });

  it("gives each side-by-side pane its own side's marks", () => {
    const m = mount(both, { marks, view: "side-by-side" });
    m.flip("Show differences", true);
    const panes = m.all("fhr-img__pane");
    const keys = (p: FakeElement) => p.byClass("fhr-img__mark").map((b) => b.attributes["data-key"]);
    expect(keys(panes[0]!)).toEqual(["layers/4", "layers/7"]);
    expect(keys(panes[1]!)).toEqual(["layers/4", "layers/9"]);
  });

  it("places an outline in window space around its box", () => {
    const m = mount(both, { marks: [marks[1]!] });
    m.flip("Show differences", true);
    const box = m.all("fhr-img__mark")[0]!;
    // Zoom 1 before anything loads: the box, grown by the 2 px border.
    expect([box.style["left"], box.style["top"], box.style["width"], box.style["height"]]).toEqual(["10px", "18px", "34px", "44px"]);
    expect(box.attributes["title"]).toBe("Sun");
  });

  it("reports pointing and picking, lights a change's outlines on both sides, and takes new marks", () => {
    const seen: [string | null, boolean][] = [];
    let control: { setMarks(m: typeof marks): void; highlight(k: string | null): void } | undefined;
    const m = mount(both, { marks, view: "side-by-side", onMark: (k, p) => seen.push([k, p]), onReady: (c) => (control = c) });
    m.flip("Show differences", true);
    const sun = () => m.all("fhr-img__mark").filter((b) => b.attributes["data-key"] === "layers/4");
    sun()[0]!.fire("pointerenter");
    expect(sun().every((b) => b.className.includes("fhr-img__mark--hot"))).toBe(true);
    sun()[0]!.fire("click");
    sun()[0]!.fire("pointerleave");
    expect(seen).toEqual([["layers/4", false], ["layers/4", true], [null, false]]);
    expect(sun().some((b) => b.className.includes("fhr-img__mark--hot"))).toBe(false);

    control!.highlight("layers/7");
    expect(m.all("fhr-img__mark--hot").map((b) => b.attributes["data-key"])).toEqual(["layers/7"]);
    control!.setMarks([marks[2]!]);
    expect(keysOf(m)).toEqual(["layers/9:fhr-img__mark--added"]);
    expect(m.all("fhr-img__note")[0]!.allText()).toContain("1 change outlined.");
  });
});
