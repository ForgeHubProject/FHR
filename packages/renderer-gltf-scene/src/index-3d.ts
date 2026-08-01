// Heavy 3D entry for the gltf-scene renderer. Bundled separately (it inlines
// three.js) so the lite change-tree bundle stays tiny; the lite bundle
// dynamic-imports this chunk only when the viewer opens the 3D scene. This file
// is internal to the gltf-scene bundle — it defines no shared contract.
//
// The whole degradation ladder lives here, and every rung below the top one
// leaves a banner saying what the reviewer is *not* seeing:
//
//   real model + diff paint                       ← the point of #44
//   real model, untextured                          external/undecodable textures
//   real model, no ghost/blink                      base missing, refused or > cap
//   scene-graph outline (a box per node)            Draco/KTX2, sibling .bin, …
//   a sentence explaining why there's no picture    unreadable bytes

import type { ChangeKind, MountProps } from "@fhr/types";
import { decodeGltf, parseGltf, type GltfDocument } from "./gltf-parse.js";
import {
  animationChanges,
  diffChangeTypes,
  materialChanges,
  meshChanges,
  nodeChanges,
} from "./diff-map.js";
import { buildSceneGraph, type SceneNode } from "./scene-graph.js";
import { buildNameIndex, indirectNodeChanges, type IndirectPaint, type NameIndex } from "./node-index.js";
import { buildOverlay, type LoadedSide } from "./model-overlay.js";
import { mountBoxScene, mountModelScene, type SceneHandle } from "./scene-3d.js";
import { fetchBlobBytes, loadGltf } from "./gltf-load.js";
import { preflightGltf, unreadablePreflight, type Preflight } from "./gltf-preflight.js";
import { createBanners, textureFailureMessage, type BannerList } from "./banner.js";
import { allowGhostBase, ghostBaseSkippedMessage } from "./limits.js";
import { emptyKeys, selectionKeys, type SelectionKeys } from "./selection-keys.js";
import { entityPath } from "./change-path.js";
import { availableModes, createModeState, defaultMode } from "./presentation.js";
import { boxSize, defaultSplit, type SplitOrientation } from "./split.js";
import { createChrome, type Chrome } from "./chrome.js";
import type { QueueEntry } from "./queue.js";

/**
 * What the lite bundle wires into the scene: the selection round trip (#45).
 *
 * The two halves speak different keys, on purpose. The lite bundle and the host
 * speak the handler's fully-qualified *paths*, which are what a diff row and a
 * host's selection state are keyed on. The scene speaks *node names*, which is
 * what the overlay's maps are keyed on (model-overlay.ts). This module owns the
 * translation, because `nodeChanges` is the one place that has seen the pairing
 * the handler wrote — so neither half has to re-derive an escaping rule.
 */
export type SceneHooks = {
  /**
   * The viewer picked a change here — by clicking geometry, or from the chrome's
   * own change queue or structure tree. Null means "nothing". The scene has
   * already applied the visuals, so the lite half only has to follow.
   */
  onPick?: (path: string | null) => void;
  /** Change path → the one-line headline its callout shows (see review.ts). */
  headlines?: Record<string, string>;
  /**
   * The review worklist for the change-queue region, formatted by the lite half
   * (queue.ts). Absent or empty means no queue region — "view" mode, or a diff
   * with nothing in it.
   */
  queue?: QueueEntry[];
  /**
   * The queue's ‹ / › asked for the next (+1) or previous (-1) change. Reported,
   * not performed: the live view owns the selection, so a step comes back as a
   * `selectChange` call and the two never disagree about where the reviewer is.
   */
  onStep?: (delta: number) => void;
};

/**
 * Build and mount the 3D view: the real model with the diff painted on, or the
 * most informative fallback we can honestly offer. Returns a disposer (stops the
 * loop, frees GPU resources). The lite bundle awaits this.
 */
