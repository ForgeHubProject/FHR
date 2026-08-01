// The gltf-scene renderer's OWN 3D scene. three.js is this renderer's private
// choice for drawing its picture — it is NOT a shared FHR contract and nothing
// here is reused by other formats' renderers. The only FHR contracts are
// mount() and StructuredDiff; everything in this file lives inside this one
// bundle. A different 3D format's renderer is free to draw however it likes.
//
// Two views live here, behind one viewport:
//   * mountModelScene — the real model with the diff painted on (#44), in
//                       whichever presentation the reviewer picked (#56)
//   * mountBoxScene   — a unit box per node, the honest fallback for when the
//                       real model can't be decoded (always paired with a banner)
//
// The presentations are not separate views. One scene, one camera rig, one
// canvas: a mode switch changes which groups are visible and, for side-by-side,
// how many scissored passes the frame takes. Nothing is torn down, which is what
// makes "switching presentation must not cost the reviewer their place" true by
// construction rather than by careful restoration.
//
// Requires a DOM + WebGL context (i.e. a real browser). Everything decided
// *about* the picture — mapping, grammar, framing, budgets, the split geometry,
// the mode ladder — lives in the pure modules this file calls, which are tested
// headlessly.

import * as THREE from "three";
import { OrbitControls } from "three/examples/jsm/controls/OrbitControls.js";
import type { SceneNode } from "./scene-graph.js";
import type { Overlay } from "./model-overlay.js";
import { createFlyTo, DEFAULT_FLY_MS } from "./flyto.js";
import { disposeTree } from "./dispose.js";
import { createIsolator } from "./isolate.js";
import { createCallout, projectToScreen } from "./callout.js";
import { changeAtHits, isClickGesture, isVisibleInTree, ndcFromPointer } from "./pick.js";
import { withPaneAspect } from "./camera-sync.js";
import { drawSplit, splitPanes, type Pane, type PaneSide, type SplitOrientation } from "./split.js";
import { HEATMAP_MODE, versionLayers, type PresentationMode } from "./presentation.js";
import { createHeatmapLegend, LEGEND_MEASURING } from "./legend.js";
import type { Heatmap, HeatmapSummary } from "./heatmap.js";

type Theme = "light" | "dark";

/** What a pane's label says. The reviewer must never have to infer which is which. */
const SIDE_LABEL: Record<PaneSide, string> = {
  base: "Previous version",
  head: "Current version",
};

export type SceneHandle = {
  dispose(): void;
  /**
   * Frame a named change (the change-list ⇄ 3D wiring in #45 calls this).
   * Returns false when the name isn't one of the painted changes.
   */
  flyToChange?(name: string): boolean;
  /** Frame every change at once — what the view does on load. */
  flyToChanges?(): void;
  /**
   * Select a change by name: fly to it, isolate it, and call it out (#45). null
   * clears the selection. `fly: false` selects without moving the camera — what a
   * click *in* the viewport does, since the reviewer is already looking at it.
   * Returns false when the name isn't one of the painted changes.
   */
  selectChange?(name: string | null, options?: { fly?: boolean }): boolean;
  /**
   * Frame a node of the *current* version by name, changed or not — what the
   * structure tree asks for. Returns false when this file has no such node.
   */
  frameNode?(name: string): boolean;
  /** Switch presentation. No-op for a scene with only one (the fallbacks). */
  setMode?(mode: PresentationMode): void;
  /** Change which way side-by-side cuts the canvas. */
  setSplit?(orientation: SplitOrientation): void;
  /**
   * Turn the deviation heatmap on or off (#46). The first `true` starts the
   * measurement, which is why nothing here is synchronous. No-op on a scene
   * built without one.
   */
  setHeatmap?(on: boolean): void;
};

const deg2rad = (d: number): number => (d * Math.PI) / 180;

type Viewport = {
  scene: THREE.Scene;
  camera: THREE.PerspectiveCamera;
  renderer: THREE.WebGLRenderer;
  controls: OrbitControls;
  /** Run `cb` once per frame, before the controls update and the draw. */
  onFrame(cb: (nowMs: number) => void): void;
  /**
   * Replace the per-frame draw — side-by-side takes two scissored passes where
   * every other mode takes one. null restores the single full-canvas render.
   */
  setDraw(draw: (() => void) | null): void;
  /** The canvas size in CSS pixels: what the split geometry is computed against. */
  size(): { width: number; height: number };
  /** Register teardown work to run before the viewport itself is released. */
  onDispose(cb: () => void): void;
  dispose(): void;
};

