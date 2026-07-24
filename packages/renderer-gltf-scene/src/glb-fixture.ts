// Test-only glTF/GLB builder. Not imported by any bundle entry — it exists so
// the tests can exercise the real GLTFLoader on real bytes (parsing needs no
// WebGL, only a DOM-free ArrayBuffer), and so the preflight matrix is tested
// against documents shaped exactly like the ones it will meet in production.

export type FixtureNode = {
  name?: string;
  mesh?: number;
  children?: number[];
  translation?: [number, number, number];
  rotation?: [number, number, number, number];
  scale?: [number, number, number];
};

export type FixtureSpec = {
  /** Nodes, in document order. Default: a single named mesh node. */
  nodes?: FixtureNode[];
  /** Scene root node indices. Default: every node that is nobody's child. */
  sceneNodes?: number[];
  /** Primitives on mesh 0 (>1 makes the loader build a Group of Meshes). */
  primitives?: number;
  extensionsRequired?: string[];
  extensionsUsed?: string[];
  /** Point buffer 0 at a sibling file instead of embedding it. */
  externalBufferUri?: string;
  /** Images that live in sibling files. */
  externalImageUris?: string[];
  /** Add an image whose bytes live in the file itself (bufferView). */
  embeddedImage?: boolean;
  sceneName?: string;
};

export type Fixture = { json: Record<string, unknown>; bin: Uint8Array };

const TRIANGLE = new Float32Array([0, 0, 0, 1, 0, 0, 0, 1, 0]);

/** Build a glTF document (plus its BIN payload) for one triangle mesh. */
export function buildGltf(spec: FixtureSpec = {}): Fixture {
  const nodes: FixtureNode[] = spec.nodes ?? [{ name: "Cube", mesh: 0, translation: [0, 0, 0] }];
  const childIndices = new Set<number>();
  for (const n of nodes) for (const c of n.children ?? []) childIndices.add(c);
  const sceneNodes = spec.sceneNodes ?? nodes.map((_, i) => i).filter((i) => !childIndices.has(i));

  const bin = new Uint8Array(TRIANGLE.buffer.slice(0));
  const primitiveCount = spec.primitives ?? 1;
  const primitives = Array.from({ length: primitiveCount }, () => ({
    attributes: { POSITION: 0 },
    material: 0,
  }));

  const json: Record<string, unknown> = {
    asset: { version: "2.0", generator: "fhr test fixture" },
    scene: 0,
    scenes: [{ name: spec.sceneName ?? "Scene", nodes: sceneNodes }],
    nodes,
    meshes: [{ name: "Tri", primitives }],
    materials: [{ name: "Mat", pbrMetallicRoughness: { baseColorFactor: [0.8, 0.8, 0.8, 1] } }],
    accessors: [
      { bufferView: 0, componentType: 5126, count: 3, type: "VEC3", min: [0, 0, 0], max: [1, 1, 0] },
    ],
    bufferViews: [{ buffer: 0, byteOffset: 0, byteLength: bin.byteLength }],
    buffers: [
      spec.externalBufferUri
        ? { uri: spec.externalBufferUri, byteLength: bin.byteLength }
        : { byteLength: bin.byteLength },
    ],
  };

  const images: Record<string, unknown>[] = [];
  for (const uri of spec.externalImageUris ?? []) images.push({ uri });

  let payload = bin;
  if (spec.embeddedImage) {
    // A 1×1 PNG kept in the file's own bytes (bufferView), not a sibling file.
    const views = json["bufferViews"] as Record<string, unknown>[];
    const offset = align4(bin.byteLength);
    views.push({ buffer: 0, byteOffset: offset, byteLength: PNG_1X1.byteLength });
    images.push({ bufferView: views.length - 1, mimeType: "image/png" });
    payload = new Uint8Array(offset + PNG_1X1.byteLength);
    payload.set(bin, 0);
    payload.set(PNG_1X1, offset);
    (json["buffers"] as Record<string, unknown>[])[0]!["byteLength"] = payload.byteLength;
  }

  if (images.length > 0) json["images"] = images;
  if (spec.extensionsRequired) json["extensionsRequired"] = spec.extensionsRequired;
  if (spec.extensionsUsed) json["extensionsUsed"] = spec.extensionsUsed;
  return { json, bin: payload };
}

const PNG_1X1 = new Uint8Array([
  0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
  0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
  0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
  0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
  0x42, 0x60, 0x82,
]);

const align4 = (n: number): number => n + ((4 - (n % 4)) % 4);

/** Serialise a fixture as .glb (JSON chunk + BIN chunk), the way exporters do. */
export function toGlb(fixture: Fixture): Uint8Array {
  const jsonBytes = new TextEncoder().encode(JSON.stringify(fixture.json));
  const jsonPad = align4(jsonBytes.length) - jsonBytes.length;
  const jsonLen = jsonBytes.length + jsonPad;
  const hasBin = fixture.bin.byteLength > 0 && !("uri" in ((fixture.json["buffers"] as Record<string, unknown>[])[0] ?? {}));
  const binPad = hasBin ? align4(fixture.bin.byteLength) - fixture.bin.byteLength : 0;
  const binLen = hasBin ? fixture.bin.byteLength + binPad : 0;
  const total = 12 + 8 + jsonLen + (hasBin ? 8 + binLen : 0);

  const out = new Uint8Array(total);
  const dv = new DataView(out.buffer);
  dv.setUint32(0, 0x46546c67, true); // "glTF"
  dv.setUint32(4, 2, true);
  dv.setUint32(8, total, true);
  dv.setUint32(12, jsonLen, true);
  dv.setUint32(16, 0x4e4f534a, true); // "JSON"
  out.set(jsonBytes, 20);
  for (let i = 0; i < jsonPad; i++) out[20 + jsonBytes.length + i] = 0x20; // pad with spaces
  if (hasBin) {
    const at = 20 + jsonLen;
    dv.setUint32(at, binLen, true);
    dv.setUint32(at + 4, 0x004e4942, true); // "BIN\0"
    out.set(fixture.bin, at + 8);
  }
  return out;
}

/** Serialise a fixture as .gltf JSON with its buffer embedded as a data: uri. */
export function toGltfJson(fixture: Fixture): Uint8Array {
  const json = { ...fixture.json };
  const buffers = (json["buffers"] as Record<string, unknown>[]).map((b) => ({ ...b }));
  if (!buffers[0]!["uri"]) {
    buffers[0]!["uri"] = `data:application/octet-stream;base64,${base64(fixture.bin)}`;
  }
  json["buffers"] = buffers;
  return new TextEncoder().encode(JSON.stringify(json));
}

/** The ArrayBuffer GLTFLoader.parseAsync requires (never pass it a view). */
export function toArrayBuffer(bytes: Uint8Array): ArrayBuffer {
  return bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer;
}

function base64(bytes: Uint8Array): string {
  let s = "";
  for (const b of bytes) s += String.fromCharCode(b);
  // eslint-disable-next-line no-undef -- btoa exists in browsers and Node ≥16.
  return typeof btoa === "function" ? btoa(s) : Buffer.from(bytes).toString("base64");
}
