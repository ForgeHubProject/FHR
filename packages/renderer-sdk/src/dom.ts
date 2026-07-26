// The change tree: the lite DOM view every renderer gets for free, and one half
// of the linked review surface (the other half being whatever the renderer draws
// beside it — a 3D scene, a table, nothing).
//
// What makes it a review surface rather than a list:
//   * a summary bar of counts by kind, so the size of the change is the first
//     thing read;
//   * one highlighted row, owned by the caller (`handle.select`) rather than by
//     this module, so a click in a 3D viewport and a click on a row can drive the
//     same selection without fighting each other;
//   * `n`/`p`/↑/↓ stepping over *review stops* (see reviewStops), the
//     next/previous-change grammar Unreal's diff tool established;
//   * the panel formatting rules from format.ts: before → after plus a delta with
//     a magnitude, colours as chips, index churn de-emphasised.
//
// The DOM is built by hand and the stylesheet ships inside the container: a
// renderer bundle cannot rely on host CSS.

import type { MountProps } from "@fhr/types";
import { flattenDiff, countKinds, reviewStops, type DiffRow, type ReviewStop } from "./diff.js";
import { formatChange, type FormattedChange } from "./format.js";

const KIND_SYMBOL: Record<string, string> = { added: "+", removed: "−", modified: "~" };

// Self-contained styles — a renderer bundle cannot rely on host CSS. Injected
// once per container, scoped under .fhr-diff, theme-aware via [data-theme].
const STYLE = `
.fhr-diff { font: 13px/1.55 ui-sans-serif, system-ui, sans-serif; color: #1f2328; max-width: 60rem; }
.fhr-diff[data-theme="dark"] { color: #e6edf3; }
.fhr-diff:focus { outline: none; }
.fhr-diff__summary { display: flex; gap: 8px; align-items: center; padding: 2px 0 12px; flex-wrap: wrap; }
.fhr-diff__count { font-weight: 600; font-size: 12px; padding: 2px 9px; border-radius: 999px; white-space: nowrap; }
.fhr-diff__count--added { color: #1a7f37; background: rgba(31,136,61,0.12); }
.fhr-diff__count--removed { color: #cf222e; background: rgba(207,34,46,0.12); }
.fhr-diff__count--modified { color: #9a6700; background: rgba(154,103,0,0.14); }
.fhr-diff__count--other { color: #57606a; background: rgba(130,130,130,0.14); }
.fhr-diff[data-theme="dark"] .fhr-diff__count--added { color: #3fb950; background: rgba(63,185,80,0.16); }
.fhr-diff[data-theme="dark"] .fhr-diff__count--removed { color: #f85149; background: rgba(248,81,73,0.16); }
.fhr-diff[data-theme="dark"] .fhr-diff__count--modified { color: #d29922; background: rgba(210,153,34,0.16); }
.fhr-diff__nav { display: flex; gap: 4px; align-items: center; margin-left: auto; }
.fhr-diff__step { font: inherit; font-size: 12px; line-height: 1; padding: 4px 8px; border-radius: 6px;
  border: 1px solid #d0d7de; background: transparent; color: inherit; cursor: pointer; }
.fhr-diff[data-theme="dark"] .fhr-diff__step { border-color: #30363d; }
.fhr-diff__position { font-size: 12px; color: #57606a; min-width: 3.5em; text-align: center; }
.fhr-diff__rows { list-style: none; margin: 0; padding: 0; }
/* Rows read as an indented tree — hover highlight and rounded corners instead
   of full-width rules, which look like a broken table in a wide panel. */
.fhr-diff__row { display: flex; gap: 8px; align-items: baseline; padding: 3px 8px; border-radius: 6px; cursor: default; }
.fhr-diff__row:hover { background: rgba(130,130,130,0.10); }
.fhr-diff__row[data-selectable="1"] { cursor: pointer; }
.fhr-diff__row[aria-selected="true"] { background: rgba(31,111,235,0.14); box-shadow: inset 2px 0 0 currentColor; }
/* A faint guide connects a group header to its indented children. */
.fhr-diff__row[data-depth="0"] { margin-top: 2px; }
.fhr-diff__row[data-noise="1"] { opacity: 0.6; }
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
/* The delta column: the number a reviewer actually judges the change by. */
.fhr-diff__delta { margin-left: auto; padding-left: 12px; font-family: ui-monospace, monospace; font-size: 12px;
  font-weight: 600; white-space: nowrap; }
.fhr-diff__swatch { display: inline-block; width: 0.85em; height: 0.85em; border-radius: 3px; margin-right: 4px;
  vertical-align: -0.1em; border: 1px solid rgba(130,130,130,0.55); }
.fhr-diff__empty { padding: 16px 4px; color: #57606a; }
.fhr-diff__note { padding: 8px 4px; color: #57606a; font-style: italic; }
`;