export async function mount3d(
  container: HTMLElement,
  props: MountProps,
  hooks: SceneHooks = {},
): Promise<SceneHandle> {
  const doc = container.ownerDocument;
  const theme = props.theme ?? "light";

  // Banners above, viewport filling what's left.
  container.style.display = "flex";
  container.style.flexDirection = "column";
  const banners = createBanners(doc);
  container.appendChild(banners.el);
  const host = doc.createElement("div");
  host.style.cssText = "flex:1 1 auto;min-height:0;position:relative";
  container.appendChild(host);

  // Filled in once the diff has been read; empty for the fallback views, whose
  // handles have no selection to translate anyway.
  let keys: SelectionKeys = emptyKeys();
  // Built only for the real-model view (the fallbacks have no viewport chrome to
  // wrap), and null until then.
  let chrome: Chrome | null = null;
  // Which node rows a mesh/material change lands on, once the file has been
  // indexed. Identity until then: the fallbacks have no structure tree.
  let rowOf: (name: string) => string = (name) => name;

  const withCleanup = (handle: SceneHandle | null): SceneHandle => ({
    dispose(): void {
      handle?.dispose();
      chrome?.dispose();
      banners.el.remove();
      host.remove();
    },
    flyToChange: handle?.flyToChange?.bind(handle),
    flyToChanges: handle?.flyToChanges?.bind(handle),
    selectChange(path: string | null, options?: { fly?: boolean }): boolean {
      return routeSelection({ chrome, keys, handle, rowOf }, path, options);
    },
  });

  const headUrl = props.blobs?.head?.url ?? props.blobs?.base?.url;
  const headBytes = await fetchBlobBytes(headUrl);
  if (!headBytes) {
    note(doc, host, "3D scene needs the file bytes, which this view didn't provide.");
    return withCleanup(null);
  }

  // Pre-flight the JSON before the loader sees it: see gltf-preflight.ts for why
  // looking first is not optional.
  let headDoc: GltfDocument | null = null;
  let preflight: Preflight;
  try {
    headDoc = decodeGltf(new Uint8Array(headBytes));
    preflight = preflightGltf(headDoc);
  } catch (err) {
    preflight = unreadablePreflight(errText(err));
  }
  for (const message of preflight.banners) banners.add(message);

  if (!preflight.canLoadModel || !headDoc) {
    return withCleanup(mountOutline(host, headDoc, props, theme, banners));
  }

  const failedResources: string[] = [];
  let head: LoadedSide;
  try {
    const { gltf, meshoptReady } = await loadGltf(headBytes, {
      meshopt: preflight.needsMeshopt,
      onResourceError: (url) => failedResources.push(url),
    });
    if (preflight.needsMeshopt && !meshoptReady) {
      banners.add(
        "This file's geometry is meshopt-compressed and the decoder was blocked by this page's " +
          "security policy, so some geometry may be missing.",
      );
    }
    head = { gltf, index: buildNameIndex(headDoc) };
  } catch (err) {
    banners.add(
      `The model couldn't be decoded (${errText(err)}). ` +
        "Showing the scene-graph outline instead — the change list below is unaffected.",
    );
    return withCleanup(mountOutline(host, headDoc, props, theme, banners));
  }

  const base = await loadBase(props, banners);
  const changes = nodeChanges(props.diff);
  const meshes = meshChanges(props.diff);
  const materials = materialChanges(props.diff);
  // Mesh and material rows are selection targets too, now that they resolve to
  // geometry: registering them here is what lets clicking one fly the camera,
  // closing the dead end #52 shipped with.
  keys = selectionKeys([...changes, ...meshes, ...materials]);
  // Meshes and materials reach the model indirectly, and animations not at all.
  // All three are passed in: the first two to be painted through the geometry
  // that carries them, the third only to be counted, so a diff whose whole
  // content is unpaintable says so instead of rendering as an unchanged model.
  const overlay = buildOverlay({
    head,
    base,
    changes,
    meshes,
    materials,
    unpaintable: animationChanges(props.diff),
    theme,
  });
  for (const message of overlay.notes) banners.add(message);
  const textureNote = textureFailureMessage(failedResources);
  if (textureNote) banners.add(textureNote);

  // ── the three-region chrome ─────────────────────────────────────────────────
  // The structure tree is `buildSceneGraph`'s output, the same annotated node
  // list the box-scene fallback draws — promoted here from "what you see when
  // the model fails to load" to a region that is always there.
  //
  // `indirect` is what keeps that region honest about the two change classes
  // that reach the model through geometry a node merely carries (#51). It is
  // resolved against the same name index the paint used, so the dot beside a row
  // and the colour on the geometry come from one answer, not two.
  const indirect = indirectPaint(head.index, props);
  const structure = structureRows(headDoc, props, indirect);
  rowOf = (name) => indirect.byChange.get(name)?.[0] ?? name;
  const queue = hooks.queue ?? [];
  // Overlay and side-by-side both need the previous version in hand, so a mount
  // that couldn't load it offers neither rather than offering an empty toggle.
  const modes = availableModes({ bothVersionsResident: overlay.baseSolidGroup !== null });
  const modeState = createModeState({ initial: defaultMode(props.capabilities), available: modes });
  let split: SplitOrientation = defaultSplit(boxSize(overlay.sceneBox));

  let scene: SceneHandle | null = null;
  chrome = createChrome(host, {
    theme,
    modes,
    mode: modeState.mode,
    split,
    structure,
    queue,
    info: viewInfo(structure.length, queue.length, props),
    onMode: (mode) => {
      if (!modeState.set(mode)) return;
      chrome?.setMode(mode);
      scene?.setMode?.(mode);
    },
    onSplit: (orientation) => {
      split = orientation;
      chrome?.setSplit(orientation);
      scene?.setSplit?.(orientation);
    },
    onQueueSelect: (path) => {
      const name = keys.nameOf(path);
      if (name !== null) scene?.selectChange?.(name);
      chrome?.selectChange(path);
      chrome?.highlightNode(name === null ? null : rowOf(name));
      hooks.onPick?.(path);
    },
    onStep: (delta) => hooks.onStep?.(delta),
    onNode: (name) => {
      scene?.frameNode?.(name);
      chrome?.highlightNode(name);
      // A node the diff touched is a queue selection like any other, whether the
      // diff named the node itself or the mesh/material it carries. One the diff
      // doesn't reach at all leaves the queue where it is — there is nothing for
      // it to show, and moving the position would lose the reviewer's place.
      const via = overlay.boxByChangeName.has(name) ? name : indirect.byNode.get(name)?.name;
      if (via === undefined) return;
      const path = keys.pathOf(via);
      chrome?.selectChange(path);
      hooks.onPick?.(path);
    },
  });

  scene = mountModelScene(chrome.viewport, {
    overlay,
    theme,
    mode: modeState.mode,
    split,
    blink: overlay.baseSolidGroup !== null,
    headlines: keys.headlinesByName(hooks.headlines),
    // The tree's rows are nodes and the overlay's keys are change names, so
    // framing a row has to be able to ask which change reaches it — otherwise a
    // node painted through its mesh gets captioned "not in the change list".
    changeOfNode: (name) => indirect.byNode.get(name)?.name ?? null,
    onPick: (name) => {
      const path = name === null ? null : keys.pathOf(name);
      chrome?.selectChange(path);
      chrome?.highlightNode(name === null ? null : rowOf(name));
      hooks.onPick?.(path);
    },
  });
  return withCleanup(scene);
}

