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
// The rendered worklist is capped (MAX_ROWS), like the structure tree beside it
// and for the same reason — this builds synchronously inside the 3D mount. The
// position readout and the panel are keyed on the whole queue regardless, so the
// cap costs a row, never a place.
//
// DOM-only — no three.js.

import { queuePosition, type QueueEntry } from "./queue.js";

const KIND_SYMBOL: Record<string, string> = { added: "+", removed: "−", modified: "~" };

/**
 * Most rows this region will build. Every row is four elements and a listener,
 * built synchronously inside the 3D mount, and nothing upstream bounds the change
 * count (limits.ts caps blob *bytes*, which says nothing about how many changes
 * are in them): 20 000 changes was 80 000 live elements and ~0.3 s of blocking
 * DOM work before the canvas existed.
 *
 * Lower than the structure tree's cap next door, and for a different reason. The
 * tree is scrolled, so its cap is about how much list is worth building; the
 * queue is *stepped* — the position readout and `n`/`p` stay exact over all N
 * whatever this number is (see below), so past the cap a reviewer loses the row
 * under the highlight, not their place.
 */
export const MAX_ROWS = 2000;

/** The note that stands in for the stops the cap left unrendered. Never silent. */
export function truncatedQueueMessage(shown: number, total: number): string {
  return (
    `Listing the first ${shown} of ${total} changes — the other ${total - shown} are omitted to ` +
    `keep this view responsive. The position above and n / p still walk all ${total}.`
  );
}

export type QueueView = {
  el: HTMLElement;
  /** Move the highlight and rebuild the panel. null clears both. */
  select(path: string | null): void;
  /**
   * Change path → the deviation the heatmap measured for it, formatted.
   *
   * Arrives late and only sometimes: the queue is built before the 3D chunk has
   * loaded a model, and the number does not exist until the reviewer asks for
   * the heatmap (#46). So it is a *later* fact about a row that is already
   * there, not a field of the entry — and a row with no entry here simply
   * doesn't show it, which is the honest reading of "not measured".
   */
  setDeviations(byPath: ReadonlyMap<string, string>): void;
  dispose(): void;
};

/** The panel row label for a measured deviation. */
export const DEVIATION_LABEL = "max deviation";

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
  // The panel and the position readout are keyed on the *whole* queue, so a stop
  // past the cap still fills the panel and still counts: only its row is missing.
  for (const entry of entries) entryByPath.set(entry.path, entry);
  const shown = entries.length > MAX_ROWS ? entries.slice(0, MAX_ROWS) : entries;
  if (shown.length < entries.length) {
    const note = doc.createElement("div");
    note.className = "fhr3d__empty";
    note.setAttribute("data-truncated", String(entries.length - shown.length));
    note.textContent = truncatedQueueMessage(shown.length, entries.length);
    list.appendChild(note);
  }
  for (const entry of shown) {
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

  let deviations: ReadonlyMap<string, string> = new Map();

  /** The heatmap's number for this change, as one more panel row. */
  const appendDeviation = (path: string): void => {
    const measured = deviations.get(path);
    if (measured === undefined) return;
    const line = doc.createElement("div");
    line.className = "fhr3d__field";
    line.setAttribute("data-field", DEVIATION_LABEL);
    const label = doc.createElement("span");
    label.className = "fhr3d__fieldlabel";
    label.textContent = DEVIATION_LABEL;
    const value = doc.createElement("span");
    value.className = "fhr3d__delta";
    value.textContent = measured;
    line.append(label, value);
    panel.appendChild(line);
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
      appendDeviation(entry.path);
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
    appendDeviation(entry.path);
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
    setDeviations(byPath: ReadonlyMap<string, string>): void {
      deviations = byPath;
      fillPanel();
    },
    dispose(): void {
      for (const off of teardown) off();
      teardown.length = 0;
    },
  };
}
