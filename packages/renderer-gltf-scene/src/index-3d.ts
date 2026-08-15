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
  geometryChanges,
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
import { createHeatmap } from "./heatmap.js";
import { formatDeviation } from "./deviation.js";
import type { QueueEntry } from "./queue.js";

/**
 * What the lite bundle wires into the scene: the selection round trip (#45).
 *
 * Both halves speak the handler's fully-qualified change *paths* — what a diff
 * row, a host's selection state and (since #47) the overlay's own maps are all
 * keyed on. A name is not a change's identity: one diff can carry a deletion and
 * a rename that share a name and mean different objects, so translating to node
 * names here merged them and lost one. All that is left to reconcile is a
 * selection that names a field of a change (see selection-keys.ts).
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
  // Which structure-tree row a change path lands on, once the file has been
  // indexed. Null until then, and null after it for a change with no row: the
  // fallbacks have no tree at all, and an animation has nothing in one.
  let rowOf: (path: string) => string | null = () => null;

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
  // The two directions between a change and a tree row, both keyed on the
  // change's path (#47) — a name identifies neither end of this any more.
  //
  //   path → row   a node change's row is the node itself; a mesh or material
  //                change's is the first node its geometry reaches.
  //   row → path   the overlay's own answer, resolved against the head model, so
  //                the row that lights up and the geometry that is painted come
  //                from one resolution rather than two. A node's own change wins
  //                over one that only reaches it, which is how the overlay
  //                recorded it.
  const rowByChangePath = new Map<string, string>();
  for (const change of nodeChanges(props.diff)) rowByChangePath.set(change.path, change.name);
  for (const [path, rows] of indirect.byChange) {
    const first = rows[0];
    if (first !== undefined) rowByChangePath.set(path, first);
  }
  rowOf = (path) => rowByChangePath.get(path) ?? null;
  const changePathByRow = new Map<string, string>();
  for (const [nodeIndex, path] of overlay.changePathByNodeIndex) {
    const row = head.index.keyByIndex[nodeIndex];
    if (row !== undefined && !changePathByRow.has(row)) changePathByRow.set(row, path);
  }
  const queue = hooks.queue ?? [];
  // Overlay and side-by-side both need the previous version in hand, so a mount
  // that couldn't load it offers neither rather than offering an empty toggle.
  const modes = availableModes({ bothVersionsResident: overlay.baseSolidGroup !== null });
  const modeState = createModeState({ initial: defaultMode(props.capabilities), available: modes });
  let split: SplitOrientation = defaultSplit(boxSize(overlay.sceneBox));

  // The deviation heatmap (#46). Null — and therefore no toggle and no legend —
  // whenever there is nothing to measure: no vertex-data edit in the diff, no
  // previous version loaded, or no pair of primitives the two files agree on.
  // Nothing is computed here; building it only works out what *could* be
  // measured, and the first toggle pays for the rest.
  const heatmap = createHeatmap({ head, base, geometry: geometryChanges(props.diff) });

  let scene: SceneHandle | null = null;
  chrome = createChrome(host, {
    theme,
    modes,
    mode: modeState.mode,
    split,
    structure,
    queue,
    info: viewInfo(structure.length, queue.length, props),
    heatmap: heatmap !== null,
    onMode: (mode) => {
      if (!modeState.set(mode)) return;
      chrome?.setMode(mode);
      scene?.setMode?.(mode);
    },
    onFrameAll: () => {
      // The selection goes first, and it has to go: a change is *isolated* while
      // it is selected, so framing "everything" with one still on would fly the
      // camera out to a box holding one visible part and a great deal of nothing.
      // It clears down the same route a click on empty space takes — queue, tree,
      // scene, and one `select` event for the host — so the reset leaves all four
      // agreeing rather than leaving the change tree pointing at a change the
      // viewport has stopped showing.
      routeSelection({ chrome, keys, handle: scene, rowOf }, null);
      hooks.onPick?.(null);
      scene?.frameAll?.();
    },
    onHeatmap: (on) => scene?.setHeatmap?.(on),
    onSplit: (orientation) => {
      split = orientation;
      chrome?.setSplit(orientation);
      scene?.setSplit?.(orientation);
    },
    onQueueSelect: (path) => {
      const changePath = keys.changePathOf(path);
      if (changePath !== null) scene?.selectChange?.(changePath);
      chrome?.selectChange(path);
      chrome?.highlightNode(changePath === null ? null : rowOf(changePath));
      hooks.onPick?.(path);
    },
    onStep: (delta) => hooks.onStep?.(delta),
    onNode: (row) => {
      scene?.frameNode?.(row);
      chrome?.highlightNode(row);
      // A node the diff touched is a queue selection like any other, whether the
      // diff named the node itself or the mesh/material it carries. One the diff
      // doesn't reach at all leaves the queue where it is — there is nothing for
      // it to show, and moving the position would lose the reviewer's place.
      const path = changePathByRow.get(row);
      if (path === undefined) return;
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
    headlines: hooks.headlines ?? {},
    // The tree's rows are nodes and a mesh or material change is about geometry
    // a node merely carries, so framing a row has to be able to ask which change
    // reaches it — otherwise a node painted through its mesh gets captioned "not
    // in the change list".
    changeOfNode: (name) => indirect.byNode.get(name)?.path ?? null,
    heatmap,
    // The queue's panel carries the number the heatmap's colours are of, so a
    // reviewer stepping the worklist reads "max deviation 12 mm" without having
    // to find the part on screen and hover it.
    onHeatmap: (summary) => {
      const byPath = new Map<string, string>();
      for (const [path, value] of summary.byPath) byPath.set(path, formatDeviation(value));
      chrome?.setDeviations(byPath);
    },
    onPick: (path) => {
      chrome?.selectChange(path);
      chrome?.highlightNode(path === null ? null : rowOf(path));
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
   * Change path → the structure tree's row for it, or null when the tree has
   * none. A mesh or material change is named for the mesh or the material
   * ("BodyMesh"), which is not a row of a tree of *nodes*: highlighting that
   * name clears the tree instead of moving it, so a worklist of #51 changes left
   * the left region inert for its whole length. The mount supplies the
   * resolution, and only the mount can: it takes both directions from the name
   * index the paint itself resolved through. Without one — the fallbacks, which
   * have no tree — there is nothing to highlight and nothing to guess from.
   */
  rowOf?: (path: string) => string | null;
};

