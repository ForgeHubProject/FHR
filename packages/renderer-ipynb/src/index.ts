import { defineRenderer } from "@fhr/renderer-sdk";
import type { DiffChange, MountProps } from "@fhr/types";
import { foldUnchanged, lineDiff, type DiffLine } from "./linediff.js";
import { renderNotebookView } from "./notebook.js";

// Replaced at bundle-build time with the release's short commit SHA (see
// build.mjs). Guarded with typeof so importing the source directly (e.g. in a
// unit test, where the define isn't applied) doesn't throw.
declare const __BUILD__: string;
const BUILD = typeof __BUILD__ !== "undefined" ? __BUILD__ : "dev";

/**
 * Renderer for Jupyter notebooks. A single notebook is drawn as one (cells,
 * prompts, outputs; notebook.ts). In a diff each changed cell is a card, and
 * inside it the cell's source and outputs are line-diffed like a small text
 * file: which cells were added, removed, moved or edited, and what changed.
 */
export default defineRenderer({
  handlerId: "ipynb",
  extensions: [".ipynb"],
  build: BUILD,
  // A single notebook is drawn as a notebook (FHR#112); a diff, cell by cell.
  modes: ["view", "diff"],
  render(container: HTMLElement, props: MountProps) {
    if (props.mode === "view") return renderNotebookView(container, props);
    return renderNotebookDiff(container, props);
  },
});

const CSS = `
.fhr-nb { font: 13px/1.5 ui-sans-serif, system-ui, sans-serif; color: #1f2328; }
.fhr-nb[data-theme="dark"] { color: #e6edf3; }
.fhr-nb__summary { padding: 8px 12px; color: #59636e; }
.fhr-nb[data-theme="dark"] .fhr-nb__summary { color: #9198a1; }
.fhr-nb__empty { padding: 12px; color: #59636e; }
.fhr-nb__cell { margin: 8px 12px 12px; border: 1px solid #d1d9e0; border-radius: 6px; overflow: hidden; }
.fhr-nb[data-theme="dark"] .fhr-nb__cell { border-color: #3d444d; }
.fhr-nb__cell[aria-selected="true"] { outline: 2px solid #0969da; }
.fhr-nb__head { display: flex; gap: 8px; align-items: center; padding: 6px 10px; background: #f6f8fa; cursor: pointer; }
.fhr-nb[data-theme="dark"] .fhr-nb__head { background: #151b23; }
.fhr-nb__badge { font-size: 11px; font-weight: 600; padding: 0 6px; border-radius: 10px; border: 1px solid currentColor; }
.fhr-nb__badge--added { color: #1a7f37; } .fhr-nb__badge--removed { color: #d1242f; }
.fhr-nb__badge--modified { color: #9a6700; } .fhr-nb__badge--moved { color: #8250df; }
.fhr-nb[data-theme="dark"] .fhr-nb__badge--added { color: #3fb950; }
.fhr-nb[data-theme="dark"] .fhr-nb__badge--removed { color: #f85149; }
.fhr-nb[data-theme="dark"] .fhr-nb__badge--modified { color: #d29922; }
.fhr-nb[data-theme="dark"] .fhr-nb__badge--moved { color: #a371f7; }
.fhr-nb__title { font-weight: 600; }
.fhr-nb__note { color: #59636e; }
.fhr-nb[data-theme="dark"] .fhr-nb__note { color: #9198a1; }
.fhr-nb__part { padding: 4px 10px 0; font-size: 11px; font-weight: 600; text-transform: uppercase; color: #59636e; }
.fhr-nb__lines { width: 100%; border-collapse: collapse; font: 12px/1.5 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }
.fhr-nb__lines td { padding: 0 8px; white-space: pre-wrap; word-break: break-all; vertical-align: top; }
.fhr-nb__lines td.fhr-nb__no { width: 1%; min-width: 2.5em; text-align: right; color: #59636e; user-select: none; white-space: nowrap; }
.fhr-nb__line--add { background: #e6ffec; } .fhr-nb__line--del { background: #ffebe9; }
.fhr-nb[data-theme="dark"] .fhr-nb__line--add { background: rgba(46,160,67,0.15); }
.fhr-nb[data-theme="dark"] .fhr-nb__line--del { background: rgba(248,81,73,0.15); }
.fhr-nb__line--fold td { color: #59636e; background: #f6f8fa; font-style: italic; }
.fhr-nb[data-theme="dark"] .fhr-nb__line--fold td { background: #151b23; }
`;

const SIGN: Record<DiffLine["kind"], string> = { same: " ", add: "+", del: "-", fold: "" };

function asText(v: unknown): string {
  if (v === undefined || v === null) return "";
  return typeof v === "string" ? v : JSON.stringify(v);
}

function child(ch: DiffChange, path: string): DiffChange | undefined {
  return ch.children?.find((c) => c.path === path);
}

/** "code cell 3" → "Code cell 3". */
function title(label: string): string {
  return label.charAt(0).toUpperCase() + label.slice(1);
}