/** The three surfaces one selection has to reach. Null before the mount built them. */
export type SelectionSurfaces = {
  chrome: Chrome | null;
  keys: SelectionKeys;
  handle: SceneHandle | null;
  /**
   * Change name → the structure tree's row for it. A mesh or material change is
   * named for the mesh or the material ("BodyMesh"), which is not a row of a
   * tree of *nodes*: highlighting that name clears the tree instead of moving
   * it, so a worklist of #51 changes left the left region inert for its whole
   * length. The mount supplies the resolution; without one — the fallbacks,
   * which have no tree — a name stands for itself, which is what a node change
   * wants anyway.
   */
  rowOf?: (name: string) => string;
};

/**
 * Route a selection that arrived from *outside* the 3D view — a change-tree row,
 * an `n`/`p` step, a host push — to the surfaces that have to agree about it.
 *
 * **Each surface is given the key it is itself keyed on; neither key is derived
 * from the other.** The queue is keyed on `DiffChange.path`, so it gets the path
 * this call was handed, normalised to the object that owns it (a field row
 * "…/translation" selects its stop rather than dropping the position readout).
 * Re-deriving that path from the node name instead is neither total nor
 * injective: it is null for every change with no node behind it — an animation,
 * a material — which would clear the highlight and reset the readout to the size
 * of the job, and it lands on the wrong row whenever a mesh and a node share a
 * name, which exporters produce routinely. The node name is the structure tree's
 * and the scene's key, and only theirs.
 *
 * Exported because mounting the real view needs WebGL, and this routing is what
 * keeps the queue's position honest on every route into it.
 */