/** What the tree reports back to its owner. Both are viewer actions. */
export type DiffTreeOptions = {
  /** The row to highlight on first render. */
  selectedPath?: string | null;
  /** A row was activated (clicked). The owner decides what selection becomes. */
  onSelect?: (path: string, row: DiffRow) => void;
  /** `n`/`p`/↑/↓ pressed with the tree focused: +1 next, -1 previous. */
  onStep?: (delta: number) => void;
  /** Override the panel formatting for a row (see format.ts for the default). */
  format?: (row: DiffRow) => FormattedChange;
  /** Override which rows the review path stops on (see diff.ts reviewStops). */
  stops?: ReviewStop[];
  /** Set false to leave keyboard handling to the caller. Default: on. */
  keyboard?: boolean;
};

/** Live handle over a rendered change tree: selection without a re-render. */
export type DiffTreeHandle = {
  /** The tree's root element (already appended to the container). */
  root: HTMLElement;
  /** Review-stop paths in display order — what stepping moves through. */
  stops: string[];
  /** The highlighted path, or null. */
  readonly selected: string | null;
  /**
   * Highlight the row for `path` (null clears). Programmatic: it does not call
   * onSelect, so pushing selection in can't echo back out. Returns false when no
   * row matches — a selection key from a host that doesn't exist in this diff.
   */
  select(path: string | null): boolean;
  /** Move focus to the tree, so the keyboard path works without a click. */
  focus(): void;
  /** Remove listeners. The DOM itself goes with the container. */
  dispose(): void;
};

/** Elements a text caret can be in: never steal keys from those. */
function isTextEntry(target: unknown): boolean {
  const el = target as { tagName?: string; isContentEditable?: boolean } | null;
  if (!el) return false;
  if (el.isContentEditable === true) return true;
  const tag = (el.tagName ?? "").toUpperCase();
  return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT";
}

/**
 * Next/previous intent for a keyboard event, or 0 for "not ours".
 *
 * `n`/`p` are Unreal's diff-navigation keys; the arrows are what everyone tries
 * first in a list. Modifier chords belong to the browser, and a key pressed with
 * a caret in a text field belongs to the text field.
 */
export function stepIntent(
  event: { key?: string; target?: unknown; ctrlKey?: boolean; metaKey?: boolean; altKey?: boolean },
  opts: { arrows?: boolean } = {},
): number {
  if (event.ctrlKey === true || event.metaKey === true || event.altKey === true) return 0;
  if (isTextEntry(event.target)) return 0;
  const key = event.key ?? "";
  if (key === "n" || key === "N") return 1;
  if (key === "p" || key === "P") return -1;
  if (opts.arrows !== false) {
    if (key === "ArrowDown") return 1;
    if (key === "ArrowUp") return -1;
  }
  return 0;
}

/**
 * Render a StructuredDiff as a self-contained change tree in `container`.
 *
 * The two-argument form is unchanged from before this slice: clicking a row still
 * emits `select` through `props.onEvent`. Pass `options` to take over selection
 * (highlight a row, step through changes, react to a click) and keep the
 * returned handle to move the highlight without re-rendering anything.
 */