/**
 * Renderer + camera + controls + lights + grid + the frame loop, with resize
 * handling and a teardown that actually releases the GPU. Both views share it.
 */
function createViewport(container: HTMLElement, theme: Theme): Viewport {
  const width = container.clientWidth || 640;
  const height = container.clientHeight || 420;

  const scene = new THREE.Scene();
  const background = new THREE.Color(theme === "dark" ? 0x0d1117 : 0xf6f8fa);
  scene.background = background;

  const camera = new THREE.PerspectiveCamera(50, width / height, 0.1, 5000);

  const renderer = new THREE.WebGLRenderer({ antialias: true });
  // Side-by-side clears the canvas itself before scissoring (split.ts), and a
  // bare clear() uses whatever colour the GL state happens to hold — black on
  // the first frame, since nothing has rendered a background yet. Naming it here
  // makes the gutter the same colour as the space around the model, always.
  renderer.setClearColor(background, 1);
  renderer.setPixelRatio(Math.min(globalThis.devicePixelRatio || 1, 2));
  renderer.setSize(width, height);
  container.appendChild(renderer.domElement);

  scene.add(new THREE.AmbientLight(0xffffff, 0.75));
  const key = new THREE.DirectionalLight(0xffffff, 0.85);
  key.position.set(6, 10, 8);
  scene.add(key);

  const grid = new THREE.GridHelper(
    40,
    40,
    theme === "dark" ? 0x30363d : 0xd0d7de,
    theme === "dark" ? 0x21262d : 0xe6edf3,
  );
  scene.add(grid);

  const controls = new OrbitControls(camera, renderer.domElement);
  controls.enableDamping = true;

  const frameCallbacks: ((nowMs: number) => void)[] = [];
  const disposeCallbacks: (() => void)[] = [];
  const size = { width, height };
  let draw: (() => void) | null = null;

  let raf = 0;
  let alive = true;
  const tick = (nowMs: number): void => {
    if (!alive) return;
    raf = requestAnimationFrame(tick);
    for (const cb of frameCallbacks) cb(nowMs);
    controls.update();
    if (draw) draw();
    else renderer.render(scene, camera);
  };
  raf = requestAnimationFrame(tick);

  // Resize: the container is laid out by the host, so its size can change
  // without the window's. ResizeObserver catches that; the window listener is
  // the fallback where the observer isn't available.
  const resize = (): void => {
    const w = container.clientWidth || size.width;
    const h = container.clientHeight || size.height;
    if (w === 0 || h === 0) return;
    size.width = w;
    size.height = h;
    camera.aspect = w / h;
    camera.updateProjectionMatrix();
    renderer.setSize(w, h);
  };
  const observer = typeof ResizeObserver === "function" ? new ResizeObserver(() => resize()) : null;
  observer?.observe(container);
  globalThis.addEventListener?.("resize", resize);

  return {
    scene,
    camera,
    renderer,
    controls,
    onFrame(cb): void {
      frameCallbacks.push(cb);
    },
    setDraw(next): void {
      draw = next;
    },
    size(): { width: number; height: number } {
      return { width: size.width, height: size.height };
    },
    onDispose(cb): void {
      disposeCallbacks.push(cb);
    },
    dispose(): void {
      alive = false;
      cancelAnimationFrame(raf);
      observer?.disconnect();
      globalThis.removeEventListener?.("resize", resize);
      for (const cb of disposeCallbacks) cb();
      controls.dispose();
      disposeTree(grid);
      renderer.dispose();
      // Free the WebGL context itself: browsers cap live contexts (~16), and a
      // reviewer toggling the 3D view repeatedly would otherwise hit the cap and
      // start losing older canvases.
      try {
        renderer.forceContextLoss();
      } catch {
        // Not fatal — some environments don't implement the extension.
      }
      renderer.domElement.parentNode?.removeChild(renderer.domElement);
    },
  };
}

