// The one place that touches GLTFLoader. Kept thin on purpose: everything the
// renderer *decides* lives in pure modules around it (preflight, mapping,
// overlay), so the interesting logic is testable without a browser.
//
// Two loader contracts that are easy to get wrong and expensive to debug:
//   1. `parse`/`parseAsync` type-check their input with `instanceof ArrayBuffer`.
//      Hand it a Uint8Array and it takes the "this is a JSON string" branch and
//      misparses. Always pass the ArrayBuffer itself.
//   2. Never `loadAsync` a blob: URL for glTF — relative uri resolution against
//      a blob URL is not meaningful. Fetch the bytes yourself and parse them.

import * as THREE from "three";
import { GLTFLoader, type GLTF } from "three/examples/jsm/loaders/GLTFLoader.js";

export type LoadedGltf = GLTF;

export type LoadOptions = {
  /** Attach the meshopt decoder (file uses EXT_meshopt_compression). */
  meshopt?: boolean;
  /** Called with each sub-resource (texture) the loader failed to fetch. */
  onResourceError?: (url: string) => void;
};

export type LoadResult = {
  gltf: LoadedGltf;
  /** True when a meshopt file got its decoder; false when the decoder was refused. */
  meshoptReady: boolean;
};

/**
 * Attach the meshopt decoder, which is a self-contained ES module with the wasm
 * inlined (+~7.5 KB gzip) — no extra asset to host. It still needs to
 * *instantiate* that wasm, which a CSP without `wasm-unsafe-eval` forbids, so
 * both the import and the readiness check are guarded: a refused decoder must
 * degrade to a banner, never to a broken view.
 */
async function attachMeshopt(loader: GLTFLoader): Promise<boolean> {
  try {
    const mod = await import("three/examples/jsm/libs/meshopt_decoder.module.js");
    const decoder = mod.MeshoptDecoder;
    await decoder.ready;
    loader.setMeshoptDecoder(decoder);
    return true;
  } catch {
    return false;
  }
}

/**
 * Parse glTF/GLB bytes into a three.js scene. `bytes` must be the ArrayBuffer,
 * not a view of it (see the note above). Rejects on unreadable files; callers
 * degrade to the scene-graph outline rather than showing nothing.
 */
export async function loadGltf(bytes: ArrayBuffer, opts: LoadOptions = {}): Promise<LoadResult> {
  // A LoadingManager is the only channel through which the loader reports failed
  // sub-resources; without it, missing textures are silently swallowed.
  const manager = new THREE.LoadingManager();
  if (opts.onResourceError) {
    const report = opts.onResourceError;
    manager.onError = (url: string): void => report(url);
  }
  const loader = new GLTFLoader(manager);
  const meshoptReady = opts.meshopt === true ? await attachMeshopt(loader) : false;
  const gltf = await loader.parseAsync(bytes, "");
  return { gltf, meshoptReady };
}

/**
 * Fetch a blob URL as an ArrayBuffer. Returns null when the host didn't serve
 * the bytes (missing url, non-2xx) — the caller shows an honest note instead.
 */
export async function fetchBlobBytes(url: string | undefined): Promise<ArrayBuffer | null> {
  if (!url) return null;
  const res = await fetch(url);
  if (!res.ok) return null;
  return res.arrayBuffer();
}