/** The badge a cell gets: a cell whose only change is its position reads as moved. */
function badge(ch: DiffChange): "added" | "removed" | "modified" | "moved" {
  if (ch.kind === "added" || ch.kind === "removed") return ch.kind;
  const kids = ch.children ?? [];
  return kids.length > 0 && kids.every((k) => k.path === "position") ? "moved" : "modified";
}

/** Draw a notebook diff into `container`: a summary, then one card per changed cell. */
export function renderNotebookDiff(container: HTMLElement, props: MountProps): () => void {
  const doc = container.ownerDocument;
  const el = (tag: string, cls?: string, txt?: string): HTMLElement => {
    const e = doc.createElement(tag);
    if (cls) e.className = cls;
    if (txt !== undefined) e.textContent = txt;
    return e;
  };
  const root = el("div", "fhr-nb");
  root.setAttribute("data-theme", props.theme === "dark" ? "dark" : "light");
  const style = el("style");
  style.textContent = CSS;
  root.appendChild(style);
  container.appendChild(root);

  const changes = props.diff?.changes ?? [];
  if (!props.diff || changes.length === 0) {
    root.appendChild(el("div", "fhr-nb__empty", props.diff ? "No changes." : "No diff provided."));
    return () => {};
  }

  const cells = changes.filter((c) => c.path.startsWith("cells/"));
  const counts = { added: 0, removed: 0, modified: 0, moved: 0 };
  for (const c of cells) counts[badge(c)]++;
  const parts = [`${counts.added} added`, `${counts.removed} removed`, `${counts.modified} changed`];
  if (counts.moved) parts.push(`${counts.moved} moved`);
  root.appendChild(el("div", "fhr-nb__summary", `${cells.length} cell${cells.length === 1 ? "" : "s"}: ${parts.join(" · ")}`));

  const teardown: (() => void)[] = [];
  for (const ch of changes) {
    if (!ch.path.startsWith("cells/")) {
      // Notebook-level rows (the kernel): one line, before → after.
      const card = el("div", "fhr-nb__cell");
      const head = el("div", "fhr-nb__head");
      head.append(el("span", "fhr-nb__badge fhr-nb__badge--modified", "changed"), el("span", "fhr-nb__title", title(ch.label ?? ch.path)));
      head.appendChild(el("span", "fhr-nb__note", `${asText(ch.before)} → ${asText(ch.after)}`));
      card.appendChild(head);
      root.appendChild(card);
      continue;
    }

    const kind = badge(ch);
    const card = el("div", "fhr-nb__cell");
    card.setAttribute("data-path", ch.path);
    card.setAttribute("data-kind", kind);
    card.setAttribute("aria-selected", props.selectedChangePath === ch.path ? "true" : "false");
    const head = el("div", "fhr-nb__head");
    head.append(el("span", `fhr-nb__badge fhr-nb__badge--${kind}`, kind), el("span", "fhr-nb__title", title(ch.label ?? ch.path)));
    const pos = child(ch, "position");
    if (pos) head.appendChild(el("span", "fhr-nb__note", `moved from cell ${asText(pos.before)} to ${asText(pos.after)}`));
    const type = child(ch, "cell_type");
    if (type) head.appendChild(el("span", "fhr-nb__note", `${asText(type.before)} → ${asText(type.after)}`));
    card.appendChild(head);
    if (props.onEvent) {
      const onEvent = props.onEvent;
      const select = (): void => onEvent({ type: "select", changePath: ch.path });
      head.addEventListener("click", select);
      teardown.push(() => head.removeEventListener("click", select));
    }

    if (kind === "moved") card.appendChild(el("div", "fhr-nb__empty", "Content unchanged."));
    for (const part of ["source", "outputs"] as const) {
      const row = child(ch, part);
      if (!row) continue;
      if (part === "outputs") card.appendChild(el("div", "fhr-nb__part", "Outputs"));
      card.appendChild(linesTable(el, foldUnchanged(lineDiff(asText(row.before), asText(row.after)))));
    }
    root.appendChild(card);
  }

  if (props.mode === "merge") {
    root.appendChild(el("div", "fhr-nb__empty", "Interactive merge resolution is not yet available in this renderer."));
  }
  return () => teardown.forEach((off) => off());
}

function linesTable(el: (tag: string, cls?: string, txt?: string) => HTMLElement, lines: DiffLine[]): HTMLElement {
  const table = el("table", "fhr-nb__lines");
  for (const line of lines) {
    const tr = el("tr", `fhr-nb__line fhr-nb__line--${line.kind}`);
    if (line.kind === "fold") {
      const td = el("td", undefined, `⋯ ${line.count} unchanged line${line.count === 1 ? "" : "s"}`);
      td.setAttribute("colspan", "4");
      tr.appendChild(td);
    } else {
      tr.append(
        el("td", "fhr-nb__no", line.kind === "add" ? "" : String(line.oldNo)),
        el("td", "fhr-nb__no", line.kind === "del" ? "" : String(line.newNo)),
        el("td", "fhr-nb__no", SIGN[line.kind]),
        el("td", undefined, line.text),
      );
    }
    table.appendChild(tr);
  }
  return table;
}
