// One review surface out of two views.
//
// The renderer owns both halves of the linkage issue #45 asks for — it renders
// the SDK's change tree *and* mounts the 3D scene — so the whole round trip is
// internal to this bundle:
//
//   row click / n / p / ↑ / ↓  ─┐
//   click on geometry in 3D    ─┼→  one selection  ─┬→ highlight the tree row
//   host push (selectedChangePath) ┘                ├→ fly + isolate + callout
//                                                   └→ onEvent({type:"select"})
//
// One rule keeps that from oscillating: **selection has a single owner, and it is
// this module**. The tree never highlights itself on click and the scene never
// decides what is selected; both report the viewer's action and wait to be told.
// The source of each change is tracked so a selection pushed *in* by a host is
// never echoed back *out* as an event, and so a click on geometry doesn't fly the
// camera away from the thing the reviewer just clicked.
//
// The 3D scene arrives as an injected mounter rather than an import: that keeps
// three.js out of the lite bundle (it is a lazily-imported chunk in production)
// and lets every behaviour here be tested headlessly against a stub scene.

import {
  renderDiffTree,
  stepIndex,
  stepIntent,
  type DiffTreeHandle,
  type ReviewStop,
} from "@fhr/renderer-sdk";
import type { MountProps } from "@fhr/types";
import { changeTreeCss } from "./palette.js";
import { entityStops, formatGltfChange, headline, headlines } from "./review.js";
import { buildQueue, type QueueEntry } from "./queue.js";

/** Where a selection came from — what the fan-out must skip. */
export type SelectSource = "viewer" | "scene" | "host";

/** The bit of the 3D scene this module talks to (see index-3d.ts for the real one). */
export type Scene3D = {
  dispose(): void;
  /** Select a change by path: fly, isolate, call out. Returns false if unknown. */
  selectChange?(path: string | null, opts?: { fly?: boolean }): boolean;
};

export type SceneHooks = {
  /** The viewer picked a change in the 3D view (null: picked nothing). */
  onPick?: (path: string | null) => void;
  /** Path → one-line summary, for the single viewport callout. */
  headlines?: Record<string, string>;
  /**
   * The review worklist for the 3D view's change-queue region. Formatted here,
   * where the SDK's formatting rules already live, so the 3D chunk renders a
   * worklist rather than re-interpreting a diff (see queue.ts).
   */
  queue?: QueueEntry[];
  /** The queue's ‹ / › asked for the next (+1) or previous (-1) change. */
  onStep?: (delta: number) => void;
};

/** Mounts the 3D scene. Async because in production it is a lazy chunk import. */
export type SceneMounter = (
  host: HTMLElement,
  props: MountProps,
  hooks: SceneHooks,
) => Promise<Scene3D>;

export type LiveView = {
  /**
   * Apply a host prop push without tearing anything down. Returns false when the
   * push reaches something built at mount time (new blobs, a new diff, a new
   * mode or theme), which is the caller's signal to fall back to a full redraw.
   */
  update(props: MountProps, prev: MountProps): boolean;
  dispose(): void;
  /** The selected change path, or null. */
  readonly selected: string | null;
  /** The review path, in display order. */
  readonly stops: string[];
  /** Whether the 3D scene is currently mounted. */
  readonly scene: Scene3D | null;
  select(path: string | null, source?: SelectSource): void;
  step(delta: number): void;
  /** Open (or close) the 3D scene — what the "View in 3D" button does. */
  toggle3d(): void;
};

/** Blob identity: what makes a re-fetch and a re-parse unavoidable. */
function blobKey(props: MountProps): string {
  const b = props.blobs;
  return [b?.base?.url, b?.head?.url, b?.ours?.url, b?.theirs?.url].join("|");
}