/**
 * Route a selection that arrived from *outside* the 3D view — a change-tree row,
 * an `n`/`p` step, a host push — to the surfaces that have to agree about it.
 *
 * **Each surface is given the key it is itself keyed on; neither key is derived
 * from the other.** The queue and the scene both work in `DiffChange.path`, so
 * both get the path — the queue the one this call was handed, the scene that
 * path resolved to a change this diff actually carries (a field row
 * "…/translation" selects its stop rather than dropping the position readout).
 * Only the structure tree works in node names, because its rows are the head
 * file's nodes, and `rowOf` is the one thing that may translate.
 *
 * A path is never re-derived from a name in either direction. That derivation is
 * neither total nor injective: it is null for every change with no node behind
 * it — an animation, a material — which would clear the highlight and reset the
 * readout to the size of the job; it lands on the wrong row whenever a mesh and
 * a node share a name, which exporters produce routinely; and since #47 it
 * cannot even tell two changes apart, because a deletion and a rename into the
 * name it vacated share a name and mean different objects.
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
  const changePath = path === null ? null : keys.changePathOf(path);
  chrome?.selectChange(path === null ? null : entityPath(path));
  chrome?.highlightNode(changePath === null ? null : rowOf?.(changePath) ?? null);
  if (!handle?.selectChange) return false;
  if (path === null) return handle.selectChange(null, options);
  return changePath === null ? false : handle.selectChange(changePath, options);
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
