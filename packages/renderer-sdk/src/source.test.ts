import { describe, it, expect, vi, afterEach } from "vitest";
import type { MountProps } from "@fhr/types";
import { defineRenderer } from "./define.js";
import { looksBinary, renderSourceView } from "./source.js";
import { createFakeDocument, asElement, type FakeElement } from "./fake-dom.js";

// A container defineRenderer can clear between passes (the fake DOM has no
// replaceChildren of its own).
function container(): FakeElement {
  const el = createFakeDocument().createElement("div");
  (el as FakeElement & { replaceChildren(): void }).replaceChildren = () => {
    el.childNodes = [];
  };
  return el;
}

const head = (url = "/blob/head"): MountProps["blobs"] => ({ head: { url, size: 3 } });

function serve(body: string, status = 200) {
  const fetchMock = vi.fn(async () => ({ ok: status < 400, status, text: async () => body }));
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

/** Let the fetch → text → render chain settle. */
const settle = () => new Promise((r) => setTimeout(r, 0));

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("renderSourceView", () => {
  it("shows the head blob's text", async () => {
    const fetchMock = serve('a = 1\n[b]\nc = "x"\n');
    const el = container();
    renderSourceView(asElement(el), { mode: "view", blobs: head("/raw/a.toml") });
    await settle();
    expect(fetchMock).toHaveBeenCalledWith("/raw/a.toml");
    expect(el.byClass("fhr-source")[0]!.textContent).toBe('a = 1\n[b]\nc = "x"\n');
  });

  it("says so for a binary file instead of printing its bytes", async () => {
    serve("RIFF\u0000\u0000WAVEfmt ");
    const el = container();
    renderSourceView(asElement(el), { mode: "view", blobs: head() });
    await settle();
    expect(el.allText()).toBe("Binary file: no text to show.");
  });

  it("reports a failed fetch", async () => {
    serve("", 404);
    const el = container();
    renderSourceView(asElement(el), { mode: "view", blobs: head() });
    await settle();
    expect(el.allText()).toBe("Could not load this file (HTTP 404).");
  });

  it("says so when the host passed no file", () => {
    const el = container();
    renderSourceView(asElement(el), { mode: "view" });
    expect(el.allText()).toBe("No file provided.");
  });

  it("drops a response that arrives after cleanup", async () => {
    serve("late");
    const el = container();
    const cleanup = renderSourceView(asElement(el), { mode: "view", blobs: head() });
    (cleanup as () => void)();
    await settle();
    expect(el.allText()).toBe("Loading…");
  });

  it("follows the host theme", () => {
    const el = container();
    renderSourceView(asElement(el), { mode: "view", theme: "dark" });
    expect(el.byClass("fhr-source")[0]!.style.cssText).toContain("background:#0d1117");
  });

  it("looksBinary only looks for NUL bytes", () => {
    expect(looksBinary("plain text, ünïcode ✓")).toBe(false);
    expect(looksBinary("PNG\u0000")).toBe(true);
  });
});

describe("defineRenderer modes", () => {
  const renderer = (modes?: MountProps["mode"][]) => {
    const calls: string[] = [];
    const bundle = defineRenderer({
      handlerId: "h",
      extensions: [".h"],
      ...(modes ? { modes } : {}),
      render: (_el, p) => {
        calls.push(`render:${p.mode}`);
        return () => calls.push("cleanup");
      },
      update: (_el, p) => {
        calls.push(`update:${p.mode}`);
      },
    });
    return { bundle, calls };
  };

  it("publishes the modes it was given, and nothing when none were given", () => {
    expect(renderer(["diff"]).bundle.modes).toEqual(["diff"]);
    expect("modes" in renderer().bundle).toBe(false);
  });

  it("answers an unlisted view with the file's text, without calling render", async () => {
    serve("{ }");
    const { bundle, calls } = renderer(["diff"]);
    const el = container();
    bundle.mount(asElement(el), { mode: "view", blobs: head() });
    await settle();
    expect(calls).toEqual([]);
    expect(el.allText()).toBe("{ }");
  });

  it("still calls render for a listed view, and for every mode when none are listed", () => {
    const listed = renderer(["view", "diff"]);
    listed.bundle.mount(asElement(container()), { mode: "view" });
    expect(listed.calls).toEqual(["render:view"]);
    const all = renderer();
    all.bundle.mount(asElement(container()), { mode: "view" });
    expect(all.calls).toEqual(["render:view"]);
  });

  it("still calls render for an unlisted merge: there is a diff to draw", () => {
    const { bundle, calls } = renderer(["diff"]);
    bundle.mount(asElement(container()), { mode: "merge" });
    expect(calls).toEqual(["render:merge"]);
  });

  it("switches between the floor and render on update, never patching across them", async () => {
    serve("text");
    const { bundle, calls } = renderer(["diff"]);
    const el = container();
    const inst = bundle.mount(asElement(el), { mode: "diff" });
    inst.update({ mode: "view", blobs: head() });
    await settle();
    expect(el.allText()).toBe("text");
    inst.update({ mode: "diff" });
    // diff → view tore down render's pass; view → diff redrew rather than
    // asking the hook to patch DOM it never built.
    expect(calls).toEqual(["render:diff", "cleanup", "render:diff"]);
  });
});