export function renderDiffTree(
  container: HTMLElement,
  props: MountProps,
  options: DiffTreeOptions = {},
): DiffTreeHandle {
  const doc = container.ownerDocument;
  const root = doc.createElement("div");
  root.className = "fhr-diff";
  root.setAttribute("data-theme", props.theme ?? "light");

  const style = doc.createElement("style");
  style.textContent = STYLE;
  root.appendChild(style);

  const teardown: (() => void)[] = [];
  const rowByPath = new Map<string, HTMLElement>();
  let selected: string | null = null;
  // Assigned once the summary bar exists; a no-op until then, and for a diff with
  // nothing in it (where `select` is still callable and must not throw).
  let updatePosition: () => void = () => {};

  const handle: DiffTreeHandle = {
    root,
    stops: [],
    get selected(): string | null {
      return selected;
    },
    select(path: string | null): boolean {
      const previous = selected === null ? undefined : rowByPath.get(selected);
      previous?.setAttribute("aria-selected", "false");
      selected = path;
      if (path === null) {
        updatePosition();
        return true;
      }
      const next = rowByPath.get(path);
      if (!next) {
        updatePosition();
        return false;
      }
      next.setAttribute("aria-selected", "true");
      const scroll = (next as { scrollIntoView?: (arg: unknown) => void }).scrollIntoView;
      if (typeof scroll === "function") scroll.call(next, { block: "nearest" });
      updatePosition();
      return true;
    },
    focus(): void {
      const focus = (root as { focus?: () => void }).focus;
      if (typeof focus === "function") focus.call(root);
    },
    dispose(): void {
      for (const off of teardown) off();
      teardown.length = 0;
    },
  };

  const diff = props.diff;
  // changes may be null over the wire (a nil Go slice marshals to JSON null) —
  // treat that as "no changes" rather than dereferencing null.length.
  if (!diff || !diff.changes || diff.changes.length === 0) {
    const empty = doc.createElement("div");
    empty.className = "fhr-diff__empty";
    empty.textContent = diff ? "No changes." : "No diff provided.";
    root.appendChild(empty);
    container.appendChild(root);
    return handle;
  }

  const stops = options.stops ?? reviewStops(diff);
  const stopPaths = stops.map((s) => s.row.path);
  const stopSet = new Set(stopPaths);
  handle.stops = stopPaths;

  // ── summary bar ─────────────────────────────────────────────────────────────
  // Counted over the review stops, not over every row: a reviewer reading
  // "7 modified" off a diff of three changed objects has been told the size of
  // the tree, which is not what they asked. `diffSummary` still reports the
  // whole-tree totals for callers that want them.
  const counts = countKinds(stops.map((stop) => stop.row));
  const summary = doc.createElement("div");
  summary.className = "fhr-diff__summary";
  for (const kind of counts.kinds) {
    const n = counts.byKind[kind] ?? 0;
    if (n === 0) continue;
    const known = KIND_SYMBOL[kind] !== undefined;
    const span = doc.createElement("span");
    span.className = `fhr-diff__count fhr-diff__count--${known ? kind : "other"}`;
    span.setAttribute("data-kind", kind);
    span.textContent = `${KIND_SYMBOL[kind] ?? "•"} ${n} ${kind}`;
    summary.appendChild(span);
  }

  // Position readout + step buttons: the same review path as `n`/`p`, for the
  // reviewer who never learns the keys.
  let position: HTMLElement | null = null;
  updatePosition = (): void => {
    if (!position) return;
    const at = selected === null ? -1 : stopPaths.indexOf(selected);
    position.textContent = at < 0 ? `${stopPaths.length} changes` : `${at + 1} / ${stopPaths.length}`;
  };
  if (options.onStep && stopPaths.length > 0) {
    const nav = doc.createElement("div");
    nav.className = "fhr-diff__nav";
    const step = (delta: number, label: string, title: string): HTMLElement => {
      const button = doc.createElement("button");
      button.className = "fhr-diff__step";
      button.textContent = label;
      button.setAttribute("type", "button");
      button.setAttribute("title", title);
      button.setAttribute("data-step", String(delta));
      const onClick = (): void => options.onStep?.(delta);
      button.addEventListener("click", onClick);
      teardown.push(() => button.removeEventListener("click", onClick));
      return button;
    };
    position = doc.createElement("span");
    position.className = "fhr-diff__position";
    nav.append(step(-1, "‹", "Previous change (p)"), position, step(1, "›", "Next change (n)"));
    summary.appendChild(nav);
  }
  root.appendChild(summary);
  updatePosition();

  if (props.mode === "merge") {
    const note = doc.createElement("div");
    note.className = "fhr-diff__note";
    note.textContent = "Interactive merge resolution is not yet available in this renderer.";
    root.appendChild(note);
  }

  // ── rows ────────────────────────────────────────────────────────────────────
  const format = options.format ?? ((row: DiffRow): FormattedChange => formatChange(row));
  const selectable = options.onSelect !== undefined || props.onEvent !== undefined;
  const list = doc.createElement("ul");
  list.className = "fhr-diff__rows";
  for (const row of flattenDiff(diff)) {
    const li = doc.createElement("li");
    li.className = "fhr-diff__row";
    li.setAttribute("data-depth", String(row.depth));
    li.setAttribute("data-path", row.path);
    li.setAttribute("data-kind", row.kind);
    li.setAttribute("aria-selected", "false");
    if (stopSet.has(row.path)) li.setAttribute("data-stop", "1");
    li.style.paddingLeft = `${8 + row.depth * 18}px`;

    const mark = doc.createElement("span");
    mark.className = `fhr-diff__mark fhr-diff__mark--${row.kind}`;
    mark.textContent = KIND_SYMBOL[row.kind] ?? "•";
    li.appendChild(mark);

    const label = doc.createElement("span");
    label.className = "fhr-diff__label";
    label.textContent = row.label;
    li.appendChild(label);

    if (row.before !== undefined || row.after !== undefined) {
      const display = format(row);
      if (display.noise) li.setAttribute("data-noise", "1");
      const values = doc.createElement("span");
      values.className = "fhr-diff__values";
      appendSide(doc, values, display.before, display.beforeSwatch?.css);
      values.appendChild(
        Object.assign(doc.createElement("span"), { className: "fhr-diff__arrow", textContent: "→" }),
      );
      appendSide(doc, values, display.after, display.afterSwatch?.css);
      li.appendChild(values);

      if (display.deltaCell) {
        const delta = doc.createElement("span");
        delta.className = "fhr-diff__delta";
        delta.textContent = display.deltaCell;
        li.appendChild(delta);
      } else if (display.noise) {
        const delta = doc.createElement("span");
        delta.className = "fhr-diff__delta";
        delta.setAttribute("title", "An array index changed; the data it points at may be identical.");
        delta.textContent = "index";
        li.appendChild(delta);
      }
    }

    if (selectable) {
      li.setAttribute("data-selectable", "1");
      const onClick = (): void => {
        options.onSelect?.(row.path, row);
        // Kept for the two-argument callers that predate DiffTreeOptions: with no
        // onSelect, a click is still a select event on the host's channel.
        if (!options.onSelect) props.onEvent?.({ type: "select", changePath: row.path });
      };
      li.addEventListener("click", onClick);
      teardown.push(() => li.removeEventListener("click", onClick));
    }
    rowByPath.set(row.path, li);
    list.appendChild(li);
  }
  root.appendChild(list);

  // ── keyboard ────────────────────────────────────────────────────────────────
  if (options.onStep && options.keyboard !== false) {
    // Focusable so the keys work at all, and so the reviewer can tab to the list
    // instead of having to click a row first.
    root.setAttribute("tabindex", "0");
    const onKeyDown = (event: unknown): void => {
      const e = event as KeyboardEvent & { preventDefault?: () => void };
      const delta = stepIntent(e);
      if (delta === 0) return;
      e.preventDefault?.();
      options.onStep?.(delta);
    };
    root.addEventListener("keydown", onKeyDown as EventListener);
    teardown.push(() => root.removeEventListener("keydown", onKeyDown as EventListener));
  }

  container.appendChild(root);
  if (options.selectedPath !== undefined) handle.select(options.selectedPath);
  return handle;
}

/** One side of a before → after pair, with a colour chip when it is a colour. */
function appendSide(doc: Document, into: HTMLElement, text: string, swatchCss?: string): void {
  if (swatchCss) {
    const chip = doc.createElement("span");
    chip.className = "fhr-diff__swatch";
    chip.style.background = swatchCss;
    chip.setAttribute("data-swatch", swatchCss);
    into.appendChild(chip);
  }
  const value = doc.createElement("span");
  value.textContent = text;
  into.appendChild(value);
}
