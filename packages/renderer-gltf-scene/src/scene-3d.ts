// The gltf-scene renderer's OWN 3D scene. three.js is this renderer's private
// choice for drawing its picture — it is NOT a shared FHR contract and nothing
// here is reused by other formats' renderers. The only FHR contracts are
// mount() and StructuredDiff; everything in this file lives inside this one
// bundle. A different 3D format's renderer is free to draw however it likes.
//
// Two views live here, behind one viewport:
//   * mountModelScene — the real model with the diff painted on (#44)
//   * mountBoxScene   — a unit box per node, the honest fallback for when the
//                       real model can't be decoded (always paired with a banner)
//
// Requires a DOM + WebGL context (i.e. a real browser). Everything decided
// *about* the picture — mapping, grammar, framing, budgets — lives in the pure
// modules this file calls, which are tested headlessly.

import * as THREE from "three";
import { OrbitControls } from "three/examples/jsm/controls/OrbitControls.js";
import type { SceneNode } from "./scene-graph.js";
import type { Overlay } from "./model-overlay.js";
import { createFlyTo, DEFAULT_FLY_MS } from "./flyto.js";
import { disposeTree } from "./dispose.js";
import { createIsolator } from "./isolate.js";
import { createCallout, projectToScreen } from "./callout.js";
import { changeAtHits, isClickGesture, ndcFromPointer } from "./pick.js";

type Theme = "light" | "dark";

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
};

const deg2rad = (d: number): number => (d * Math.PI) / 180;

type Viewport = {
  scene: THREE.Scene;
  camera: THREE.PerspectiveCamera;
  renderer: THREE.WebGLRenderer;
  controls: OrbitControls;
  /** Run `cb` once per frame, before the controls update and the draw. */
  onFrame(cb: (nowMs: number) => void): void;
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
  scene.background = new THREE.Color(theme === "dark" ? 0x0d1117 : 0xf6f8fa);

  const camera = new THREE.PerspectiveCamera(50, width / height, 0.1, 5000);

  const renderer = new THREE.WebGLRenderer({ antialias: true });
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

  let raf = 0;
  let alive = true;
  const tick = (nowMs: number): void => {
    if (!alive) return;
    raf = requestAnimationFrame(tick);
    for (const cb of frameCallbacks) cb(nowMs);
    controls.update();
    renderer.render(scene, camera);
  };
  raf = requestAnimationFrame(tick);

  // Resize: the container is laid out by the host, so its size can change
  // without the window's. ResizeObserver catches that; the window listener is
  // the fallback where the observer isn't available.
  const resize = (): void => {
    const w = container.clientWidth || width;
    const h = container.clientHeight || height;
    if (w === 0 || h === 0) return;
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
};

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

  // ── A/B blink ───────────────────────────────────────────────────────────────
  // Both versions are already resident, so the swap is a `visible` toggle and
  // lands in the very next frame. That matters: the change-blindness literature
  // is unambiguous that a blank or slow intermediate frame destroys the
  // detection advantage the blink exists to provide.
  const canBlink = options.blink === true && overlay.baseSolidGroup !== null;
  const showBase = (on: boolean): void => {
    overlay.headGroup.visible = !on;
    if (overlay.baseGhostGroup) overlay.baseGhostGroup.visible = !on;
    if (overlay.removedGroup) overlay.removedGroup.visible = !on;
    if (overlay.movedGroup) overlay.movedGroup.visible = !on;
    if (overlay.baseSolidGroup) overlay.baseSolidGroup.visible = on;
  };

  const canvas = viewport.renderer.domElement;
  if (canBlink) {
    // Compile both blink states up front, for the same "no slow frame" reason: a
    // material first seen mid-blink would compile its shader on that frame.
    showBase(true);
    try {
      viewport.renderer.compile(viewport.scene, viewport.camera);
    } catch {
      // compile() is an optimisation; a failure here must not break mounting.
    }
    showBase(false);

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
  let selectedBox: THREE.Box3 | null = null;
  const anchor = new THREE.Vector3();

  const clearSelection = (): void => {
    isolator.clear();
    callout.hide();
    selectedBox = null;
  };

  const applySelection = (name: string | null, fly: boolean): boolean => {
    if (name === null) {
      clearSelection();
      return true;
    }
    const box = overlay.boxByChangeName.get(name);
    const objects = overlay.objectsByChangeName.get(name);
    if (!box || box.isEmpty() || !objects || objects.length === 0) {
      clearSelection();
      return false;
    }
    isolator.isolate(objects);
    callout.show(name, options.headlines?.[name] ?? "changed");
    selectedBox = box;
    if (fly) flyTo.to(box);
    return true;
  };

  // The callout follows the geometry: one projection and two style writes per
  // frame, and only while something is selected.
  viewport.onFrame(() => {
    if (!selectedBox || !callout.visible) return;
    selectedBox.getCenter(anchor);
    const width = container.clientWidth || 1;
    const height = container.clientHeight || 1;
    callout.place(projectToScreen(anchor, viewport.camera, { width, height }), { width, height });
  });

  // ── picking ─────────────────────────────────────────────────────────────────
  // Pick against the painted layers only: the ghost of the previous version is
  // context, and clicking through it to the current model is what a reviewer
  // means. OrbitControls owns dragging, so a press that travelled is not a click.
  const raycaster = new THREE.Raycaster();
  const pickTargets: THREE.Object3D[] = [overlay.headGroup];
  if (overlay.removedGroup) pickTargets.push(overlay.removedGroup);
  if (overlay.movedGroup) pickTargets.push(overlay.movedGroup);

  let pressed: { x: number; y: number; t: number } | null = null;
  const onPointerDown = (event: PointerEvent): void => {
    pressed = { x: event.clientX, y: event.clientY, t: nowMs() };
  };
  const onPointerUp = (event: PointerEvent): void => {
    const down = pressed;
    pressed = null;
    if (!down) return;
    if (!isClickGesture(down, { x: event.clientX, y: event.clientY, t: nowMs() })) return;

    const rect = canvas.getBoundingClientRect();
    const ndc = ndcFromPointer(event, rect);
    raycaster.setFromCamera(new THREE.Vector2(ndc.x, ndc.y), viewport.camera);
    const hits = raycaster.intersectObjects(pickTargets, true);
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

  viewport.onDispose(() => {
    viewport.controls.removeEventListener("start", onControlStart);
    canvas.removeEventListener("pointerdown", onPointerDown as EventListener);
    canvas.removeEventListener("pointerup", onPointerUp as EventListener);
    callout.dispose();
    isolator.clear();
    hint?.remove();
  });

  return {
    dispose(): void {
      viewport.dispose();
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
  };
}

function nowMs(): number {
  return typeof performance === "object" && typeof performance.now === "function"
    ? performance.now()
    : Date.now();
}

/** One-line affordance for the blink; same muted style as the view's status text. */
function blinkHint(container: HTMLElement): HTMLElement {
  const hint = container.ownerDocument.createElement("div");
  hint.style.cssText =
    "padding:6px 2px 0;font:12px ui-sans-serif,system-ui,sans-serif;color:#8b949e;pointer-events:none";
  hint.textContent = "Click the view, then hold Space to see the previous version.";
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