export type ModelSceneOptions = {
  overlay: Overlay;
  theme?: Theme;
  /** Presentation to open on (presentation.ts). Default: structural. */
  mode?: PresentationMode;
  /** Which way side-by-side cuts the canvas. Default: columns. */
  split?: SplitOrientation;
  /** Enable the A/B blink (only meaningful when the base model loaded). */
  blink?: boolean;
  /** Fly to the changes once mounted (the reveal on load). Default: on. */
  flyToChangesOnLoad?: boolean;
  /**
   * The viewer clicked geometry: the change name they hit, or null for a click
   * that landed on unchanged geometry or on nothing. The scene has already
   * applied the selection's visuals by the time this runs.
   */
  onPick?: (name: string | null) => void;
  /** Change name → the one-line headline its callout shows (see review.ts). */
  headlines?: Record<string, string>;
  /**
   * The change that reaches a node through geometry the node does not own — a
   * mesh several nodes instance, a material one of its primitives references
   * (#51). Null for a node no change reaches.
   *
   * Supplied by the mount rather than derived here: the mount holds the name
   * index the paint itself resolved through, so the caption over a painted node
   * and the dot beside its structure-tree row cannot come from two answers.
   */
  changeOfNode?: (name: string) => string | null;
  /**
   * The deviation heatmap, already built and gated (heatmap.ts). Absent means
   * the view can't offer one, and nothing here draws a toggle or a legend.
   */
  heatmap?: Heatmap | null;
  /**
   * The measurement finished. Fired once per mount, not on every toggle: the
   * mount forwards these numbers to the change queue's panel, and a deviation
   * the reviewer has already been shown is a fact about the two files that
   * switching the colours off does not un-measure.
   */
  onHeatmap?: (summary: HeatmapSummary) => void;
};

/** How framing a structure-tree row should read. */
export type NodeFraming =
  /** The diff names this node: behave exactly as the queue does for it. */
  | { via: "change"; change: string }
  /** Painted, but through a mesh or a material — the change has another name. */
  | { via: "entity"; change: string }
  /** The tree's root row, which is the glTF scene rather than a node. */
  | { via: "scene" }
  /** Genuinely untouched: the only case that may say so. */
  | { via: "none" };

/** The callout line over a node the diff does not reach. */
export const NOT_IN_CHANGE_LIST = "not in the change list";

/**
 * What clicking a structure-tree row means, as a decision with no pixels in it.
 *
 * Split out because mounting the view needs WebGL and this is the part that can
 * lie. `boxByChangeName` is keyed on *change* names, and #51's two change
 * classes are named for a mesh or a material rather than for the node they
 * paint — so asking that map "is this node in the change list" answers no for
 * geometry the overlay has just painted orange, and the row that used to caption
 * it "not in the change list" was contradicting the picture next to it.
 */
export function framingForNode(
  name: string,
  facts: {
    isChangeName(name: string): boolean;
    changeOfNode(name: string): string | null;
    sceneRootName: string | null;
  },
): NodeFraming {
  if (facts.isChangeName(name)) return { via: "change", change: name };
  const entity = facts.changeOfNode(name);
  if (entity !== null) return { via: "entity", change: entity };
  // The root row must not borrow the unchanged node's headline: "not in the
  // change list" over a model with changes reads as a claim about the file.
  return name === facts.sceneRootName ? { via: "scene" } : { via: "none" };
}

/**
 * Mount the real model with the diff painted on. The overlay is already built
 * (see model-overlay.ts); this adds the viewport, the A/B blink and the camera
 * behaviour, and owns teardown of both.
 */
