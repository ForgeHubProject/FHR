import { defineRenderer, renderDiffTree } from "@fhr/renderer-sdk";
import type { MountProps } from "@fhr/types";

// Replaced at bundle-build time with the release's short commit SHA (see build.mjs).
declare const __BUILD__: string;

// The 3D scene lives in a separate, heavier chunk (it inlines three.js). This
// lite bundle stays tiny and loads the chunk on demand — so viewing the change
// tree never pays for three.js. Types are declared locally so referencing them
// can't pull three.js into this bundle.
type SceneHandle = { dispose(): void };
type SceneChunk = { mount3d(el: HTMLElement, props: MountProps): Promise<SceneHandle> };

// Resolve the 3D chunk as a sibling of this module's URL. Built as a string
// (not `new URL(literal, ...)`) so esbuild leaves it a runtime dynamic import
// rather than trying to bundle three.js in here.
function chunkUrl(): string {
  return import.meta.url.replace(/[^/]*(?:\?.*)?$/, "renderer-gltf-scene-3d.js");
}
let chunkPromise: Promise<SceneChunk> | null = null;
function loadChunk(): Promise<SceneChunk> {
  if (!chunkPromise) chunkPromise = import(/* @vite-ignore */ chunkUrl()) as Promise<SceneChunk>;
  return chunkPromise;
}

/**
 * Reference renderer for glTF/GLB scene diffs. The change-tree view is the
 * always-available default; the interactive 3D scene loads on demand behind the
 * same mount() contract. three.js is this renderer's private choice of how to
 * draw its scene — it is not a shared FHR contract.
 */
export default defineRenderer({
  handlerId: "gltf-scene",
  extensions: [".gltf", ".glb"],
  build: __BUILD__,
  render(container: HTMLElement, props: MountProps) {
    // `view` (a single snapshot) goes straight to the 3D scene.
    if (props.mode === "view") {
      return mount3DInto(container, props);
    }
    // diff + merge render the change tree; diff additionally offers the scene.
    renderDiffTree(container, props);
    if (props.mode === "diff") {
      return attachView3DToggle(container, props);
    }
    return;
  },
});

// Load + mount the 3D scene into a host under `container`. Returns a cleanup
// that disposes the scene even if it finishes loading after teardown.
function mount3DInto(container: HTMLElement, props: MountProps): () => void {
  const doc = container.ownerDocument;
  let handle: SceneHandle | null = null;
  let disposed = false;

  const status = doc.createElement("div");
  status.style.cssText = "padding:12px 4px;font:13px ui-sans-serif,system-ui;color:#8b949e";
  status.textContent = "Loading 3D scene…";
  const host = doc.createElement("div");
  host.style.cssText = "width:100%;height:420px;border-radius:8px;overflow:hidden";
  container.append(status, host);

  loadChunk()
    .then((chunk) => chunk.mount3d(host, props))
    .then((h) => {
      status.remove();
      if (disposed) h.dispose();
      else handle = h;
    })
    .catch((err) => {
      status.textContent = "3D scene failed to load: " + errText(err);
    });

  return () => {
    disposed = true;
    handle?.dispose();
  };
}

// A "View in 3D" toggle appended under the change tree. Loads the scene on first
// click, toggles it off on the next. Returns a cleanup disposing any live scene.
function attachView3DToggle(container: HTMLElement, props: MountProps): () => void {
  const doc = container.ownerDocument;
  let handle: SceneHandle | null = null;
  let loading = false;
  let disposed = false;

  const bar = doc.createElement("div");
  bar.style.cssText = "padding:10px 4px 4px";
  const btn = doc.createElement("button");
  btn.textContent = "View in 3D";
  btn.style.cssText =
    "font:12px ui-sans-serif,system-ui;padding:5px 12px;border-radius:6px;border:1px solid #d0d7de;background:transparent;color:inherit;cursor:pointer";
  const host = doc.createElement("div");
  bar.appendChild(btn);
  container.append(bar, host);

  btn.addEventListener("click", () => {
    if (loading) return;
    if (handle) {
      handle.dispose();
      handle = null;
      host.replaceChildren();
      host.removeAttribute("style");
      btn.textContent = "View in 3D";
      return;
    }
    loading = true;
    btn.textContent = "Loading…";
    host.style.cssText = "width:100%;height:420px;margin-top:8px;border-radius:8px;overflow:hidden";
    loadChunk()
      .then((chunk) => chunk.mount3d(host, props))
      .then((h) => {
        loading = false;
        if (disposed) h.dispose();
        else {
          handle = h;
          btn.textContent = "Hide 3D";
        }
      })
      .catch((err) => {
        loading = false;
        btn.textContent = "View in 3D";
        host.textContent = "3D failed: " + errText(err);
      });
  });

  return () => {
    disposed = true;
    handle?.dispose();
  };
}

function errText(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
