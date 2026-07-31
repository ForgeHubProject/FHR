// The left region: the model's structure, the whole of it.
//
// This is not the change list, and the difference is the point. The change list
// is short by construction and can only reach things that changed, so today
// there is no way to click an *unchanged* part for context — and the list ends
// up padded with structural wrappers just to stay navigable. Splitting them lets
// the tree be the whole model and the queue stay scannable.
//
// The data is `buildSceneGraph()`'s, unchanged: it already walks the file's node
// hierarchy and already annotates every node with its change kind. Until #56 that
// annotated tree was wired only to the box-scene fallback, so a reviewer saw it
// exactly when the model *failed* to load. Here it is a persistent region.
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

  for (const node of nodes) {
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