export function mountModelScene(container: HTMLElement, options: ModelSceneOptions): SceneHandle {
  const { overlay } = options;
  const theme = options.theme ?? "light";
  const viewport = createViewport(container, theme);
  viewport.scene.add(overlay.root);

  const flyTo = createFlyTo(viewport.camera, viewport.controls);
  // Start on the whole model, then move to the changes: the reviewer sees what
  // they are looking at before the camera closes in on what changed.
  flyTo.snap(overlay.sceneBox.isEmpty() ? unitBox() : overlay.sceneBox);
  if (options.flyToChangesOnLoad !== false && !overlay.changeBox.isEmpty()) {
    flyTo.to(overlay.changeBox, DEFAULT_FLY_MS);
  }
  viewport.onFrame((nowMs) => flyTo.update(nowMs));
  // A flight the reviewer interrupts by grabbing the model must stop, not fight.
  const onControlStart = (): void => flyTo.cancel();
  viewport.controls.addEventListener("start", onControlStart);

  // ── presentation ────────────────────────────────────────────────────────────
  // Every mode is the same scene with a different set of groups visible, plus —
  // for side-by-side only — a second scissored pass. Nothing is built or torn
  // down on a switch, so the camera, the selection and the region layout all
  // survive it without anything having to remember to restore them.
  let mode: PresentationMode = options.mode ?? "structural";
  let split: SplitOrientation = options.split ?? "columns";
  let blinking = false;

  /**
   * Show exactly one version. What `grammar` means, and why the paint is part of
   * it rather than something a hidden group takes care of, is `versionLayers`
   * in presentation.ts — the decision is pure and tested there; this only writes
   * the answer onto the scene.
   */
  const showVersion = (side: PaneSide, grammar: boolean): void => {
    const layers = versionLayers({ side, grammar, mode });
    overlay.headGroup.visible = layers.head;
    if (overlay.baseSolidGroup) overlay.baseSolidGroup.visible = layers.baseSolid;
    if (overlay.baseGhostGroup) overlay.baseGhostGroup.visible = layers.baseGhost;
    if (overlay.removedGroup) overlay.removedGroup.visible = layers.removed;
    if (overlay.movedGroup) overlay.movedGroup.visible = layers.moved;
    // A material swap, not a traversal, and guarded against no-ops — side-by-side
    // asks once per pane per frame.
    overlay.setPaint(layers.paint);
  };

  // ── A/B blink ───────────────────────────────────────────────────────────────
  // Both versions are already resident, so the swap is a `visible` toggle and
  // lands in the very next frame. That matters: the change-blindness literature
  // is unambiguous that a blank or slow intermediate frame destroys the
  // detection advantage the blink exists to provide.
  //
  // It works in every mode. In side-by-side it swaps which pane draws which
  // version, so each pane does the same-pixels A/B the effect depends on — the
  // one thing side-by-side otherwise cannot give you.
  const canBlink = options.blink === true && overlay.baseSolidGroup !== null;
  const showBase = (on: boolean): void => {
    blinking = on;
    if (mode !== "side-by-side") showVersion(on ? "base" : "head", true);
    // side-by-side reads `blinking` in its own pass, once per frame.
  };

  const canvas = viewport.renderer.domElement;
  if (canBlink) {
    // Compile both blink states up front, for the same "no slow frame" reason: a
    // material first seen mid-blink would compile its shader on that frame.
    showVersion("base", true);
    try {
      viewport.renderer.compile(viewport.scene, viewport.camera);
    } catch {
      // compile() is an optimisation; a failure here must not break mounting.
    }
    showVersion("head", true);

    canvas.tabIndex = 0;
    canvas.style.outline = "none";
    const isSpace = (event: KeyboardEvent): boolean => event.code === "Space" || event.key === " ";
    const onKeyDown = (event: KeyboardEvent): void => {
      if (!isSpace(event)) return;
      event.preventDefault(); // don't scroll the review page
      if (!event.repeat) showBase(true);
    };
    const onKeyUp = (event: KeyboardEvent): void => {
      if (!isSpace(event)) return;
      event.preventDefault();
      showBase(false);
    };
    const onPointerDown = (): void => canvas.focus();
    const onBlur = (): void => showBase(false);
    canvas.addEventListener("keydown", onKeyDown);
    canvas.addEventListener("keyup", onKeyUp);
    canvas.addEventListener("pointerdown", onPointerDown);
    canvas.addEventListener("blur", onBlur);
    viewport.onDispose(() => {
      canvas.removeEventListener("keydown", onKeyDown);
      canvas.removeEventListener("keyup", onKeyUp);
      canvas.removeEventListener("pointerdown", onPointerDown);
      canvas.removeEventListener("blur", onBlur);
    });
  }

  const hint = canBlink ? blinkHint(container) : null;

  // ── selection: isolate + one callout ────────────────────────────────────────
  // The base model's solid copy is exempt from isolation: the blink is a
  // whole-model comparison and has to keep working while a change is isolated.
  const isolator = createIsolator(overlay.root, {
    skip: overlay.baseSolidGroup ? [overlay.baseSolidGroup] : [],
  });
  const callout = createCallout(container, theme);
  const anchor = new THREE.Vector3();

  /** What the viewport is currently pointing at, whatever put it there. */
  type Marked = {
    name: string;
    headline: string;
    box: THREE.Box3;
    /** The objects to isolate, or null for something that isn't a change. */
    isolate: THREE.Object3D[] | null;
  };
  let marked: Marked | null = null;

  /**
   * Re-apply the marking for the current mode. Both affordances are
   * single-viewport only, for the same underlying reason — they assume there is
   * one picture:
   *
   *   isolation, because two panes showing different amounts of model is not a
   *   comparison, it is a trick question;
   *   the callout, because it is placed by projecting a world point into *the*
   *   viewport, and in a split there are two projections. A label placed by the
   *   wrong one points confidently at unrelated geometry.
   *
   * The queue's panel carries the same numbers, so nothing is lost in the split.
   */
  const refreshMark = (): void => {
    const single = mode !== "side-by-side";
    if (marked?.isolate && single) isolator.isolate(marked.isolate);
    else isolator.clear();
    if (marked && single) callout.show(marked.name, marked.headline);
    else callout.hide();
  };

  const mark = (next: Marked | null, fly: boolean): void => {
    marked = next;
    refreshMark();
    if (fly && next) flyTo.to(next.box);
  };

  const applySelection = (name: string | null, fly: boolean): boolean => {
    if (name === null) {
      mark(null, false);
      return true;
    }
    const box = overlay.boxByChangeName.get(name);
    const objects = overlay.objectsByChangeName.get(name);
    if (!box || box.isEmpty() || !objects || objects.length === 0) {
      mark(null, false);
      return false;
    }
    mark({ name, headline: options.headlines?.[name] ?? "changed", box, isolate: objects }, fly);
    return true;
  };

  // The callout follows the geometry: one projection and two style writes per
  // frame, and only while something is marked.
  viewport.onFrame(() => {
    if (!marked || !callout.visible) return;
    marked.box.getCenter(anchor);
    const width = container.clientWidth || 1;
    const height = container.clientHeight || 1;
    callout.place(projectToScreen(anchor, viewport.camera, { width, height }), { width, height });
  });

  // ── side-by-side ────────────────────────────────────────────────────────────
  // One renderer, two scissor rects, one camera rig drawn twice (camera-sync.ts
  // for why that is the whole of "cameras locked"). Both versions are already
  // resident for the ghost and the blink, so the mode loads nothing.
  const paneLabels = overlay.baseSolidGroup === null ? null : createPaneLabels(container, theme);
  let labelKey = "";

  const drawPanes = (): void => {
    const { renderer, scene, camera } = viewport;
    const size = viewport.size();
    const panes = splitPanes(split, size);
    // drawSplit owns the scissor/clear sequence — see split.ts for why the
    // gutter needs a clear of its own.
    drawSplit(renderer, size, panes, (pane) => {
      // Holding Space swaps the panes' contents, so each pane compares the two
      // versions in its own pixels rather than across the gutter.
      const side: PaneSide = blinking ? (pane.side === "base" ? "head" : "base") : pane.side;
      showVersion(side, false);
      withPaneAspect(camera, pane.aspect, () => renderer.render(scene, camera));
    });
    placeLabels(panes);
  };

  /** Move the pane labels, but only when something about them actually changed. */
  const placeLabels = (panes: Pane[]): void => {
    if (!paneLabels) return;
    const key = panes.map((p) => `${p.side}:${p.css.x},${p.css.y},${p.css.width}`).join("|") + (blinking ? "!" : "");
    if (key === labelKey) return;
    labelKey = key;
    panes.forEach((pane, index) => {
      const side: PaneSide = blinking ? (pane.side === "base" ? "head" : "base") : pane.side;
      paneLabels.place(index, SIDE_LABEL[side], pane);
    });
  };

  // ── deviation heatmap ───────────────────────────────────────────────────────
  // A sub-view of overlay rather than a mode of its own (presentation.ts), and
  // lazy in the strongest sense: the measurement doesn't start until the first
  // `setHeatmap(true)`, and switching modes suspends it rather than recomputing.
  //
  // Suspends rather than *forgets*: what the reviewer asked for outlives a trip
  // through side-by-side, so returning to overlay brings the colours back for the
  // cost of one pointer write per painted mesh.
  const heatmap = options.heatmap ?? null;
  const legend = heatmap ? createHeatmapLegend(container, theme) : null;
  const heatTargets = heatmap ? heatmap.targets() : [];
  let heatmapWanted = false;
  /**
   * Which enable() call owns the picture. A reviewer who toggles twice while a
   * large mesh is being measured would otherwise get the first run's colours
   * written over the second's decision.
   */
  let heatmapTicket = 0;
  /** Where the pointer last was, or null when there is nothing new to read. */
  let hoverAt: { clientX: number; clientY: number } | null = null;

  const heatmapActive = (): boolean => heatmapWanted && mode === HEATMAP_MODE;

  const applyHeatmap = (): void => {
    if (!heatmap || !legend) return;
    if (!heatmapActive()) {
      heatmapTicket++;
      heatmap.disable();
      legend.show(false);
      hoverAt = null;
      return;
    }
    const known = heatmap.summary();
    legend.show(true);
    // The status line exists because the honest alternative — an empty legend
    // beside an unpainted model — is indistinguishable from a broken toggle.
    legend.setStatus(known === null ? LEGEND_MEASURING : null);
    if (known) legend.setRange(known.min, known.max);
    const ticket = ++heatmapTicket;
    void heatmap.enable().then((summary) => {
      if (ticket !== heatmapTicket || summary === null) return;
      legend.setStatus(null);
      legend.setRange(summary.min, summary.max);
      // Reported once, and not withdrawn when the heatmap goes off: the number
      // is a measurement of the two files, not a property of the current view.
      options.onHeatmap?.(summary);
    });
  };

  /** Make the current `mode` the picture. One place, used by mount and by the toggle. */
  const applyMode = (): void => {
    if (mode === "side-by-side") {
      viewport.setDraw(drawPanes);
      paneLabels?.show(true);
    } else {
      viewport.setDraw(null);
      paneLabels?.show(false);
      labelKey = "";
      showVersion(blinking ? "base" : "head", true);
    }
    // After showVersion, which may have put the overlay's own paint back on.
    applyHeatmap();
    refreshMark();
  };
  applyMode();

  // ── picking ─────────────────────────────────────────────────────────────────
  // Pick against the painted layers only: the ghost of the previous version is
  // context, and clicking through it to the current model is what a reviewer
  // means. OrbitControls owns dragging, so a press that travelled is not a click.
  const raycaster = new THREE.Raycaster();
  const pickTargets: THREE.Object3D[] = [overlay.headGroup];
  if (overlay.removedGroup) pickTargets.push(overlay.removedGroup);
  if (overlay.movedGroup) pickTargets.push(overlay.movedGroup);

  /**
   * The rect and camera aspect a click must be turned into a ray with.
   *
   * A split canvas holds two projections, so building a ray from the whole
   * canvas would land it wherever the arithmetic happened to put it — confidently
   * selecting geometry the reviewer did not click. Each pane is measured on its
   * own instead, and only the pane showing the *current* version is pickable:
   * the diff is painted on that model, and the previous version has no change to
   * select. A click in the gutter, or in the other pane, is not a pick.
   */
  const pickTarget = (
    event: PointerEvent,
    rect: DOMRect,
  ): { rect: { left: number; top: number; width: number; height: number }; aspect: number } | null => {
    if (mode !== "side-by-side") return { rect, aspect: viewport.camera.aspect };
    // While the blink is held the panes are transposed and only the last one
    // drawn has its geometry visible, so a pick then would be a coin flip.
    if (blinking) return null;
    const x = event.clientX - rect.left;
    const y = event.clientY - rect.top;
    for (const pane of splitPanes(split, viewport.size())) {
      if (x < pane.css.x || x >= pane.css.x + pane.css.width) continue;
      if (y < pane.css.y || y >= pane.css.y + pane.css.height) continue;
      if (pane.side !== "head") return null;
      return {
        rect: {
          left: rect.left + pane.css.x,
          top: rect.top + pane.css.y,
          width: pane.css.width,
          height: pane.css.height,
        },
        aspect: pane.aspect,
      };
    }
    return null;
  };

  let pressed: { x: number; y: number; t: number } | null = null;
  const onPointerDown = (event: PointerEvent): void => {
    pressed = { x: event.clientX, y: event.clientY, t: nowMs() };
  };
  const onPointerUp = (event: PointerEvent): void => {
    const down = pressed;
    pressed = null;
    if (!down) return;
    if (!isClickGesture(down, { x: event.clientX, y: event.clientY, t: nowMs() })) return;

    const target = pickTarget(event, canvas.getBoundingClientRect());
    if (!target) return;
    const ndc = ndcFromPointer(event, target.rect);
    let hits: THREE.Intersection[] = [];
    withPaneAspect(viewport.camera, target.aspect, () => {
      raycaster.setFromCamera(new THREE.Vector2(ndc.x, ndc.y), viewport.camera);
      hits = raycaster.intersectObjects(pickTargets, true);
    });
    const name = changeAtHits(hits, {
      changeNameByObject: overlay.changeNameByObject,
      changeNameByNodeIndex: overlay.changeNameByNodeIndex,
      nodeIndexOf: overlay.nodeIndexOfObject,
    });
    // Selecting without flying: the reviewer is already looking at what they hit.
    applySelection(name, false);
    options.onPick?.(name);
  };
  canvas.addEventListener("pointerdown", onPointerDown as EventListener);
  canvas.addEventListener("pointerup", onPointerUp as EventListener);

  // The legend's live reading. Recorded on the event and resolved in the frame
  // loop, at most one raycast per frame: pointermove fires far faster than the
  // display refreshes, and a ray per event would spend more time answering a
  // question about a pixel nobody looked at than drawing the model.
  const onHoverMove = (event: PointerEvent): void => {
    if (heatmapActive()) hoverAt = { clientX: event.clientX, clientY: event.clientY };
  };
  const onHoverLeave = (): void => {
    hoverAt = null;
    legend?.clearReading();
  };
  if (heatmap && legend) {
    canvas.addEventListener("pointermove", onHoverMove as EventListener);
    canvas.addEventListener("pointerleave", onHoverLeave);
    viewport.onFrame(() => {
      const at = hoverAt;
      if (!at || !heatmapActive() || !heatmap.on) return;
      hoverAt = null;
      const ndc = ndcFromPointer(at, canvas.getBoundingClientRect());
      raycaster.setFromCamera(new THREE.Vector2(ndc.x, ndc.y), viewport.camera);
      const hits = raycaster.intersectObjects(heatTargets, false);
      const front = hits.find((hit) => isVisibleInTree(hit.object));
      const reading = front ? heatmap.readAt(front.object, front.face) : null;
      if (reading) legend.setReading(reading.label, reading.value);
      else legend.clearReading();
    });
  }

  viewport.onDispose(() => {
    viewport.controls.removeEventListener("start", onControlStart);
    canvas.removeEventListener("pointerdown", onPointerDown as EventListener);
    canvas.removeEventListener("pointerup", onPointerUp as EventListener);
    canvas.removeEventListener("pointermove", onHoverMove as EventListener);
    canvas.removeEventListener("pointerleave", onHoverLeave);
    callout.dispose();
    isolator.clear();
    legend?.dispose();
    paneLabels?.dispose();
    hint?.remove();
  });

  return {
    dispose(): void {
      viewport.dispose();
      // Before the overlay, and in this order for two reasons: the heat clones
      // have to come off the model while the meshes it built are still around,
      // and the `color` attributes it wrote live on geometry the overlay's walk
      // is about to dispose.
      heatmap?.dispose();
      overlay.dispose();
    },
    flyToChange(name: string): boolean {
      const box = overlay.boxByChangeName.get(name);
      if (!box || box.isEmpty()) return false;
      flyTo.to(box);
      return true;
    },
    flyToChanges(): void {
      flyTo.to(overlay.changeBox);
    },
    selectChange(name: string | null, selectOptions: { fly?: boolean } = {}): boolean {
      return applySelection(name, selectOptions.fly !== false);
    },
    frameNode(name: string): boolean {
      const framing = framingForNode(name, {
        isChangeName: (n) => overlay.boxByChangeName.has(n),
        changeOfNode: options.changeOfNode ?? ((): null => null),
        sceneRootName: overlay.sceneRootName,
      });
      // A node the diff names should behave exactly as it does from the queue,
      // or the two regions would disagree about what "selected" looks like.
      if (framing.via === "change") return applySelection(framing.change, true);
      const box = overlay.boxOfNode(name);
      if (!box) return false;
      if (framing.via === "entity") {
        // Painted through a mesh or a material: the clicked row's own box is
        // framed, because one mesh can be instanced by nodes on opposite sides
        // of the model and the reviewer asked about this one — but the caption
        // and the isolation come from the change, which is what makes it orange.
        mark(
          {
            name,
            headline: options.headlines?.[framing.change] ?? "changed",
            box,
            isolate: overlay.objectsByChangeName.get(framing.change) ?? null,
          },
          true,
        );
        return true;
      }
      // An unchanged node gets framed and named, and nothing is isolated. What a
      // *selected* unchanged node should do to the rest of the model — dim it,
      // leave it — is one of the questions #56 left open; this deliberately does
      // the least that still answers "which one is that".
      const headline = framing.via === "scene" ? "the whole model" : NOT_IN_CHANGE_LIST;
      mark({ name, headline, box, isolate: null }, true);
      return true;
    },
    setMode(next: PresentationMode): void {
      if (next === mode) return;
      mode = next;
      applyMode();
    },
    setSplit(orientation: SplitOrientation): void {
      if (orientation === split) return;
      split = orientation;
      // The next frame re-derives the rects; clearing the key makes the labels
      // follow rather than stay where the old orientation put them.
      labelKey = "";
    },
    setHeatmap(on: boolean): void {
      if (on === heatmapWanted) return;
      heatmapWanted = on;
      applyHeatmap();
    },
  };
}

