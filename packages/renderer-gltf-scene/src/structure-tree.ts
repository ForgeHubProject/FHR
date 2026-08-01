// The left region: the model's structure, the whole of it.
//
// This is not the change list, and the difference is the point. The change list
// is short by construction and can only reach things that changed, so today
// there is no way to click an *unchanged* part for context — and the list ends
// up padded with structural wrappers just to stay navigable. Splitting them lets
// the tree be the whole model and the queue stay scannable.
//
// The data is `buildSceneGraph()`'s: the file's node hierarchy with a change
// kind on each row. Until #56 that annotated tree was wired only to the
// box-scene fallback, so a reviewer saw it exactly when the model *failed* to
// load. Here it is a persistent region.
//
// The annotation is only as complete as the map handed to `buildSceneGraph`,
// and a map of the *node* changes alone is not complete: a mesh or a material
// change paints geometry through the nodes carrying it (#51), and a row marked
// "unchanged" beside a viewport that has it painted orange is a lie in the more
// prominent of the two. index-3d.ts's `annotationKinds` is what folds those in.
//
// "The whole model" up to a row cap (MAX_ROWS): being on every mount means this
// is now on the critical path to first frame, and an unbounded row-per-node list
// makes a large assembly pay for it. The cap spends its budget on the changed
// nodes first and says what it left out — including, when the changed nodes
// alone overflow it, that some of *those* are missing.
//
// DOM-only — no three.js — and only the handful of DOM calls fake-dom covers.

import type { SceneNode } from "./scene-graph.js";

export type StructureTree = {
  el: HTMLElement;
  /**
   * Highlight the row for a glTF node name (null clears). Returns false when the
   * file has no node by that name — a diff can name one that isn't in the scene.
   */
  select(name: string | null): boolean;
  dispose(): void;
};

/** Indent per level, in pixels. Deep rigs are common, so this stays small. */
const INDENT = 11;
/** Past this depth the indent stops growing, or a skeleton runs off the region. */
const MAX_INDENT_DEPTH = 6;

/**
 * Most rows this region will build. Every row is three elements and a listener,
 * and this now runs on every mount rather than only the box-scene fallback, so
 * an unbounded tree on a 50 000-node assembly is 150 000 live elements built
 * synchronously before the canvas exists — half a second of blocking work for a
 * list nobody scrolls to the end of. Nothing upstream bounds the node count
 * (limits.ts caps blob *bytes*, which says nothing about node count), so the
 * bound belongs here.
 *
 * The number is a compromise, not a measurement: high enough that real assemblies
 * arrive whole, low enough that the pathological ones cost tens of milliseconds.
 */
export const MAX_ROWS = 4000;

/** What the cap kept, and what it cost to keep it. */
type CappedRows = {
  rows: readonly SceneNode[];
  /** Changed nodes the cap could not fit. Zero in every non-degenerate case. */
  changedDropped: number;
};

/**
 * Which rows survive the cap, in document order.
 *
 * Changed nodes are kept first: they are the names the queue asks `select()`
 * for, so dropping one breaks the queue↔tree link rather than merely shortening
 * a list. The remaining budget goes to the file's first unchanged nodes — the
 * tree is read top-down, and the top is the model's own root and its major
 * assemblies.
 *
 * "First", not "unconditionally": when the changed nodes *alone* overflow the
 * cap there is no ordering that keeps them all, and that case is not exotic here
 * — a regenerated topology makes `semanticCompare` false and the structural diff
 * reports every node changed (presentation.ts, SPEC-RENDERING §2e), so a
 * re-tessellated export lands on it every time. The overflow is counted rather
 * than hidden, because the note above the tree is otherwise stating a falsehood.
 */
