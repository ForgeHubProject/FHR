import { describe, it, expect, vi, afterEach } from "vitest";
import bundle from "./index.js";
import { drawCells, markdownToDom, renderOutput, safeHref } from "./notebook.js";

// Just enough DOM: elements record tag, class, attributes, children and text;
// appended strings become text nodes. Nothing here can parse HTML, so a test
// that finds an <img onerror> or <script> element would mean we created one.
type Node = { tag: string; className: string; text: string; attrs: Record<string, string>; children: Node[]; style: Record<string, string>; listeners: Record<string, (() => void)[]> };
function makeDoc() {
  const createElement = (tag: string) => {
    const el = {
      tag, className: "", attrs: {} as Record<string, string>, children: [] as Node[], style: {} as Record<string, string>, listeners: {} as Record<string, (() => void)[]>,
      get text() { return el._text; }, _text: "",
      set textContent(v: string) { el._text = v; el.children = []; },
      get textContent(): string { return el._text + el.children.map((c) => (c as unknown as { textContent: string }).textContent).join(""); },
      setAttribute(k: string, v: string) { el.attrs[k] = v; },
      getAttribute(k: string) { return el.attrs[k] ?? null; },
      appendChild(c: Node) { el.children.push(c); return c; },
      append(...cs: (Node | string)[]) { for (const c of cs) el.children.push(typeof c === "string" ? textNode(c) : c); },
      remove() { /* detached */ },
      addEventListener(t: string, f: () => void) { (el.listeners[t] ??= []).push(f); },
    };
    return el;
  };
  const textNode = (t: string) => ({ tag: "#text", className: "", attrs: {}, children: [], style: {}, listeners: {}, textContent: t, text: t }) as unknown as Node;
  return { createElement };
}
const doc = makeDoc();
const make = (tag: string, cls?: string, text?: string) => {
  const e = doc.createElement(tag) as unknown as HTMLElement;
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
};
const all = (n: Node): Node[] => [n, ...n.children.flatMap(all)];
const tags = (n: HTMLElement) => all(n as unknown as Node).map((x) => x.tag);
const textOf = (n: HTMLElement) => (n as unknown as { textContent: string }).textContent;

afterEach(() => vi.unstubAllGlobals());

describe("ipynb renderer modes", () => {
  it("draws a single notebook as well as diffs", () => {
    expect(bundle.modes).toEqual(["view", "diff"]);
  });
});

describe("markdownToDom", () => {
  it("draws headings, paragraphs, lists, quotes, code and inline styles", () => {
    const md = markdownToDom(make, "# Monthly sales\nA **first** look at the *numbers*, with `code`.\n\n- one\n- two\n\n1. first\n\n> quoted\n\n```py\nx = 1\n```\n---");
    expect(tags(md)).toEqual(expect.arrayContaining(["h1", "p", "strong", "em", "code", "ul", "li", "ol", "pre", "hr"]));
    expect(textOf(md)).toContain("Monthly sales");
    expect(textOf(md)).toContain("x = 1");
  });

  it("never creates HTML from a cell: tags in Markdown stay text", () => {
    const md = markdownToDom(make, 'Hi <img src=x onerror="alert(1)"> and <script>alert(2)</script>');
    expect(tags(md)).not.toContain("img");
    expect(tags(md)).not.toContain("script");
    expect(textOf(md)).toContain('<img src=x onerror="alert(1)">');
  });

  it("links only http(s) and mailto; anything else stays text", () => {
    const md = markdownToDom(make, "[ok](https://example.com) [bad](javascript:alert(1)) [mail](mailto:a@b.c)");
    const links = all(md as unknown as Node).filter((n) => n.tag === "a");
    expect(links.map((a) => a.attrs["href"])).toEqual(["https://example.com", "mailto:a@b.c"]);
    expect(links[0]!.attrs["rel"]).toContain("noopener");
    expect(textOf(md)).toContain("bad");
    expect(safeHref(" JavaScript:alert(1)")).toBeNull();
  });

  it("an image shows only from the cell's own attachments, never fetched from elsewhere", () => {
    const att = (ref: string) => (ref === "attachment:plot.png" ? "data:image/png;base64,AAA" : null);
    const md = markdownToDom(make, "![plot](attachment:plot.png) ![remote](https://tracker.example/p.gif)", att);
    const imgs = all(md as unknown as Node).filter((n) => n.tag === "img");
    expect(imgs.map((i) => i.attrs["src"])).toEqual(["data:image/png;base64,AAA"]);
    expect(textOf(md)).toContain("remote");
  });
});

