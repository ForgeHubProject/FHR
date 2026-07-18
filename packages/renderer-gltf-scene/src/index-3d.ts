// Heavy 3D entry for the gltf-scene renderer. Bundled separately (it inlines
// three.js) so the lite change-tree bundle stays tiny; the lite bundle
// dynamic-imports this chunk only when the viewer opens the 3D scene. This file
// is internal to the gltf-scene bundle — it defines no shared contract.

import type { MountProps } from "@fhr/types";
import { entitiesFromBlob } from "./gltf-parse.js";
import { diffChangeTypes } from "./diff-map.js";
import { buildSceneGraph } from "./scene-graph.js";
import { mountScene, type SceneHandle } from "./scene-3d.js";

async function loadHeadBytes(props: MountProps): Promise<Uint8Array | null> {
  const url = props.blobs?.head?.url ?? props.blobs?.base?.url;
  if (!url) return null;
  const res = await fetch(url);
  if (!res.ok) return null;
  return new Uint8Array(await res.arrayBuffer());
}

/**
 * Build and mount the 3D scene for the head blob, coloured by the diff. Returns
 * a disposer (stop the loop + free GPU resources), or renders a short message
 * if the blob bytes aren't available. The lite bundle awaits this.
 */
export async function mount3d(container: HTMLElement, props: MountProps): Promise<SceneHandle> {
  const theme = props.theme ?? "light";
  const bytes = await loadHeadBytes(props);
  if (!bytes) {
    const note = container.ownerDocument.createElement("div");
    note.style.cssText = "padding:16px;font:13px ui-sans-serif,system-ui;color:#8b949e";
    note.textContent = "3D scene needs the file bytes, which this view didn't provide.";
    container.appendChild(note);
    return { dispose() { note.remove(); } };
  }
  const entities = entitiesFromBlob(bytes);
  const changeMap = diffChangeTypes(props.diff);
  return mountScene(container, buildSceneGraph(entities, changeMap), theme);
}

export default { mount3d };