function cappedRows(nodes: readonly SceneNode[], cap: number): CappedRows {
  if (nodes.length <= cap) return { rows: nodes, changedDropped: 0 };
  const keep = new Set<number>();
  let changedDropped = 0;
  for (let i = 0; i < nodes.length; i++) {
    if (nodes[i]!.kind === "unchanged") continue;
    if (keep.size < cap) keep.add(i);
    else changedDropped++;
  }
  for (let i = 0; i < nodes.length && keep.size < cap; i++) keep.add(i);
  return { rows: nodes.filter((_, i) => keep.has(i)), changedDropped };
}

/** The note that stands in for the rows the cap dropped. Never silent. */
export function truncatedMessage(shown: number, total: number, changedDropped = 0): string {
  const omitted =
    `Showing ${shown} of ${total} nodes — the other ${total - shown} are omitted to keep this ` +
    `view responsive. `;
  // Claiming completeness when the changed rows themselves overflowed would be a
  // lie on screen, and `select()` returns false for exactly those names.
  return changedDropped === 0
    ? `${omitted}Every changed node is listed.`
    : `${omitted}${changedDropped} of the omitted nodes changed, so not every change has a row here.`;
}

export function renderStructureTree(
  doc: Document,
  nodes: readonly SceneNode[],
  options: { onPick: (name: string) => void },
): StructureTree {
  const el = doc.createElement("div");
  el.className = "fhr3d__nodes";
  el.setAttribute("data-region-body", "tree");

  const teardown: (() => void)[] = [];
  // First row wins for a duplicated name: that is also the node the diff's own
  // name reconciliation lands on (node-index.ts), so the two agree.
  const rowByName = new Map<string, HTMLElement>();
  let selected: HTMLElement | null = null;

  if (nodes.length === 0) {
    const empty = doc.createElement("div");
    empty.className = "fhr3d__empty";
    empty.textContent = "This file's scene graph is empty.";
    el.appendChild(empty);
  }

  const { rows, changedDropped } = cappedRows(nodes, MAX_ROWS);
  if (rows.length < nodes.length) {
    const note = doc.createElement("div");
    note.className = "fhr3d__empty";
    note.setAttribute("data-truncated", String(nodes.length - rows.length));
    note.setAttribute("data-changed-dropped", String(changedDropped));
    note.textContent = truncatedMessage(rows.length, nodes.length, changedDropped);
    el.appendChild(note);
  }

  for (const node of rows) {
    const row = doc.createElement("div");
    row.className = "fhr3d__node";
    row.setAttribute("data-node", node.name);
    row.setAttribute("data-kind", node.kind);
    row.setAttribute("aria-selected", "false");
    row.setAttribute("title", node.name);
    row.style.paddingLeft = `${6 + Math.min(node.depth, MAX_INDENT_DEPTH) * INDENT}px`;

    // A dot rather than a +/−/~ glyph: this region is read as a shape, and the
    // marks belong to the change list where they carry the counts.
    const dot = doc.createElement("span");
    dot.className = "fhr3d__dot";
    dot.setAttribute("data-kind", node.kind);
    row.appendChild(dot);

    const label = doc.createElement("span");
    label.className = "fhr3d__nodename";
    label.textContent = node.name;
    row.appendChild(label);

    const onClick = (): void => options.onPick(node.name);
    row.addEventListener("click", onClick);
    teardown.push(() => row.removeEventListener("click", onClick));

    if (!rowByName.has(node.name)) rowByName.set(node.name, row);
    el.appendChild(row);
  }

  return {
    el,
    select(name: string | null): boolean {
      selected?.setAttribute("aria-selected", "false");
      selected = null;
      if (name === null) return true;
      const row = rowByName.get(name);
      if (!row) return false;
      row.setAttribute("aria-selected", "true");
      selected = row;
      const scroll = (row as { scrollIntoView?: (arg: unknown) => void }).scrollIntoView;
      if (typeof scroll === "function") scroll.call(row, { block: "nearest" });
      return true;
    },
    dispose(): void {
      for (const off of teardown) off();
      teardown.length = 0;
    },
  };
}
