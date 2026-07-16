// glTF/GLB → scene-entity parser for the renderer. This is *display* parsing
// (turning a blob into a renderable node graph), not authoritative diffing —
// the semantic diff still comes from the handler. Ported from ForgeHub's
// apps/api/src/gltf-parser.ts so the rendered scene matches what the server
// ingests, with .glb binary-container support added.

export type Transform = {
  position: [number, number, number];
  rotationEulerDeg: [number, number, number];
  scale: [number, number, number];
};

export type Entity = {
  id: string;            // renderer tree id (== entityId here)
  entityId: string;
  parentEntityId: string | null;
  kind: string;          // "assembly" | "module" | "part"
  name: string;
  path: string;
  transform: Transform | null;
};

type GltfNode = {
  name?: string;
  children?: number[];
  mesh?: number;
  translation?: [number, number, number];
  rotation?: [number, number, number, number];
  scale?: [number, number, number];
};
type GltfScene = { nodes?: number[]; name?: string };
type GltfDocument = {
  asset?: { version: string };
  scene?: number;
  scenes?: GltfScene[];
  nodes?: GltfNode[];
};

function slugify(name: string): string {
  return name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "") || "node";
}

function quatToEulerDeg(q: [number, number, number, number]): [number, number, number] {
  const [x, y, z, w] = q;
  const roll = Math.atan2(2 * (w * x + y * z), 1 - 2 * (x * x + y * y));
  const pitch = Math.asin(Math.max(-1, Math.min(1, 2 * (w * y - z * x))));
  const yaw = Math.atan2(2 * (w * z + x * y), 1 - 2 * (y * y + z * z));
  const toDeg = (r: number) => r * (180 / Math.PI);
  return [toDeg(roll), toDeg(pitch), toDeg(yaw)];
}

/** Decode a .gltf (JSON) or .glb (binary container) blob into a glTF document. */
export function decodeGltf(bytes: Uint8Array): GltfDocument {
  // .glb: 12-byte header (magic "glTF"), then length-prefixed chunks; the first
  // chunk (type "JSON") holds the document.
  const GLB_MAGIC = 0x46546c67; // "glTF" little-endian
  if (bytes.length >= 12) {
    const dv = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    if (dv.getUint32(0, true) === GLB_MAGIC) {
      let offset = 12;
      while (offset + 8 <= bytes.length) {
        const chunkLen = dv.getUint32(offset, true);
        const chunkType = dv.getUint32(offset + 4, true);
        const dataStart = offset + 8;
        if (chunkType === 0x4e4f534a) { // "JSON"
          const json = new TextDecoder().decode(bytes.subarray(dataStart, dataStart + chunkLen));
          return JSON.parse(json) as GltfDocument;
        }
        offset = dataStart + chunkLen;
      }
      throw new Error("GLB has no JSON chunk");
    }
  }
  return JSON.parse(new TextDecoder().decode(bytes)) as GltfDocument;
}

/** Walk a glTF document's default scene into a flat entity list. */
export function parseGltf(doc: GltfDocument): Entity[] {
  const nodes = doc.nodes ?? [];
  const scenes = doc.scenes ?? [];
  const defaultScene = scenes[doc.scene ?? 0];
  if (!defaultScene) throw new Error("glTF has no scenes");
  const rootIndices = defaultScene.nodes ?? [];
  if (rootIndices.length === 0) throw new Error("glTF scene has no root nodes");

  const entities: Entity[] = [];
  const seenPaths = new Set<string>();
  const uniquePath = (base: string): string => {
    if (!seenPaths.has(base)) { seenPaths.add(base); return base; }
    let i = 1;
    while (seenPaths.has(`${base}-${i}`)) i++;
    const p = `${base}-${i}`;
    seenPaths.add(p);
    return p;
  };

  let syntheticRootId: string | null = null;
  if (rootIndices.length > 1) {
    const sceneName = defaultScene.name ?? "scene";
    const rootPath = uniquePath(slugify(sceneName));
    syntheticRootId = rootPath;
    entities.push({ id: rootPath, entityId: rootPath, parentEntityId: null, kind: "assembly", name: sceneName, path: rootPath, transform: null });
  }

  const walk = (nodeIndex: number, parentEntityId: string | null, parentPath: string): void => {
    const node = nodes[nodeIndex];
    if (!node) return;
    const rawName = node.name ?? `node-${nodeIndex}`;
    const basePath = parentPath ? `${parentPath}.${slugify(rawName)}` : slugify(rawName);
    const entityPath = uniquePath(basePath);

    let transform: Transform | null = null;
    if (node.translation ?? node.rotation ?? node.scale) {
      transform = {
        position: node.translation ?? [0, 0, 0],
        rotationEulerDeg: quatToEulerDeg(node.rotation ?? [0, 0, 0, 1]),
        scale: node.scale ?? [1, 1, 1],
      };
    }
    const hasChildren = (node.children?.length ?? 0) > 0;
    const kind = hasChildren ? "assembly" : node.mesh !== undefined ? "part" : "module";

    entities.push({ id: entityPath, entityId: entityPath, parentEntityId, kind, name: rawName, path: entityPath, transform });
    for (const childIndex of node.children ?? []) walk(childIndex, entityPath, entityPath);
  };

  for (const rootIndex of rootIndices) walk(rootIndex, syntheticRootId, "");
  return entities;
}

export function entitiesFromBlob(bytes: Uint8Array): Entity[] {
  return parseGltf(decodeGltf(bytes));
}