export function routeSelection(
  surfaces: SelectionSurfaces,
  path: string | null,
  options?: { fly?: boolean },
): boolean {
  const { chrome, keys, handle, rowOf } = surfaces;
  const name = path === null ? null : keys.nameOf(path);
  chrome?.selectChange(path === null ? null : entityPath(path));
  chrome?.highlightNode(name === null ? null : rowOf?.(name) ?? name);
  if (!handle?.selectChange) return false;
  if (path === null) return handle.selectChange(null, options);
  return name === null ? false : handle.selectChange(name, options);
}

/** The centre's top-left line: what this view is showing, in two numbers. */
function viewInfo(nodes: number, changes: number, props: MountProps): string {
  const parts = [`${nodes} ${nodes === 1 ? "node" : "nodes"}`];
  if (props.mode !== "view") parts.push(`${changes} ${changes === 1 ? "change" : "changes"}`);
  return parts.join(" · ");
}

/**
 * What a node row is annotated with: its own change, or the one that reaches it.
 *
 * `diffChangeTypes` is the node-level changes and nothing else, but since #51
 * two whole classes of change paint the model through something a node merely
 * carries — a mesh several nodes instance, a material a primitive references.
 * Annotating from node changes alone marks that geometry "unchanged" in the
 * region #56 made permanent, beside a viewport that has it painted orange, and
 * `frameNode` then captions it "not in the change list".
 *
 * A node's own change still wins: it is the more specific fact about that node,
 * and it is the one whose path the queue is keyed on.
 */
export function annotationKinds(props: MountProps, indirect: IndirectPaint): Map<string, ChangeKind> {
  const kinds = diffChangeTypes(props.diff);
  for (const [name, change] of indirect.byNode) if (!kinds.has(name)) kinds.set(name, change.kind);
  return kinds;
}

/** Which of this file's nodes the diff's mesh and material changes reach. */
export function indirectPaint(index: NameIndex, props: MountProps): IndirectPaint {
  return indirectNodeChanges(index, meshChanges(props.diff), materialChanges(props.diff));
}

/**
 * The structure tree's rows. A file whose scene graph can't be walked still has
 * a perfectly good model on screen — parseGltf is stricter than the loader — so
 * this degrades to an empty region rather than failing the mount.
 *
 * Exported for the same reason `routeSelection` is: mounting the view needs
 * WebGL, and whether a row admits its change does not.
 */
