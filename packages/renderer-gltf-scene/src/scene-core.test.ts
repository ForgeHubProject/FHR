import { describe, it, expect } from "vitest";
import type { StructuredDiff } from "@fhr/types";
import { decodeGltf, entitiesFromBlob, type Entity } from "./gltf-parse.js";
import { diffChangeTypes } from "./diff-map.js";

const doc = {
  asset: { version: "2.0" },
  scene: 0,
  scenes: [{ nodes: [0, 1] }],
  nodes: [
    { name: "Cube", translation: [1, 2, 3], mesh: 0 },
    { name: "Rig", children: [2] },
    { name: "Bone", translation: [0, 1, 0] },
  ],
};

const bytes = (d: object): Uint8Array => new TextEncoder().encode(JSON.stringify(d));
const ents = (d: object): Entity[] => entitiesFromBlob(bytes(d));

function glb(json: object): Uint8Array {
  const jsonBytes = new TextEncoder().encode(JSON.stringify(json));
  const pad = (4 - (jsonBytes.length % 4)) % 4;
  const chunkLen = jsonBytes.length + pad;
  const total = 12 + 8 + chunkLen;
  const buf = new Uint8Array(total);
  const dv = new DataView(buf.buffer);
  dv.setUint32(0, 0x46546c67, true); // "glTF"
  dv.setUint32(4, 2, true);
  dv.setUint32(8, total, true);
  dv.setUint32(12, chunkLen, true);
  dv.setUint32(16, 0x4e4f534a, true); // "JSON"
  buf.set(jsonBytes, 20);
  for (let i = 0; i < pad; i++) buf[20 + jsonBytes.length + i] = 0x20;
  return buf;
}

describe("parseGltf (via entitiesFromBlob)", () => {
  it("builds a synthetic assembly root for a multi-root scene", () => {
    const root = ents(doc).find((e) => e.parentEntityId === null && e.kind === "assembly" && e.name === "scene");
    expect(root).toBeTruthy();
  });

  it("classifies kind by structure: children→assembly, mesh→part, else→module", () => {
    const e = ents(doc);
    expect(e.find((x) => x.name === "Cube")?.kind).toBe("part"); // has mesh
    expect(e.find((x) => x.name === "Rig")?.kind).toBe("assembly"); // has children
    expect(e.find((x) => x.name === "Bone")?.kind).toBe("module"); // neither
  });

  it("converts quaternion rotation to euler degrees and keeps translation", () => {
    const cube = ents(doc).find((e) => e.name === "Cube")!;
    expect(cube.transform?.position).toEqual([1, 2, 3]);
    expect(cube.transform?.rotationEulerDeg).toEqual([0, 0, 0]);
  });

  it("nests children under their parent", () => {
    const e = ents(doc);
    const rig = e.find((x) => x.name === "Rig")!;
    const bone = e.find((x) => x.name === "Bone")!;
    expect(bone.parentEntityId).toBe(rig.entityId);
  });
});

describe("decodeGltf / entitiesFromBlob", () => {
  it("decodes a plain .gltf JSON blob", () => {
    expect(decodeGltf(bytes(doc)).nodes?.length).toBe(3);
    expect(ents(doc).some((e) => e.name === "Cube")).toBe(true);
  });

  it("decodes a .glb binary container", () => {
    expect(entitiesFromBlob(glb(doc)).some((e) => e.name === "Bone")).toBe(true);
  });

  it("throws on a scene-less document", () => {
    expect(() => ents({ asset: { version: "2.0" }, nodes: [] })).toThrow(/no scenes/);
  });
});

describe("diffChangeTypes", () => {
  // Handler paths are "/"-separated and fully qualified, so field changes carry
  // the node prefix too ("nodes/Cube/translation"); only the node level colours
  // the scene.
  const diff: StructuredDiff = {
    version: "1.0",
    format: "gltf-scene",
    changes: [
      {
        path: "nodes",
        kind: "modified",
        children: [
          {
            path: "nodes/Cube",
            kind: "modified",
            label: "Cube",
            children: [
              { path: "nodes/Cube/translation", kind: "modified" },
              { path: "nodes/Cube/parent", kind: "modified", before: "Body", after: "Door_L" },
            ],
          },
          { path: "nodes/NewLamp", kind: "added", label: "NewLamp" },
        ],
      },
      {
        path: "materials",
        kind: "modified",
        children: [{ path: "materials/Paint", kind: "modified", label: "Paint" }],
      },
    ],
  };

  it("keys node change kinds by slugified name, ignoring field children", () => {
    const m = diffChangeTypes(diff);
    expect(m.get("cube")).toBe("modified");
    expect(m.get("newlamp")).toBe("added");
    expect(m.has("translation")).toBe(false);
    expect(m.has("parent")).toBe(false);
  });

  it("ignores other collections", () => {
    expect(diffChangeTypes(diff).has("paint")).toBe(false);
  });

  it("unescapes the path when a change carries no label", () => {
    const m = diffChangeTypes({
      version: "1.0",
      format: "gltf-scene",
      changes: [{ path: "nodes/rig%2Fhand", kind: "removed" }],
    });
    expect(m.get("rig-hand")).toBe("removed");
  });

  it("returns an empty map for no diff", () => {
    expect(diffChangeTypes(undefined).size).toBe(0);
  });
});