type PaneLabels = {
  show(on: boolean): void;
  place(index: number, text: string, pane: Pane): void;
  dispose(): void;
};

/**
 * The two "which version is this" captions. DOM over the canvas, like the
 * callout: crisp at any pixel ratio, no texture memory, and never occluded by
 * the model. An unlabelled split is a guessing game, and a reviewer who guesses
 * wrong reads every change backwards.
 */
function createPaneLabels(container: HTMLElement, theme: Theme): PaneLabels {
  const doc = container.ownerDocument;
  const dark = theme === "dark";
  const paper = dark ? "rgba(13,17,23,0.82)" : "rgba(255,255,255,0.88)";
  const ink = dark ? "#e6edf3" : "#1f2328";
  const els: HTMLElement[] = [0, 1].map(() => {
    const el = doc.createElement("div");
    el.style.cssText =
      `position:absolute;z-index:2;pointer-events:none;display:none;padding:2px 8px;border-radius:6px;` +
      `background:${paper};color:${ink};font:11px/1.5 ui-sans-serif,system-ui,sans-serif;white-space:nowrap`;
    container.appendChild(el);
    return el;
  });
  let visible = false;

  return {
    show(on: boolean): void {
      visible = on;
      for (const el of els) el.style.display = on ? "block" : "none";
    },
    place(index: number, text: string, pane: Pane): void {
      const el = els[index];
      if (!el) return;
      el.textContent = text;
      el.style.left = `${pane.css.x + 8}px`;
      el.style.top = `${pane.css.y + 8}px`;
      if (visible) el.style.display = "block";
    },
    dispose(): void {
      for (const el of els) el.remove();
    },
  };
}