describe("renderOutput", () => {
  it("stream and text/plain as preformatted text; stderr marked", () => {
    expect(textOf(renderOutput(make, { output_type: "stream", name: "stdout", text: ["1650\n"] }))).toBe("1650\n");
    const err = renderOutput(make, { output_type: "stream", name: "stderr", text: "warn" });
    expect(all(err as unknown as Node).some((n) => n.className.includes("fhr-nbv__stderr"))).toBe(true);
    expect(textOf(renderOutput(make, { output_type: "execute_result", data: { "text/plain": ["<Axes>"] } }))).toBe("<Axes>");
  });

  it("a plot as an image from the notebook's own data, preferred over its text", () => {
    const out = renderOutput(make, { output_type: "display_data", data: { "image/png": "iVBO\nRw0=", "text/plain": "<Figure>" } });
    const img = all(out as unknown as Node).find((n) => n.tag === "img");
    expect(img?.attrs["src"]).toBe("data:image/png;base64,iVBORw0=");
  });

  it("SVG through an <img>, where it cannot run scripts", () => {
    const out = renderOutput(make, { output_type: "display_data", data: { "image/svg+xml": "<svg><script>alert(1)</script></svg>" } });
    const img = all(out as unknown as Node).find((n) => n.tag === "img");
    expect(img?.attrs["src"]).toMatch(/^data:image\/svg\+xml;base64,/);
    expect(tags(out)).not.toContain("svg");
  });

  it("HTML (a pandas table) in a sandboxed frame that allows no scripts", () => {
    const out = renderOutput(make, { output_type: "execute_result", data: { "text/html": "<table><tr><td>1</td></tr></table><script>alert(1)</script>" } });
    const frame = all(out as unknown as Node).find((n) => n.tag === "iframe");
    expect(frame?.attrs["sandbox"]).toBe("allow-same-origin");
    expect(frame?.attrs["sandbox"]).not.toContain("allow-scripts");
    expect(frame?.attrs["srcdoc"]).toContain("<table>");
  });

  it("an error as its traceback without ANSI colour codes", () => {
    const out = renderOutput(make, { output_type: "error", ename: "NameError", evalue: "x", traceback: ["\x1b[0;31mNameError\x1b[0m: name 'totl' is not defined"] });
    expect(textOf(out)).toBe("NameError: name 'totl' is not defined");
  });
});

describe("drawCells", () => {
  it("draws cells in order with In/Out prompts", () => {
    const root = make("div");
    drawCells(make, root, [
      { cell_type: "markdown", source: ["# Title\n", "Intro"] },
      { cell_type: "code", execution_count: 3, source: "total", outputs: [{ output_type: "execute_result", execution_count: 3, data: { "text/plain": "1650" } }] },
      { cell_type: "code", execution_count: null, source: "pending()", outputs: [] },
    ]);
    const prompts = all(root as unknown as Node).filter((n) => n.className === "fhr-nbv__prompt").map((n) => (n as unknown as { textContent: string }).textContent);
    expect(prompts).toEqual(["", "In [3]:", "Out [3]:", "In [ ]:"]);
    expect(textOf(root)).toContain("Title");
    expect(textOf(root)).toContain("1650");
  });

  it("says so for a notebook without cells", () => {
    const root = make("div");
    drawCells(make, root, []);
    expect(textOf(root)).toBe("This notebook has no cells.");
  });
});
