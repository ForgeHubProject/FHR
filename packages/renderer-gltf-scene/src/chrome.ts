// The 3D view's three-region chrome (the decided layout on #56).
//
//   ┌──────────┬─────────────────────────────┬──────────────┐
//   │          │  file info    view options  │              │
//   │ structure├─────────────────────────────┤    change    │
//   │   tree   │          VIEWPORT           │    queue     │
//   └──────────┴─────────────────────────────┴──────────────┘
//
// It borrows the outliner / viewport / properties arrangement this audience
// already has muscle memory for — and then diverges on the right, where a review
// tool needs a worklist rather than a properties editor (see change-queue.ts).
//
// **The chrome persists across every mode; only the centre changes.** Switching
// presentation must not cost the reviewer their place, so the regions, the
// selection and the camera all outlive a mode switch — there is nothing here to
// rebuild when the mode changes, which is the cheapest way to guarantee it.
//
// Both side regions collapse by hand and fold automatically below a width
// threshold (chrome-layout.ts), which is what lets one layout serve a full
// window and a narrow column on a host's review page.
//
// DOM-only: no three.js reaches this file, and the DOM it touches is the small
// set fake-dom.ts covers, so the structure is tested headlessly.

import { KIND_CSS, NEUTRAL, hexCss } from "./palette.js";
import {
  QUEUE_WIDTH,
  TREE_WIDTH,
  regionLayout,
  type ChromeLayout,
  type RegionPreference,
} from "./chrome-layout.js";
import {
  HEATMAP_LABEL,
  HEATMAP_MODE,
  HEATMAP_TITLE,
  MODE_LABEL,
  MODE_TITLE,
  type PresentationMode,
} from "./presentation.js";
import { SPLIT_LABEL, otherSplit, type SplitOrientation } from "./split.js";
import { renderStructureTree, type StructureTree } from "./structure-tree.js";
import { renderQueue, type QueueView } from "./change-queue.js";
import type { SceneNode } from "./scene-graph.js";
import type { QueueEntry } from "./queue.js";

export type ChromeOptions = {
  theme?: "light" | "dark";
  /** Modes the toggle offers. One mode means no toggle: a switch with a single
   *  position is furniture, not a control. */
  modes: readonly PresentationMode[];
  mode: PresentationMode;
  split: SplitOrientation;
  /** Every node of the model, annotated with its change kind (scene-graph.ts). */
  structure: readonly SceneNode[];
  /** The review worklist. Empty (view mode) means no queue region at all. */
  queue: readonly QueueEntry[];
  /** One line for the top-left: what this view is showing. */
  info?: string;
  /**
   * Offer the deviation heatmap (#46). False builds no toggle at all — see
   * `heatmapOffered` in heatmap.ts for why a control that is present but refuses
   * would be worse than one that isn't there.
   */
  heatmap?: boolean;
  onMode: (mode: PresentationMode) => void;
  onSplit: (orientation: SplitOrientation) => void;
  onHeatmap?: (on: boolean) => void;
  onQueueSelect: (path: string) => void;
  onStep: (delta: number) => void;
  onNode: (name: string) => void;
};

export type Chrome = {
  root: HTMLElement;
  /** Where the canvas goes. Positioned, so the callout can overlay it. */
  viewport: HTMLElement;
  /** Move the queue's highlight and panel. */
  selectChange(path: string | null): void;
  /** Move the structure tree's highlight, by glTF node name. */
  highlightNode(name: string | null): void;
  setMode(mode: PresentationMode): void;
  setSplit(orientation: SplitOrientation): void;
  /** Change path → measured deviation, for the selected change's panel row. */
  setDeviations(byPath: ReadonlyMap<string, string>): void;
  /** Re-resolve region visibility for a container width. */
  applyWidth(width: number): void;
  readonly layout: ChromeLayout;
  dispose(): void;
};

