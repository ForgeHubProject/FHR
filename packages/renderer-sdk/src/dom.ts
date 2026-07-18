import type { MountProps } from "@fhr/types";
import { flattenDiff, diffSummary, formatValue } from "./diff.js";

const KIND_SYMBOL: Record<string, string> = { added: "+", removed: "−", modified: "~" };

// Self-contained styles — a renderer bundle cannot rely on host CSS. Injected
// once per container, scoped under .fhr-diff, theme-aware via [data-theme].
const STYLE = `
.fhr-diff { font: 13px/1.55 ui-sans-serif, system-ui, sans-serif; color: #1f2328; max-width: 60rem; }
.fhr-diff[data-theme="dark"] { color: #e6edf3; }
.fhr-diff__summary { display: flex; gap: 8px; align-items: center; padding: 2px 0 12px; flex-wrap: wrap; }
.fhr-diff__count { font-weight: 600; font-size: 12px; padding: 2px 9px; border-radius: 999px; white-space: nowrap; }
.fhr-diff__count--added { color: #1a7f37; background: rgba(31,136,61,0.12); }
.fhr-diff__count--removed { color: #cf222e; background: rgba(207,34,46,0.12); }
.fhr-diff__count--modified { color: #9a6700; background: rgba(154,103,0,0.14); }
.fhr-diff[data-theme="dark"] .fhr-diff__count--added { color: #3fb950; background: rgba(63,185,80,0.16); }
.fhr-diff[data-theme="dark"] .fhr-diff__count--removed { color: #f85149; background: rgba(248,81,73,0.16); }
.fhr-diff[data-theme="dark"] .fhr-diff__count--modified { color: #d29922; background: rgba(210,153,34,0.16); }
.fhr-diff__rows { list-style: none; margin: 0; padding: 0; }
/* Rows read as an indented tree — hover highlight and rounded corners instead
   of full-width rules, which look like a broken table in a wide panel. */
.fhr-diff__row { display: flex; gap: 8px; align-items: baseline; padding: 3px 8px; border-radius: 6px; cursor: default; }
.fhr-diff__row:hover { background: rgba(130,130,130,0.10); }
.fhr-diff__row[data-selectable="1"] { cursor: pointer; }
/* A faint guide connects a group header to its indented children. */
.fhr-diff__row[data-depth="0"] { margin-top: 2px; }
.fhr-diff__mark { flex: none; width: 1em; text-align: center; font-weight: 700; }
.fhr-diff__mark--added { color: #1a7f37; }
.fhr-diff__mark--removed { color: #cf222e; }
.fhr-diff__mark--modified { color: #9a6700; }
.fhr-diff[data-theme="dark"] .fhr-diff__mark--added { color: #3fb950; }
.fhr-diff[data-theme="dark"] .fhr-diff__mark--removed { color: #f85149; }
.fhr-diff[data-theme="dark"] .fhr-diff__mark--modified { color: #d29922; }
.fhr-diff__label { flex: none; font-weight: 500; }
.fhr-diff__values { color: #57606a; font-family: ui-monospace, monospace; font-size: 12px; }
.fhr-diff[data-theme="dark"] .fhr-diff__values { color: #8b949e; }
.fhr-diff__arrow { opacity: 0.55; padding: 0 6px; }
.fhr-diff__empty { padding: 16px 4px; color: #57606a; }
.fhr-diff__note { padding: 8px 4px; color: #57606a; font-style: italic; }
`;

/**
 * Render a StructuredDiff as a self-contained change tree in `container`.
 * This is the "lite" DOM view — the default that works on any client without
 * a 3D viewport. Clicking a row emits a `select` event via props.onEvent.
 */
export function renderDiffTree(container: HTMLElement, props: MountProps): void {
  const doc = container.ownerDocument;
  const root = doc.createElement("div");
  root.className = "fhr-diff";
  root.setAttribute("data-theme", props.theme ?? "light");

  const style = doc.createElement("style");
  style.textContent = STYLE;
  root.appendChild(style);

  const diff = props.diff;
  // changes may be null over the wire (a nil Go slice marshals to JSON null) —
  // treat that as "no changes" rather than dereferencing null.length.
  if (!diff || !diff.changes || diff.changes.length === 0) {
    const empty = doc.createElement("div");
    empty.className = "fhr-diff__empty";
    empty.textContent = diff ? "No changes." : "No diff provided.";
    root.appendChild(empty);
    container.appendChild(root);
    return;
  }

  const s = diffSummary(diff);
  const summary = doc.createElement("div");
  summary.className = "fhr-diff__summary";
  for (const [kind, n] of [
    ["added", s.added],
    ["removed", s.removed],
    ["modified", s.modified],
  ] as const) {
    if (n === 0) continue;
    const span = doc.createElement("span");
    span.className = `fhr-diff__count fhr-diff__count--${kind}`;
    span.textContent = `${KIND_SYMBOL[kind]} ${n} ${kind}`;
    summary.appendChild(span);
  }
  root.appendChild(summary);

  if (props.mode === "merge") {
    const note = doc.createElement("div");
    note.className = "fhr-diff__note";
    note.textContent = "Interactive merge resolution is not yet available in this renderer.";
    root.appendChild(note);
  }

  const list = doc.createElement("ul");
  list.className = "fhr-diff__rows";
  for (const row of flattenDiff(diff)) {
    const li = doc.createElement("li");
    li.className = "fhr-diff__row";
    li.setAttribute("data-depth", String(row.depth));
    li.style.paddingLeft = `${8 + row.depth * 18}px`;

    const mark = doc.createElement("span");
    mark.className = `fhr-diff__mark fhr-diff__mark--${row.kind}`;
    mark.textContent = KIND_SYMBOL[row.kind] ?? "•";
    li.appendChild(mark);

    const label = doc.createElement("span");
    label.className = "fhr-diff__label";
    label.textContent = row.label;
    li.appendChild(label);

    if (row.kind === "modified" && (row.before !== undefined || row.after !== undefined)) {
      const values = doc.createElement("span");
      values.className = "fhr-diff__values";
      values.append(
        formatValue(row.before),
        Object.assign(doc.createElement("span"), {
          className: "fhr-diff__arrow",
          textContent: "→",
        }),
        formatValue(row.after),
      );
      li.appendChild(values);
    }

    if (props.onEvent) {
      li.setAttribute("data-selectable", "1");
      li.addEventListener("click", () => props.onEvent?.({ type: "select", changePath: row.path }));
    }
    list.appendChild(li);
  }
  root.appendChild(list);
  container.appendChild(root);
}