function nowMs(): number {
  return typeof performance === "object" && typeof performance.now === "function"
    ? performance.now()
    : Date.now();
}

/**
 * One-line affordance for the blink. Pinned to the bottom of the viewport rather
 * than laid out under it: the viewport is a region of the chrome now, and a hint
 * that took vertical space would shrink the picture it is a hint about.
 */
function blinkHint(container: HTMLElement): HTMLElement {
  const hint = container.ownerDocument.createElement("div");
  hint.style.cssText =
    "position:absolute;left:0;right:0;bottom:0;z-index:1;padding:6px 8px;" +
    "font:12px ui-sans-serif,system-ui,sans-serif;color:#8b949e;pointer-events:none";
  // Deliberately mode-neutral: in side-by-side, Space swaps the panes rather
  // than revealing something you couldn't see.
  hint.textContent = "Click the view, then hold Space to swap the versions.";
  container.appendChild(hint);
  return hint;
}

function unitBox(): THREE.Box3 {
  return new THREE.Box3(new THREE.Vector3(-1, -1, -1), new THREE.Vector3(1, 1, 1));
}

/**
 * Fallback view: one unit box per scene node, placed by its transform and tinted
 * by its change kind (removed boxes translucent). Shown when the real model
 * can't be decoded — the caller owns the banner that says why.
 */