function css(theme: "light" | "dark"): string {
  const dark = theme === "dark";
  const line = dark ? "#30363d" : "#d8dee4";
  const ink = dark ? "#e6edf3" : "#1f2328";
  const muted = dark ? "#8b949e" : "#57606a";
  const panel = dark ? "#0d1117" : "#ffffff";
  const hover = "rgba(130,130,130,0.12)";
  const pick = "rgba(31,111,235,0.16)";
  return `
.fhr3d { display:flex; height:100%; min-height:0; font:12px/1.5 ui-sans-serif,system-ui,sans-serif;
  color:${ink}; background:${panel}; border:1px solid ${line}; border-radius:8px; overflow:hidden; }
.fhr3d__region { display:flex; flex-direction:column; min-height:0; background:${panel}; }
.fhr3d__region[data-side="left"] { width:${TREE_WIDTH}px; border-right:1px solid ${line}; }
.fhr3d__region[data-side="right"] { width:${QUEUE_WIDTH}px; border-left:1px solid ${line}; }
.fhr3d__region[data-state="collapsed"] { width:26px; }
.fhr3d__region[data-state="collapsed"] .fhr3d__title,
.fhr3d__region[data-state="collapsed"] .fhr3d__nodes,
.fhr3d__region[data-state="collapsed"] .fhr3d__queue { display:none; }
.fhr3d__region[data-state="hidden"] { display:none; }
.fhr3d__head { display:flex; align-items:center; gap:6px; padding:6px 8px; border-bottom:1px solid ${line}; }
.fhr3d__region[data-state="collapsed"] .fhr3d__head { border-bottom:none; padding:6px 4px; }
.fhr3d__title { font-weight:600; font-size:11px; letter-spacing:0.04em; text-transform:uppercase; color:${muted}; }
.fhr3d__collapse { margin-left:auto; font:inherit; line-height:1; padding:2px 5px; border-radius:5px;
  border:1px solid transparent; background:transparent; color:${muted}; cursor:pointer; }
.fhr3d__collapse:hover { background:${hover}; }
.fhr3d__centre { flex:1 1 auto; display:flex; flex-direction:column; min-width:0; min-height:0; }
.fhr3d__bar { display:flex; align-items:center; gap:8px; padding:6px 8px; border-bottom:1px solid ${line}; }
.fhr3d__info { color:${muted}; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.fhr3d__opts { display:flex; align-items:center; gap:6px; margin-left:auto; }
.fhr3d__modes { display:flex; border:1px solid ${line}; border-radius:6px; overflow:hidden; }
.fhr3d__mode { font:inherit; padding:3px 9px; border:none; background:transparent; color:${ink}; cursor:pointer; }
.fhr3d__mode + .fhr3d__mode { border-left:1px solid ${line}; }
.fhr3d__mode:hover { background:${hover}; }
.fhr3d__mode[aria-pressed="true"] { background:${pick}; font-weight:600; }
.fhr3d__splitbtn, .fhr3d__heatbtn { font:inherit; padding:3px 9px; border:1px solid ${line};
  border-radius:6px; background:transparent; color:${ink}; cursor:pointer; }
.fhr3d__splitbtn:hover, .fhr3d__heatbtn:hover { background:${hover}; }
.fhr3d__splitbtn[hidden], .fhr3d__heatbtn[hidden] { display:none; }
.fhr3d__heatbtn[aria-pressed="true"] { background:${pick}; font-weight:600; }
.fhr3d__viewport { position:relative; flex:1 1 auto; min-height:0; overflow:hidden; }
.fhr3d__nodes, .fhr3d__queue { flex:1 1 auto; min-height:0; overflow:auto; }
.fhr3d__node { display:flex; align-items:center; gap:6px; padding:2px 6px; cursor:pointer;
  white-space:nowrap; overflow:hidden; }
.fhr3d__node:hover { background:${hover}; }
.fhr3d__node[aria-selected="true"] { background:${pick}; }
.fhr3d__nodename { overflow:hidden; text-overflow:ellipsis; }
.fhr3d__dot { flex:none; width:7px; height:7px; border-radius:50%; background:${hexCss(NEUTRAL)}; }
.fhr3d__dot[data-kind="added"] { background:${KIND_CSS['added']}; }
.fhr3d__dot[data-kind="modified"] { background:${KIND_CSS['modified']}; }
.fhr3d__dot[data-kind="removed"] { background:${KIND_CSS['removed']}; }
.fhr3d__nav { display:flex; align-items:center; gap:4px; padding:6px 8px; border-bottom:1px solid ${line}; }
.fhr3d__pos { flex:1 1 auto; text-align:center; color:${muted}; }
.fhr3d__step { font:inherit; line-height:1; padding:3px 8px; border:1px solid ${line}; border-radius:6px;
  background:transparent; color:inherit; cursor:pointer; }
.fhr3d__step:hover { background:${hover}; }
.fhr3d__stops { }
.fhr3d__stop { display:flex; align-items:baseline; gap:6px; padding:3px 8px; cursor:pointer; }
.fhr3d__stop:hover { background:${hover}; }
.fhr3d__stop[aria-selected="true"] { background:${pick}; }
.fhr3d__mark { flex:none; width:1em; text-align:center; font-weight:700; color:${muted}; }
.fhr3d__mark[data-kind="added"] { color:${KIND_CSS['added']}; }
.fhr3d__mark[data-kind="modified"] { color:${KIND_CSS['modified']}; }
.fhr3d__mark[data-kind="removed"] { color:${KIND_CSS['removed']}; }
.fhr3d__stoplabel { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.fhr3d__headline { margin-left:auto; padding-left:8px; color:${muted}; white-space:nowrap; }
.fhr3d__panel { border-top:1px solid ${line}; padding:6px 8px; }
.fhr3d__paneltitle { font-weight:600; padding-bottom:2px; }
.fhr3d__panelnote { color:${muted}; }
.fhr3d__field { display:flex; align-items:baseline; gap:6px; }
.fhr3d__field[data-noise="1"] { opacity:0.6; }
.fhr3d__fieldlabel { color:${muted}; }
.fhr3d__fieldvalues { font-family:ui-monospace,monospace; font-size:11px; overflow:hidden;
  text-overflow:ellipsis; white-space:nowrap; }
.fhr3d__delta { margin-left:auto; font-family:ui-monospace,monospace; font-size:11px; font-weight:600; }
.fhr3d__empty { padding:10px 8px; color:${muted}; }
`;
}

