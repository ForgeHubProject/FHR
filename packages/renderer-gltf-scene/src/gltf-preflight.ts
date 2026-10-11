// Pre-flight for the real-model path: decide from the glTF JSON *alone* whether
// GLTFLoader can be trusted with these bytes, and what the reviewer must be told.
//
// Why look before we load: GLTFLoader's failure modes are asymmetric. A missing
// external `.bin` hard-rejects the whole parse, but a missing external *texture*
// is swallowed inside the loader and degrades silently — the reviewer would be
// shown an untextured model with no hint that anything was missing. And for
// Draco/KTX2 we must not call the loader at all: the decoders can't be shipped
// here (no .wasm host, no `wasm-unsafe-eval` in either host's CSP — see #40's
// non-goals), so the honest answer is the scene-graph outline plus a banner.
//
// Pure: JSON in, verdict + plain-language messages out. No three.js, no DOM.

import type { GltfDocument } from "./gltf-parse.js";

/** Why the real-model path was refused (`"none"` = go ahead and load it). */
export type Degradation = "none" | "external-buffer" | "unsupported-extension" | "unreadable";

export type Preflight = {
  /** May we hand these bytes to GLTFLoader? False → draw the scene-graph outline. */
  canLoadModel: boolean;
  degradation: Degradation;
  /** Plain-language messages for the reviewer. Empty = nothing worth saying. */
  banners: string[];
  /** Buffer uris that live in sibling files we cannot fetch. */
  externalBuffers: string[];
  /** Image uris that live in sibling files we cannot fetch. */
  externalImages: string[];
  /** `extensionsRequired` verbatim. */
  requiredExtensions: string[];
  /** Required extensions this viewer cannot honour (Draco, KTX2, …). */
  unsupportedExtensions: string[];
  /** File uses EXT_meshopt_compression → attach the meshopt decoder. */
  needsMeshopt: boolean;
};

/** EXT_meshopt_compression: supported, but only by opting the decoder in. */
export const MESHOPT = "EXT_meshopt_compression";

/**
 * Extensions three's GLTFLoader handles with no extra decoder or asset. Anything
 * outside this set that a file *requires* means we cannot render the real model.
 * (Unknown-but-only-*used* extensions are fine: glTF requires a file to be
 * readable without them, and the loader ignores what it doesn't know.)
 */
export const SUPPORTED_EXTENSIONS: readonly string[] = [
  "EXT_mesh_gpu_instancing",
  MESHOPT,
  "EXT_texture_avif",
  "EXT_texture_webp",
  "KHR_animation_pointer",
  "KHR_lights_punctual",
  "KHR_materials_anisotropy",
  "KHR_materials_bump",
  "KHR_materials_clearcoat",
  "KHR_materials_dispersion",
  "KHR_materials_emissive_strength",
  "KHR_materials_ior",
  "KHR_materials_iridescence",
  "KHR_materials_sheen",
  "KHR_materials_specular",
  "KHR_materials_transmission",
  "KHR_materials_unlit",
  "KHR_materials_variants",
  "KHR_materials_volume",
  "KHR_mesh_quantization",
  "KHR_texture_transform",
  // Metadata-only: nothing to decode, safe to ignore.
  "KHR_xmp_json_ld",
  "KHR_xmp",
];

/** Friendly names for the extensions we expect to have to refuse. */
const EXTENSION_LABEL: Record<string, string> = {
  KHR_draco_mesh_compression: "Draco-compressed geometry",
  KHR_texture_basisu: "KTX2/Basis-compressed textures",
  EXT_texture_ktx2: "KTX2-compressed textures",
};

function describeExtension(id: string): string {
  const label = EXTENSION_LABEL[id];
  return label ? `${label} (${id})` : id;
}

/** A uri that resolves to bytes we already hold (embedded), rather than a sibling file. */
function isEmbedded(uri: string | undefined): boolean {
  if (uri === undefined || uri === "") return true; // GLB BIN chunk
  return /^data:/i.test(uri);
}

/** "textures/wheel.png" → "wheel.png"; keeps banners short and readable. */
function baseName(uri: string): string {
  const noQuery = uri.split(/[?#]/)[0] ?? uri;
  const parts = noQuery.split("/");
  return decodeSafe(parts[parts.length - 1] || noQuery);
}

function decodeSafe(s: string): string {
  try {
    return decodeURIComponent(s);
  } catch {
    return s;
  }
}

/** Join up to `max` names for a message, e.g. "a.bin, b.bin and 2 more". */
function listNames(names: string[], max = 3): string {
  const shown = names.slice(0, max);
  const rest = names.length - shown.length;
  const head = shown.join(", ");
  return rest > 0 ? `${head} and ${rest} more` : head;
}

/**
 * Classify a decoded glTF document for the real-model path. See the table in
 * PR #44 / issue #44 for the policy each branch implements.
 */
export function preflightGltf(doc: GltfDocument): Preflight {
  const requiredExtensions = doc.extensionsRequired ?? [];
  const usedExtensions = doc.extensionsUsed ?? [];
  const supported = new Set(SUPPORTED_EXTENSIONS);

  const unsupportedExtensions = requiredExtensions.filter((e) => !supported.has(e));
  const needsMeshopt = requiredExtensions.includes(MESHOPT) || usedExtensions.includes(MESHOPT);

  const externalBuffers = (doc.buffers ?? [])
    .filter((b) => !isEmbedded(b.uri))
    .map((b) => baseName(b.uri as string));
  const externalImages = (doc.images ?? [])
    .filter((img) => img.bufferView === undefined && !isEmbedded(img.uri))
    .map((img) => baseName(img.uri as string));

  const banners: string[] = [];

  // Refusals first — they decide `degradation`, and their banner should lead.
  let degradation: Degradation = "none";
  if (unsupportedExtensions.length > 0) {
    degradation = "unsupported-extension";
    banners.push(
      `This file needs ${listNames(unsupportedExtensions.map(describeExtension))}, which this viewer can't decode. ` +
        `Showing the scene-graph outline instead — the change list below is unaffected.`,
    );
  } else if (externalBuffers.length > 0) {
    degradation = "external-buffer";
    banners.push(
      `This file keeps its geometry in a separate file (needs sibling ${listNames(externalBuffers)}), which a review view can't fetch. ` +
        `Showing the scene-graph outline instead — the change list below is unaffected.`,
    );
  }

  // Warnings — these still allow the real model to render.
  if (degradation === "none" && externalImages.length > 0) {
    const n = externalImages.length;
    banners.push(
      `${n} ${n === 1 ? "texture lives" : "textures live"} in separate files (${listNames(externalImages)}) that a review view can't fetch, ` +
        `so the model is shown untextured. Geometry and the change list are unaffected.`,
    );
  }

  return {
    canLoadModel: degradation === "none",
    degradation,
    banners,
    externalBuffers,
    externalImages,
    requiredExtensions,
    unsupportedExtensions,
    needsMeshopt,
  };
}

/** Preflight verdict for a document we couldn't even decode. */
export function unreadablePreflight(reason: string): Preflight {
  return {
    canLoadModel: false,
    degradation: "unreadable",
    banners: [`This file couldn't be read as glTF (${reason}).`],
    externalBuffers: [],
    externalImages: [],
    requiredExtensions: [],
    unsupportedExtensions: [],
    needsMeshopt: false,
  };
}
