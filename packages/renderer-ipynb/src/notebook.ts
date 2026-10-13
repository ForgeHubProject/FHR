import type { MountProps } from "@fhr/types";

// A single notebook drawn as a notebook (FHR#112): Markdown cells as text,
// code cells with their prompts and outputs, plots as images. A notebook is
// untrusted input running in the host's page, so nothing here ever inserts
// HTML: Markdown becomes DOM nodes holding text, images come only from the
// notebook's own data, and an HTML output is shown in a sandboxed frame where
// no script runs.

type El = HTMLElement;
type Make = (tag: string, cls?: string, text?: string) => El;

type Cell = {
  cell_type?: string;
  source?: string | string[];
  execution_count?: number | null;
  outputs?: Output[];
  attachments?: Record<string, Record<string, string | string[]>>;
};
type Output = {
  output_type?: string;
  name?: string;
  text?: string | string[];
  data?: Record<string, unknown>;
  execution_count?: number | null;
  ename?: string;
  evalue?: string;
  traceback?: string[];
};

/** nbformat's "multiline string": a string or an array of lines. */
export function joinText(v: unknown): string {
  if (Array.isArray(v)) return v.join("");
  return typeof v === "string" ? v : "";
}

const ANSI = /\x1b\[[0-9;]*[A-Za-z]/g;

const IMAGE_TYPES = ["image/png", "image/jpeg", "image/gif"];

// ── Markdown, as DOM ─────────────────────────────────────────────────────────

/** Only these link targets become links; anything else stays text. */
export function safeHref(url: string): string | null {
  return /^(https?:\/\/|mailto:)/i.test(url.trim()) ? url.trim() : null;
}

/**
 * Inline Markdown (code, bold, italic, links) appended to `parent` as text
 * nodes and elements: a span of `<b onclick=…>` in a cell is just text.
 */
export function appendInline(make: Make, parent: El, text: string, images: (name: string) => string | null): void {
  // Order matters: code spans first (nothing inside them is Markdown).
  // A URL may hold one level of balanced parentheses (Wikipedia's "Foo_(bar)",
  // or "javascript:alert('z')" which must not leave a stray ")" behind).
  const url = String.raw`((?:[^()\s]|\([^()\s]*\))+)`;
  const re = new RegExp(
    String.raw`\`([^\`]+)\`|!\[([^\]]*)\]\(` + url + String.raw`(?:\s+"[^"]*")?\)|\[([^\]]+)\]\(` + url + String.raw`(?:\s+"[^"]*")?\)|\*\*([^*]+)\*\*|__([^_]+)__|\*([^*\s][^*]*)\*|_([^_\s][^_]*)_`,
    "g",
  );
  let at = 0;
  for (const m of text.matchAll(re)) {
    if (m.index! > at) parent.append(text.slice(at, m.index));
    if (m[1] !== undefined) parent.appendChild(make("code", "fhr-nbv__icode", m[1]));
    else if (m[3] !== undefined) {
      // An image: the notebook's own attachment, else just its alt text.
      const src = images(m[3]);
      if (src) {
        const img = make("img", "fhr-nbv__img");
        img.setAttribute("src", src);
        img.setAttribute("alt", m[2] ?? "");
        parent.appendChild(img);
      } else parent.append(m[2] || m[3]);
    } else if (m[5] !== undefined) {
      const href = safeHref(m[5]);
      if (href) {
        const a = make("a", undefined, m[4]);
        a.setAttribute("href", href);
        a.setAttribute("target", "_blank");
        a.setAttribute("rel", "noopener noreferrer nofollow");
        parent.appendChild(a);
      } else parent.append(m[4] ?? "");
    } else if (m[6] !== undefined || m[7] !== undefined) parent.appendChild(make("strong", undefined, m[6] ?? m[7]));
    else parent.appendChild(make("em", undefined, m[8] ?? m[9]));
    at = m.index! + m[0].length;
  }
  if (at < text.length) parent.append(text.slice(at));
}

/**
 * Block Markdown (headings, paragraphs, lists, quotes, fenced code, rules) as
 * DOM. A practical subset of what notebooks hold; anything else reads as text.
 */
export function markdownToDom(make: Make, source: string, images: (name: string) => string | null = () => null): El {
  const root = make("div", "fhr-nbv__md");
  const lines = source.replace(/\r\n?/g, "\n").split("\n");
  let i = 0;
  const para: string[] = [];
  const flush = () => {
    if (!para.length) return;
    const p = make("p");
    appendInline(make, p, para.join(" "), images);
    root.appendChild(p);
    para.length = 0;
  };
  while (i < lines.length) {
    const line = lines[i]!;
    const fence = /^\s*(```|~~~)/.exec(line);
    if (fence) {
      flush();
      const body: string[] = [];
      i++;
      while (i < lines.length && !lines[i]!.trimStart().startsWith(fence[1]!)) body.push(lines[i++]!);
      i++;
      const pre = make("pre", "fhr-nbv__pre");
      pre.appendChild(make("code", undefined, body.join("\n")));
      root.appendChild(pre);
      continue;
    }
    const h = /^(#{1,6})\s+(.*?)\s*#*\s*$/.exec(line);
    if (h) {
      flush();
      const el = make(`h${h[1]!.length}`, "fhr-nbv__h");
      appendInline(make, el, h[2]!, images);
      root.appendChild(el);
      i++;
      continue;
    }
    if (/^\s*([-*_])(\s*\1){2,}\s*$/.test(line)) {
      flush();
      root.appendChild(make("hr"));
      i++;
      continue;
    }
    if (/^\s*>/.test(line)) {
      flush();
      const quote: string[] = [];
      while (i < lines.length && /^\s*>/.test(lines[i]!)) quote.push(lines[i++]!.replace(/^\s*>\s?/, ""));
      const bq = markdownToDom(make, quote.join("\n"), images);
      bq.className = "fhr-nbv__quote";
      root.appendChild(bq);
      continue;
    }
    const item = /^\s*([-*+]|\d+[.)])\s+(.*)$/.exec(line);
    if (item) {
      flush();
      const ordered = /\d/.test(item[1]!);
      const list = make(ordered ? "ol" : "ul", "fhr-nbv__list");
      while (i < lines.length) {
        const m = /^\s*([-*+]|\d+[.)])\s+(.*)$/.exec(lines[i]!);
        if (!m || /\d/.test(m[1]!) !== ordered) break;
        const li = make("li");
        appendInline(make, li, m[2]!, images);
        list.appendChild(li);
        i++;
      }
      root.appendChild(list);
      continue;
    }
    if (line.trim() === "") flush();
    else para.push(line.trim());
    i++;
  }
  flush();
  return root;
}

// ── outputs ──────────────────────────────────────────────────────────────────

/** Base64 of a UTF-8 string, for an SVG output's data: URL. */
function utf8Base64(s: string): string {
  const bytes = new TextEncoder().encode(s);
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin);
}

/** One output, drawn from the richest representation it carries. */
export function renderOutput(make: Make, out: Output): El {
  const box = make("div", "fhr-nbv__output");
  const pre = (text: string, cls = "fhr-nbv__pre") => {
    const p = make("pre", cls);
    p.textContent = text;
    return p;
  };
  switch (out.output_type) {
    case "stream":
      box.appendChild(pre(joinText(out.text), out.name === "stderr" ? "fhr-nbv__pre fhr-nbv__stderr" : "fhr-nbv__pre"));
      return box;
    case "error": {
      const tb = (out.traceback ?? []).join("\n").replace(ANSI, "");
      box.appendChild(pre(tb || `${out.ename ?? "Error"}: ${out.evalue ?? ""}`, "fhr-nbv__pre fhr-nbv__error"));
      return box;
    }
    default: {
      const data = out.data ?? {};
      const image = IMAGE_TYPES.find((t) => typeof data[t] === "string" || Array.isArray(data[t]));
      if (image) {
        const img = make("img", "fhr-nbv__img");
        img.setAttribute("src", `data:${image};base64,${joinText(data[image]).replace(/\s+/g, "")}`);
        img.setAttribute("alt", "");
        box.appendChild(img);
        return box;
      }
      if (data["image/svg+xml"] !== undefined) {
        // An SVG drawn as an <img> cannot run scripts or reach the page.
        const img = make("img", "fhr-nbv__img");
        img.setAttribute("src", `data:image/svg+xml;base64,${utf8Base64(joinText(data["image/svg+xml"]))}`);
        img.setAttribute("alt", "");
        box.appendChild(img);
        return box;
      }
      if (data["text/html"] !== undefined) {
        // A sandboxed frame: no scripts, no forms, no popups, no navigation of
        // the page. Same-origin only so the frame can be sized to its content.
        const frame = make("iframe", "fhr-nbv__html");
        frame.setAttribute("sandbox", "allow-same-origin");
        frame.setAttribute("srcdoc", `<!doctype html><meta charset="utf-8"><style>body{margin:0;font:13px system-ui,sans-serif}table{border-collapse:collapse}th,td{border:1px solid #d1d9e0;padding:2px 6px}</style>${joinText(data["text/html"])}`);
        frame.setAttribute("title", "HTML output");
        frame.addEventListener("load", () => {
          const doc = (frame as HTMLIFrameElement).contentDocument;
          if (doc?.body) frame.style.height = `${Math.min(doc.body.scrollHeight + 4, 600)}px`;
        });
        box.appendChild(frame);
        return box;
      }
      if (data["text/markdown"] !== undefined) {
        box.appendChild(markdownToDom(make, joinText(data["text/markdown"])));
        return box;
      }
      if (data["application/json"] !== undefined) {
        box.appendChild(pre(JSON.stringify(data["application/json"], null, 2)));
        return box;
      }
      box.appendChild(pre(joinText(data["text/plain"])));
      return box;
    }
  }
}

// ── the notebook ─────────────────────────────────────────────────────────────

// The host page may reset element styles (Tailwind's preflight strips heading
// sizes, list markers and paragraph margins), so the Markdown's look is set
// here rather than left to the browser's defaults.
const CSS = `
.fhr-nbv { font: 14px/1.6 ui-sans-serif, system-ui, sans-serif; color: #1f2328; padding: 8px 16px 16px; }
.fhr-nbv[data-theme="dark"] { color: #e6edf3; }
.fhr-nbv__cell { display: grid; grid-template-columns: 5.5em minmax(0, 1fr); gap: 0 8px; margin: 10px 0; }
.fhr-nbv__prompt { font: 12px/1.6 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; color: #59636e; text-align: right; padding-top: 7px; white-space: nowrap; }
.fhr-nbv[data-theme="dark"] .fhr-nbv__prompt { color: #9198a1; }
.fhr-nbv__source { border: 1px solid #d1d9e0; border-radius: 6px; background: #f6f8fa; overflow: auto; }
.fhr-nbv[data-theme="dark"] .fhr-nbv__source { border-color: #3d444d; background: #151b23; }
.fhr-nbv__pre { margin: 0; padding: 6px 10px; font: 12.5px/1.5 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; white-space: pre-wrap; word-break: break-word; }
.fhr-nbv__output { overflow: auto; }
.fhr-nbv__stderr { background: rgba(212,167,44,0.12); }
.fhr-nbv__error { background: rgba(248,81,73,0.12); }
.fhr-nbv__img { max-width: 100%; height: auto; background: #fff; }
.fhr-nbv__html { width: 100%; height: 160px; border: 0; background: #fff; }
.fhr-nbv__md p, .fhr-nbv__md .fhr-nbv__list, .fhr-nbv__md .fhr-nbv__pre, .fhr-nbv__md .fhr-nbv__quote { margin: 0 0 12px; }
.fhr-nbv__h { margin: 20px 0 10px; font-weight: 600; line-height: 1.25; }
h1.fhr-nbv__h { font-size: 2em; }
h2.fhr-nbv__h { font-size: 1.5em; }
h3.fhr-nbv__h { font-size: 1.25em; }
h4.fhr-nbv__h { font-size: 1em; }
h5.fhr-nbv__h { font-size: .875em; }
h6.fhr-nbv__h { font-size: .85em; color: #59636e; }
.fhr-nbv[data-theme="dark"] h6.fhr-nbv__h { color: #9198a1; }
.fhr-nbv__md h1, .fhr-nbv__md h2 { border-bottom: 1px solid #d1d9e0; padding-bottom: .3em; }
.fhr-nbv[data-theme="dark"] .fhr-nbv__md h1, .fhr-nbv[data-theme="dark"] .fhr-nbv__md h2 { border-color: #3d444d; }
.fhr-nbv__list { padding-left: 2em; }
ul.fhr-nbv__list { list-style: disc; }
ol.fhr-nbv__list { list-style: decimal; }
.fhr-nbv__list li + li { margin-top: .25em; }
.fhr-nbv__md hr { height: 0; border: 0; border-top: 2px solid #d1d9e0; margin: 20px 0; }
.fhr-nbv[data-theme="dark"] .fhr-nbv__md hr { border-color: #3d444d; }
.fhr-nbv__md > :first-child { margin-top: 0; }
.fhr-nbv__md > :last-child { margin-bottom: 0; }
.fhr-nbv__icode { font: 85% ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; background: rgba(129,139,152,0.15); padding: .1em .35em; border-radius: 4px; }
.fhr-nbv__quote { border-left: 3px solid #d1d9e0; padding-left: 12px; color: #59636e; }
.fhr-nbv a { color: #0969da; }
.fhr-nbv[data-theme="dark"] a { color: #4493f8; }
.fhr-nbv__note { color: #59636e; padding: 12px 0; }
`;

/** Draw a notebook file into `container`: fetched from the head blob, then cell by cell. */
export function renderNotebookView(container: HTMLElement, props: MountProps): () => void {
  const doc = container.ownerDocument;
  const make: Make = (tag, cls, text) => {
    const e = doc.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text;
    return e;
  };
  const root = make("div", "fhr-nbv");
  root.setAttribute("data-theme", props.theme === "dark" ? "dark" : "light");
  const style = make("style");
  style.textContent = CSS;
  root.appendChild(style);
  container.appendChild(root);

  const url = props.blobs?.head?.url;
  if (!url) {
    root.appendChild(make("div", "fhr-nbv__note", "No file provided."));
    return () => {};
  }
  let live = true;
  const loading = make("div", "fhr-nbv__note", "Loading the notebook…");
  root.appendChild(loading);
  fetch(url)
    .then((res) => {
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      return res.text();
    })
    .then((text) => {
      if (!live) return;
      loading.remove();
      let nb: { cells?: Cell[] };
      try {
        nb = JSON.parse(text) as { cells?: Cell[] };
      } catch {
        root.appendChild(make("div", "fhr-nbv__note", "This file isn't valid notebook JSON."));
        return;
      }
      drawCells(make, root, Array.isArray(nb.cells) ? nb.cells : []);
    })
    .catch((err: unknown) => {
      if (!live) return;
      loading.textContent = `Could not load this notebook (${err instanceof Error ? err.message : String(err)}).`;
    });
  return () => {
    live = false;
  };
}

/** The cells, in order. Exported for tests. */
export function drawCells(make: Make, root: El, cells: Cell[]): void {
  if (cells.length === 0) {
    root.appendChild(make("div", "fhr-nbv__note", "This notebook has no cells."));
    return;
  }
  for (const cell of cells) {
    const source = joinText(cell.source);
    if (cell.cell_type === "markdown") {
      const row = make("div", "fhr-nbv__cell fhr-nbv__cell--markdown");
      row.appendChild(make("div", "fhr-nbv__prompt"));
      const images = (ref: string): string | null => {
        // Markdown images resolve only to the cell's own attachments.
        const name = ref.startsWith("attachment:") ? ref.slice("attachment:".length) : null;
        const att = name ? cell.attachments?.[name] : undefined;
        const type = att && IMAGE_TYPES.find((t) => att[t] !== undefined);
        return att && type ? `data:${type};base64,${joinText(att[type]).replace(/\s+/g, "")}` : null;
      };
      row.appendChild(markdownToDom(make, source, images));
      root.appendChild(row);
      continue;
    }
    const n = cell.execution_count;
    const row = make("div", `fhr-nbv__cell fhr-nbv__cell--${cell.cell_type === "code" ? "code" : "raw"}`);
    row.appendChild(make("div", "fhr-nbv__prompt", cell.cell_type === "code" ? `In [${n ?? " "}]:` : ""));
    const src = make("div", "fhr-nbv__source");
    src.appendChild(make("pre", "fhr-nbv__pre", source));
    row.appendChild(src);
    root.appendChild(row);
    for (const out of cell.outputs ?? []) {
      const orow = make("div", "fhr-nbv__cell fhr-nbv__cell--output");
      const isResult = out.output_type === "execute_result";
      orow.appendChild(make("div", "fhr-nbv__prompt", isResult ? `Out [${out.execution_count ?? n ?? " "}]:` : ""));
      orow.appendChild(renderOutput(make, out));
      root.appendChild(orow);
    }
  }
}
