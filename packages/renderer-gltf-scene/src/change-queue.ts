// The right region: the change queue, rendered.
//
// The `‹ N changes ›` readout and the `n`/`p` stepping shipped in #52 as corner
// widgets on the change tree. Here they are the organising principle of a whole
// region: a position at the top, the ordered worklist under it, and the selected
// change's fields in a panel beneath that. Same review path, promoted from a
// decoration to the thing the region is.
//
// Stepping is reported, never performed: the live view owns the selection (see
// live-view.ts), so a step here is a request that comes back as a selection.
// Doing it locally would give the chrome a second, drifting idea of where the
// reviewer is standing.
//
// DOM-only — no three.js.

import { queuePosition, type QueueEntry } from "./queue.js";

const KIND_SYMBOL: Record<string, string> = { added: "+", removed: "−", modified: "~" };

export type QueueView = {
  el: HTMLElement;
  /** Move the highlight and rebuild the panel. null clears both. */
  select(path: string | null): void;
  dispose(): void;
};

export function renderQueue(
  doc: Document,
  entries: readonly QueueEntry[],
  options: { onSelect: (path: string) => void; onStep: (delta: number) => void },
): QueueView {
  const el = doc.createElement("div");
  el.className = "fhr3d__queue";
  el.setAttribute("data-region-body", "queue");

  const teardown: (() => void)[] = [];
  const rowByPath = new Map<string, HTMLElement>();
  const entryByPath = new Map<string, QueueEntry>();
  let selected: string | null = null;

  // ── position + stepping ─────────────────────────────────────────────────────
  const nav = doc.createElement("div");
  nav.className = "fhr3d__nav";
  const position = doc.createElement("span");
  position.className = "fhr3d__pos";
  position.setAttribute("data-position", "1");
  const step = (delta: number, glyph: string, title: string): HTMLElement => {
    const button = doc.createElement("button");
    button.className = "fhr3d__step";
    button.textContent = glyph;
    button.setAttribute("type", "button");
    button.setAttribute("title", title);
    button.setAttribute("data-step", String(delta));
    const onClick = (): void => options.onStep(delta);
    button.addEventListener("click", onClick);
    teardown.push(() => button.removeEventListener("click", onClick));
    return button;
  };
  nav.append(step(-1, "‹", "Previous change (p)"), position, step(1, "›", "Next change (n)"));
  el.appendChild(nav);

  // ── the worklist ────────────────────────────────────────────────────────────
  const list = doc.createElement("div");
  list.className = "fhr3d__stops";
  for (const entry of entries) {
    const row = doc.createElement("div");
    row.className = "fhr3d__stop";
    row.setAttribute("data-path", entry.path);
    row.setAttribute("data-kind", entry.kind);
    row.setAttribute("aria-selected", "false");

    const mark = doc.createElement("span");
    mark.className = "fhr3d__mark";
    mark.setAttribute("data-kind", entry.kind);
    mark.textContent = KIND_SYMBOL[entry.kind] ?? "•";
    const label = doc.createElement("span");
    label.className = "fhr3d__stoplabel";
    label.textContent = entry.label;
    // The headline is the number the reviewer judges the change by, so it is in
    // the list itself rather than only in the panel below.
    const headline = doc.createElement("span");
    headline.className = "fhr3d__headline";
    headline.textContent = entry.headline;
    row.append(mark, label, headline);

    const onClick = (): void => options.onSelect(entry.path);
    row.addEventListener("click", onClick);
    teardown.push(() => row.removeEventListener("click", onClick));

    rowByPath.set(entry.path, row);
    entryByPath.set(entry.path, entry);
    list.appendChild(row);
  }
  if (entries.length === 0) {
    const empty = doc.createElement("div");
    empty.className = "fhr3d__empty";
    empty.textContent = "Nothing to review here.";
    list.appendChild(empty);
  }
  el.appendChild(list);

  // ── the selected change's fields ────────────────────────────────────────────
  const panel = doc.createElement("div");
  panel.className = "fhr3d__panel";
  panel.setAttribute("data-panel", "1");
  el.appendChild(panel);

  const updatePosition = (): void => {
    position.textContent = queuePosition(entries, selected).label;
  };

  const fillPanel = (): void => {
    panel.replaceChildren();
    const entry = selected === null ? undefined : entryByPath.get(selected);
    if (!entry) return;
    const title = doc.createElement("div");
    title.className = "fhr3d__paneltitle";
    title.textContent = entry.label;
    panel.appendChild(title);
    if (entry.details.length === 0) {
      const only = doc.createElement("div");
      only.className = "fhr3d__panelnote";
      only.textContent = entry.headline;
      panel.appendChild(only);
      return;
    }
    for (const detail of entry.details) {
      const line = doc.createElement("div");
      line.className = "fhr3d__field";
      line.setAttribute("data-field", detail.label);
      if (detail.noise) line.setAttribute("data-noise", "1");
      const label = doc.createElement("span");
      label.className = "fhr3d__fieldlabel";
      label.textContent = detail.label;
      const values = doc.createElement("span");
      values.className = "fhr3d__fieldvalues";
      values.textContent = `${detail.before} → ${detail.after}`;
      line.append(label, values);
      if (detail.delta !== undefined) {
        const delta = doc.createElement("span");
        delta.className = "fhr3d__delta";
        delta.textContent = detail.delta;
        line.appendChild(delta);
      }
      panel.appendChild(line);
    }
  };

  updatePosition();

  return {
    el,
    select(path: string | null): void {
      if (selected !== null) rowByPath.get(selected)?.setAttribute("aria-selected", "false");
      selected = path;
      if (path !== null) {
        const row = rowByPath.get(path);
        row?.setAttribute("aria-selected", "true");
        const scroll = (row as { scrollIntoView?: (arg: unknown) => void } | undefined)?.scrollIntoView;
        if (typeof scroll === "function" && row) scroll.call(row, { block: "nearest" });
      }
      updatePosition();
      fillPanel();
    },
    dispose(): void {
      for (const off of teardown) off();
      teardown.length = 0;
    },
  };
}