export function mountBoxScene(
  container: HTMLElement,
  nodes: SceneNode[],
  theme: Theme = "light",
): SceneHandle {
  const viewport = createViewport(container, theme);

  const boxGeom = new THREE.BoxGeometry(1, 1, 1);
  const group = new THREE.Group();
  const bounds = new THREE.Box3();

  for (const n of nodes) {
    const removed = n.kind === "removed";
    const mat = new THREE.MeshStandardMaterial({
      color: n.color,
      metalness: 0.1,
      roughness: 0.7,
      transparent: removed,
      opacity: removed ? 0.45 : 1,
    });
    const mesh = new THREE.Mesh(boxGeom, mat);
    mesh.position.set(n.position[0], n.position[1], n.position[2]);
    mesh.rotation.set(
      deg2rad(n.rotationEulerDeg[0]),
      deg2rad(n.rotationEulerDeg[1]),
      deg2rad(n.rotationEulerDeg[2]),
    );
    mesh.scale.set(n.scale[0], n.scale[1], n.scale[2]);
    group.add(mesh);
    bounds.expandByObject(mesh);
  }
  viewport.scene.add(group);

  const flyTo = createFlyTo(viewport.camera, viewport.controls);
  flyTo.snap(bounds.isEmpty() ? unitBox() : bounds);
  viewport.onFrame((nowMs) => flyTo.update(nowMs));

  return {
    dispose(): void {
      viewport.dispose();
      disposeTree(group);
      boxGeom.dispose();
    },
  };
}