export function createChrome(container: HTMLElement, options: ChromeOptions): Chrome {
  const doc = container.ownerDocument;
  const theme = options.theme ?? "light";
  const teardown: (() => void)[] = [];

  const root = doc.createElement("div");
  root.className = "fhr3d";
  root.setAttribute("data-mode", options.mode);
  root.setAttribute("data-theme", theme);

  const style = doc.createElement("style");
  style.textContent = css(theme);
  root.appendChild(style);

  // The queue region only exists when there is a queue. A region holding "no
  // changes" spends the width the viewport needs to say nothing.
  const hasQueue = options.queue.length > 0;
  const wanted: RegionPreference = { tree: true, queue: hasQueue };
  let layout: ChromeLayout = { tree: true, queue: hasQueue };
  let width = 0;

  const button = (className: string, text: string, onClick: () => void): HTMLElement => {
    const el = doc.createElement("button");
    el.className = className;
    el.textContent = text;
    el.setAttribute("type", "button");
    el.addEventListener("click", onClick);
    teardown.push(() => el.removeEventListener("click", onClick));
    return el;
  };

  /** A side region: header with a collapse control, then its body. */
  const region = (key: "tree" | "queue", side: "left" | "right", title: string, body: HTMLElement) => {
    const el = doc.createElement("div");
    el.className = "fhr3d__region";
    el.setAttribute("data-region", key);
    el.setAttribute("data-side", side);
    el.setAttribute("data-state", "open");
    const head = doc.createElement("div");
    head.className = "fhr3d__head";
    const label = doc.createElement("span");
    label.className = "fhr3d__title";
    label.textContent = title;
    const collapse = button("fhr3d__collapse", side === "left" ? "‹" : "›", () => {
      wanted[key] = !wanted[key];
      applyWidth(width);
    });
    collapse.setAttribute("data-collapse", key);
    collapse.setAttribute("title", `Collapse or expand the ${title.toLowerCase()}`);
    head.append(label, collapse);
    el.append(head, body);
    return { el, collapse };
  };

  // ── left: structure ─────────────────────────────────────────────────────────
  const tree: StructureTree = renderStructureTree(doc, options.structure, { onPick: options.onNode });
  const left = region("tree", "left", "Structure", tree.el);

  // ── centre: top chrome + viewport ───────────────────────────────────────────
  const centre = doc.createElement("div");
  centre.className = "fhr3d__centre";
  centre.setAttribute("data-region", "viewport");

  const bar = doc.createElement("div");
  bar.className = "fhr3d__bar";
  const info = doc.createElement("div");
  info.className = "fhr3d__info";
  info.setAttribute("data-info", "1");
  info.textContent = options.info ?? "";
  const opts = doc.createElement("div");
  opts.className = "fhr3d__opts";
  opts.setAttribute("data-options", "1");

  const modeButtons = new Map<PresentationMode, HTMLElement>();
  if (options.modes.length > 1) {
    const group = doc.createElement("div");
    group.className = "fhr3d__modes";
    group.setAttribute("data-modes", "1");
    for (const mode of options.modes) {
      const el = button("fhr3d__mode", MODE_LABEL[mode], () => options.onMode(mode));
      el.setAttribute("data-mode", mode);
      el.setAttribute("title", MODE_TITLE[mode]);
      el.setAttribute("aria-pressed", String(mode === options.mode));
      modeButtons.set(mode, el);
      group.appendChild(el);
    }
    opts.appendChild(group);
  }

  // The split control is side-by-side's alone, so it is hidden rather than
  // absent: appearing and disappearing in place keeps the bar from reflowing.
  let split = options.split;
  const splitButton = button("fhr3d__splitbtn", SPLIT_LABEL[otherSplit(split)], () =>
    options.onSplit(otherSplit(split)),
  );
  splitButton.setAttribute("data-split", "1");
  opts.appendChild(splitButton);

  // The heatmap is a sub-view of overlay, so its toggle follows overlay the way
  // the split control follows side-by-side: hidden in the other modes, never
  // rebuilt. It exists at all only when there is something to measure.
  let heatmapOn = false;
  let heatmapButton: HTMLElement | null = null;
  if (options.heatmap === true) {
    const el = button("fhr3d__heatbtn", HEATMAP_LABEL, () => {
      heatmapOn = !heatmapOn;
      el.setAttribute("aria-pressed", String(heatmapOn));
      options.onHeatmap?.(heatmapOn);
    });
    el.setAttribute("data-heatmap", "1");
    el.setAttribute("aria-pressed", "false");
    el.setAttribute("title", HEATMAP_TITLE);
    opts.appendChild(el);
    heatmapButton = el;
  }

  bar.append(info, opts);
  const viewport = doc.createElement("div");
  viewport.className = "fhr3d__viewport";
  viewport.setAttribute("data-viewport", "1");
  centre.append(bar, viewport);

  // ── right: the queue ────────────────────────────────────────────────────────
  let queue: QueueView | null = null;
  let right: { el: HTMLElement } | null = null;
  if (hasQueue) {
    queue = renderQueue(doc, options.queue, {
      onSelect: options.onQueueSelect,
      onStep: options.onStep,
    });
    right = region("queue", "right", "Changes", queue.el);
  }

  root.append(left.el, centre);
  if (right) root.appendChild(right.el);
  container.appendChild(root);

  function applyState(key: "tree" | "queue", el: HTMLElement): void {
    // Three states, and the middle one matters: a region the *viewer* collapsed
    // keeps a rail they can click to bring it back, while one the width folded
    // is hidden outright — there is no room for a rail either.
    const fits = regionLayout(width, { tree: true, queue: hasQueue })[key];
    el.setAttribute("data-state", !fits ? "hidden" : wanted[key] ? "open" : "collapsed");
  }

  function applyWidth(next: number): void {
    width = next;
    layout = regionLayout(width, wanted);
    applyState("tree", left.el);
    if (right) applyState("queue", right.el);
  }

  function setMode(mode: PresentationMode): void {
    root.setAttribute("data-mode", mode);
    for (const [key, el] of modeButtons) el.setAttribute("aria-pressed", String(key === mode));
    if (mode === "side-by-side") splitButton.removeAttribute("hidden");
    else splitButton.setAttribute("hidden", "hidden");
    if (!heatmapButton) return;
    // Hidden outside overlay rather than reset: the scene suspends the heatmap
    // for the same trip and brings it back on return, so a button that forgot
    // would disagree with the picture the moment the reviewer came back.
    if (mode === HEATMAP_MODE) heatmapButton.removeAttribute("hidden");
    else heatmapButton.setAttribute("hidden", "hidden");
  }

  function setSplit(orientation: SplitOrientation): void {
    split = orientation;
    root.setAttribute("data-split", orientation);
    splitButton.textContent = SPLIT_LABEL[otherSplit(orientation)];
  }

  setMode(options.mode);
  setSplit(split);
  applyWidth(container.clientWidth || 0);

  // The container is laid out by the host, so its width can change without the
  // window's; the window listener is the fallback where the observer isn't there.
  const measure = (): void => applyWidth(container.clientWidth || width);
  const observer = typeof ResizeObserver === "function" ? new ResizeObserver(measure) : null;
  observer?.observe(container);
  globalThis.addEventListener?.("resize", measure);

  return {
    root,
    viewport,
    selectChange(path: string | null): void {
      queue?.select(path);
    },
    highlightNode(name: string | null): void {
      tree.select(name);
    },
    setMode,
    setSplit,
    setDeviations(byPath: ReadonlyMap<string, string>): void {
      queue?.setDeviations(byPath);
    },
    applyWidth,
    get layout(): ChromeLayout {
      return layout;
    },
    dispose(): void {
      observer?.disconnect();
      globalThis.removeEventListener?.("resize", measure);
      for (const off of teardown) off();
      teardown.length = 0;
      tree.dispose();
      queue?.dispose();
      root.remove();
    },
  };
}