export function createLiveView(
  container: HTMLElement,
  initial: MountProps,
  mountScene: SceneMounter,
): LiveView {
  const doc = container.ownerDocument;
  let props = initial;
  let selected: string | null = initial.selectedChangePath ?? null;
  let scene: Scene3D | null = null;
  let sceneLoading = false;
  let disposed = false;
  let tree: DiffTreeHandle | null = null;
  const teardown: (() => void)[] = [];

  const stopList: ReviewStop[] = props.mode === "view" ? [] : entityStops(props.diff);
  const stops = stopList.map((s) => s.row.path);
  const lines = headlines(stopList);
  const queue = buildQueue(stopList, formatGltfChange, headline);

  const view: LiveView = {
    update(next: MountProps, prev: MountProps): boolean {
      if (disposed) return false;
      // Anything the mount pass consumed to build what's on screen: a change
      // there is not something this module can patch, and saying otherwise would
      // leave a stale picture. The caller redraws instead.
      if (next.mode !== prev.mode) return false;
      if ((next.theme ?? "light") !== (prev.theme ?? "light")) return false;
      if (blobKey(next) !== blobKey(prev)) return false;
      if (next.diff !== prev.diff) return false;

      // Callbacks may be freshly-bound closures on every host render; adopting
      // them is the whole point of an in-place update.
      props = next;
      const wanted = next.selectedChangePath;
      if (wanted !== undefined && (wanted ?? null) !== selected) view.select(wanted ?? null, "host");
      return true;
    },
    dispose(): void {
      disposed = true;
      for (const off of teardown) off();
      teardown.length = 0;
      tree?.dispose();
      scene?.dispose();
      scene = null;
    },
    get selected(): string | null {
      return selected;
    },
    get stops(): string[] {
      return stops;
    },
    get scene(): Scene3D | null {
      return scene;
    },
    select(path: string | null, source: SelectSource = "viewer"): void {
      selected = path;
      tree?.select(path);
      // The scene already applied a pick's visuals, and re-flying to the thing
      // the reviewer just clicked would yank the camera for no reason.
      if (source !== "scene") scene?.selectChange?.(path);
      if (source !== "host") props.onEvent?.({ type: "select", changePath: path });
    },
    step(delta: number): void {
      const at = selected === null ? -1 : stops.indexOf(selected);
      const next = stepIndex(stops.length, at, delta);
      if (next < 0) return;
      view.select(stops[next]!, "viewer");
    },
    toggle3d(): void {
      /* replaced below when a viewport exists */
    },
  };

  // ── "view" mode: a single snapshot, so the scene *is* the view ───────────────
  if (props.mode === "view") {
    const host = openViewport(container, doc, "420px");
    void attachScene(host);
    return view;
  }

  // ── diff/merge: the change tree, always ──────────────────────────────────────
  tree = renderDiffTree(container, props, {
    selectedPath: selected,
    stops: stopList,
    format: formatGltfChange,
    onSelect: (path) => view.select(path, "viewer"),
    onStep: (delta) => view.step(delta),
  });
  applyPalette(container);
  if (props.mode !== "diff") return view;

  // ── the "View in 3D" toggle ──────────────────────────────────────────────────
  const bar = doc.createElement("div");
  bar.style.cssText = "padding:10px 4px 4px";
  const button = doc.createElement("button");
  button.textContent = "View in 3D";
  button.style.cssText =
    "font:12px ui-sans-serif,system-ui;padding:5px 12px;border-radius:6px;border:1px solid #d0d7de;background:transparent;color:inherit;cursor:pointer";
  const host = doc.createElement("div");
  bar.appendChild(button);
  container.append(bar, host);

  // `n`/`p` work with the canvas focused too, so a reviewer who is orbiting the
  // model can step changes without going back to the list. The arrows are left
  // alone there: in a viewport they are the scene's, and Space stays the blink.
  const onHostKeyDown = (event: unknown): void => {
    const e = event as KeyboardEvent;
    const delta = stepIntent(e, { arrows: false });
    if (delta === 0) return;
    e.preventDefault?.();
    view.step(delta);
  };
  host.addEventListener("keydown", onHostKeyDown as EventListener);
  teardown.push(() => host.removeEventListener("keydown", onHostKeyDown as EventListener));

  view.toggle3d = (): void => {
    if (sceneLoading || disposed) return;
    if (scene) {
      scene.dispose();
      scene = null;
      host.replaceChildren();
      host.removeAttribute("style");
      button.textContent = "View in 3D";
      return;
    }
    sceneLoading = true;
    button.textContent = "Loading…";
    // Taller than the bare viewport used to be: the 3D view now carries its own
    // three-region chrome, and at 420px the regions fold away on a host page that
    // is otherwise wide enough for them.
    host.style.cssText = "width:100%;height:560px;margin-top:8px;border-radius:8px;overflow:hidden";
    void attachScene(host).then((ok) => {
      button.textContent = ok ? "Hide 3D" : "View in 3D";
    });
  };
  const onClick = (): void => view.toggle3d();
  button.addEventListener("click", onClick);
  teardown.push(() => button.removeEventListener("click", onClick));
  return view;

  /** Mount the scene into `host`, wire its picks, and hand it the selection. */
  async function attachScene(host: HTMLElement): Promise<boolean> {
    const status = doc.createElement("div");
    status.style.cssText = "padding:12px 4px;font:13px ui-sans-serif,system-ui;color:#8b949e";
    status.textContent = "Loading 3D scene…";
    host.appendChild(status);
    try {
      const handle = await mountScene(host, props, {
        headlines: lines,
        queue,
        onStep: (delta) => {
          if (!disposed) view.step(delta);
        },
        onPick: (path) => {
          if (disposed) return;
          view.select(path, "scene");
        },
      });
      sceneLoading = false;
      status.remove();
      // Disposed while the chunk was in flight: the scene must still be released.
      if (disposed) {
        handle.dispose();
        return false;
      }
      scene = handle;
      // Open on whatever was already selected: a reviewer who picked a row and
      // then asked for the 3D view meant "show me that". The scene's own
      // fly-to-all-changes reveal is still what happens with nothing selected.
      if (selected !== null) handle.selectChange?.(selected);
      return true;
    } catch (err) {
      sceneLoading = false;
      status.textContent = "3D scene failed to load: " + errText(err);
      return false;
    }
  }
}

/** A viewport filling the container: "view" mode's whole picture. */
function openViewport(container: HTMLElement, doc: Document, height: string): HTMLElement {
  const host = doc.createElement("div");
  host.style.cssText = `width:100%;height:${height};border-radius:8px;overflow:hidden`;
  container.appendChild(host);
  return host;
}

/**
 * Recolour the SDK's change tree with this renderer's colour-blind-safe palette,
 * so a change is the same colour in the tree and in the 3D scene. The SDK's own
 * stylesheet ships GitHub's red/green — the worst possible pair for deuteranopia
 * — and lives inside the tree it just rendered, so appending this after it wins
 * the cascade at equal specificity. Pure CSS text: no three.js reaches the lite
 * bundle through it.
 */
function applyPalette(container: HTMLElement): void {
  const style = container.ownerDocument.createElement("style");
  style.textContent = changeTreeCss();
  container.appendChild(style);
}

function errText(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