export function structureRows(
  doc: GltfDocument,
  props: MountProps,
  indirect: IndirectPaint,
): SceneNode[] {
  try {
    return buildSceneGraph(parseGltf(doc), annotationKinds(props, indirect));
  } catch {
    return [];
  }
}

/**
 * Load the previous version, for the ghost underlay, the removed-geometry ghosts
 * and the A/B blink. Every way this can fail is a banner and a null, never a
 * failed mount: the diff painted on the current model is the part that matters.
 */
async function loadBase(props: MountProps, banners: BannerList): Promise<LoadedSide | null> {
  const baseRef = props.blobs?.base;
  const headRef = props.blobs?.head;
  // "view" mode is a single snapshot, and a host may pass the same blob twice.
  if (props.mode === "view" || !baseRef?.url || baseRef.url === headRef?.url) return null;

  // allowGhostBase now refuses only a known size over the cap, so whenever it
  // refuses there is a real number to name and the skip is never silent. The
  // `undefined` check is what narrows the type; it changes no behaviour, since
  // an absent size is allowed.
  const baseSize = baseRef.size;
  if (baseSize !== undefined && !allowGhostBase(baseSize)) {
    banners.add(ghostBaseSkippedMessage(baseSize));
    return null;
  }

  try {
    const bytes = await fetchBlobBytes(baseRef.url);
    if (!bytes) {
      banners.add(baseUnavailable("its bytes weren't served"));
      return null;
    }
    const doc = decodeGltf(new Uint8Array(bytes));
    const preflight = preflightGltf(doc);
    if (!preflight.canLoadModel) {
      banners.add(baseUnavailable(baseReason(preflight)));
      return null;
    }
    const { gltf } = await loadGltf(bytes, { meshopt: preflight.needsMeshopt });
    return { gltf, index: buildNameIndex(doc) };
  } catch (err) {
    banners.add(baseUnavailable(errText(err)));
    return null;
  }
}

function baseReason(preflight: Preflight): string {
  if (preflight.unsupportedExtensions.length > 0) {
    return `it needs ${preflight.unsupportedExtensions.join(", ")}`;
  }
  if (preflight.externalBuffers.length > 0) {
    return `it needs sibling ${preflight.externalBuffers.join(", ")}`;
  }
  return "it couldn't be read as glTF";
}

function baseUnavailable(reason: string): string {
  return (
    `The previous version couldn't be loaded (${reason}), so the ghost overlay, the A/B blink and ` +
    `removed-part ghosts are turned off. Changes on the current version are still painted.`
  );
}

/** The scene-graph outline fallback: a unit box per node, coloured by the diff. */
function mountOutline(
  host: HTMLElement,
  gltfDoc: GltfDocument | null,
  props: MountProps,
  theme: "light" | "dark",
  banners: BannerList,
): SceneHandle | null {
  if (!gltfDoc) {
    if (banners.count() === 0) banners.add("This file couldn't be read as glTF.");
    return null;
  }
  try {
    const entities = parseGltf(gltfDoc);
    // Same annotation as the real view's tree: a fallback that called a
    // mesh-changed node "unchanged" would be wrong in the one view where the
    // outline *is* the whole picture.
    const kinds = annotationKinds(props, indirectPaint(buildNameIndex(gltfDoc), props));
    return mountBoxScene(host, buildSceneGraph(entities, kinds), theme);
  } catch (err) {
    banners.add(`This file's scene graph couldn't be read either (${errText(err)}).`);
    return null;
  }
}

/** A short muted line in place of a picture, styled like the loading status. */
function note(doc: Document, host: HTMLElement, text: string): void {
  const el = doc.createElement("div");
  el.style.cssText = "padding:16px;font:13px ui-sans-serif,system-ui;color:#8b949e";
  el.textContent = text;
  host.appendChild(el);
}

function errText(err: unknown): string {
  const text = err instanceof Error ? err.message : String(err);
  return text.length > 160 ? `${text.slice(0, 157)}…` : text;
}

export default { mount3d };
