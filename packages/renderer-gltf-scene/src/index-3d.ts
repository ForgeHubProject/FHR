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

import type { MountProps } from "@fhr/types";
import { decodeGltf, parseGltf, type GltfDocument } from "./gltf-parse.js";
import { diffChangeTypes, nodeChanges } from "./diff-map.js";
import { buildSceneGraph } from "./scene-graph.js";
import { buildNameIndex } from "./node-index.js";
import { buildOverlay, type LoadedSide } from "./model-overlay.js";
import { mountBoxScene, mountModelScene, type SceneHandle } from "./scene-3d.js";
import { fetchBlobBytes, loadGltf } from "./gltf-load.js";
import { preflightGltf, unreadablePreflight, type Preflight } from "./gltf-preflight.js";
import { createBanners, textureFailureMessage, type BannerList } from "./banner.js";
import { allowGhostBase, ghostBaseSkippedMessage } from "./limits.js";

/**
 * Build and mount the 3D view: the real model with the diff painted on, or the
 * most informative fallback we can honestly offer. Returns a disposer (stops the
 * loop, frees GPU resources). The lite bundle awaits this.
 */
export async function mount3d(container: HTMLElement, props: MountProps): Promise<SceneHandle> {
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

  const withCleanup = (handle: SceneHandle | null): SceneHandle => ({
    dispose(): void {
      handle?.dispose();
      banners.el.remove();
      host.remove();
    },
    flyToChange: handle?.flyToChange?.bind(handle),
    flyToChanges: handle?.flyToChanges?.bind(handle),
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
  const overlay = buildOverlay({ head, base, changes, theme });
  for (const message of overlay.notes) banners.add(message);
  const textureNote = textureFailureMessage(failedResources);
  if (textureNote) banners.add(textureNote);

  return withCleanup(mountModelScene(host, { overlay, theme, blink: overlay.baseSolidGroup !== null }));
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

  if (!allowGhostBase(baseRef.size)) {
    if (baseRef.size > 0) banners.add(ghostBaseSkippedMessage(baseRef.size));
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
    return mountBoxScene(host, buildSceneGraph(entities, diffChangeTypes(props.diff)), theme);
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
