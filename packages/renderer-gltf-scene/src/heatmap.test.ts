// The heatmap on real parsed models. GLTFLoader parsing, geometry pairing,
// material swapping and disposal all work without WebGL, so all of it runs here;
// what still needs a browser is only whether the shaded pixels look right.

import { describe, it, expect } from "vitest";
import type { BufferAttribute, BufferGeometry, Material, Mesh, Object3D } from "three";
import type { StructuredDiff } from "@fhr/types";
import { createHeatmap, heatmapOffered } from "./heatmap.js";
import { geometryChanges } from "./diff-map.js";
import { loadGltf } from "./gltf-load.js";
import { decodeGltf } from "./gltf-parse.js";
import { buildNameIndex } from "./node-index.js";
import { meshesIn } from "./associations.js";
import type { LoadedSide } from "./model-overlay.js";
import { buildGltf, toArrayBuffer, toGlb, type FixtureSpec } from "./glb-fixture.js";

/** A w × w grid of quads at z = `lift`: positions and indices for a fixture. */
function plane(w: number, lift: number): { positions: number[]; indices: number[] } {
  const side = w + 1;
  const positions: number[] = [];
  for (let y = 0; y <= w; y++) {
    for (let x = 0; x <= w; x++) positions.push(x / w, y / w, lift);
  }
  const indices: number[] = [];
  for (let y = 0; y < w; y++) {
    for (let x = 0; x < w; x++) {
      const i = y * side + x;
      indices.push(i, i + 1, i + side, i + 1, i + side + 1, i + side);
    }
  }
  return { positions, indices };
}

async function side(spec: FixtureSpec): Promise<LoadedSide> {
  const bytes = toGlb(buildGltf(spec));
  const { gltf } = await loadGltf(toArrayBuffer(bytes));
  return { gltf, index: buildNameIndex(decodeGltf(bytes)) };
}

/** One node "Hood" carrying mesh "HoodMesh", whose surface sits at z = `lift`. */
function hood(lift: number, nodes?: FixtureSpec["nodes"]): FixtureSpec {
  const { positions, indices } = plane(4, lift);
  return {
    nodes: nodes ?? [{ name: "Hood", mesh: 0 }],
    meshName: "HoodMesh",
    positions,
    indices,
  };
}

/** A diff whose only content is a vertex-data edit on HoodMesh's primitive 0. */
function geometryDiff(mesh = "HoodMesh"): StructuredDiff {
  return {
    version: "1.0",
    format: "gltf-scene",
    changes: [
      {
        path: "meshes",
        label: "meshes",
        kind: "modified",
        children: [
          {
            path: `meshes/${mesh}`,
            label: mesh,
            kind: "modified",
            children: [
              {
                path: `meshes/${mesh}/primitives/0`,
                label: "primitive[0]",
                kind: "modified",
                children: [
                  {
                    path: `meshes/${mesh}/primitives/0/geometry`,
                    label: "geometry",
                    kind: "modified",
                    children: [
                      {
                        path: `meshes/${mesh}/primitives/0/geometry/POSITION`,
                        label: "POSITION",
                        kind: "modified",
                        before: "float32 vec3 × 25",
                        after: "float32 vec3 × 25",
                      },
                    ],
                  },
                  {
                    path: `meshes/${mesh}/primitives/0/centroid`,
                    label: "centroid",
                    kind: "modified",
                    before: "[0.5 0.5 0]",
                    after: "[0.5 0.5 0.12] (moved 0.120)",
                  },
                ],
              },
            ],
          },
        ],
      },
    ],
  };
}

const immediately = (): Promise<void> => Promise.resolve();

const materialOf = (object: Object3D): Material => (object as Mesh).material as Material;
const geometryOf = (object: Object3D): BufferGeometry => (object as Mesh).geometry;

describe("heatmapOffered", () => {
  it("needs both a vertex-data edit and the previous version", () => {
    expect(heatmapOffered({ geometryChanges: 2, baseResident: true })).toBe(true);
    // Nothing to measure: every change in the diff is a transform or a material.
    expect(heatmapOffered({ geometryChanges: 0, baseResident: true })).toBe(false);
    // Nothing to measure *against*: the previous version isn't loaded.
    expect(heatmapOffered({ geometryChanges: 3, baseResident: false })).toBe(false);
    expect(heatmapOffered({ geometryChanges: 0, baseResident: false })).toBe(false);
  });
});

describe("createHeatmap gating", () => {
  it("is null with no previous version, so the toggle is never built", async () => {
    const head = await side(hood(0.12));
    expect(createHeatmap({ head, base: null, geometry: geometryChanges(geometryDiff()) })).toBeNull();
  });

  it("is null when the diff has no vertex-data edit", async () => {
    const head = await side(hood(0.12));
    const base = await side(hood(0));
    // A material reassignment on the same mesh: a real change, but one whose
    // geometry is byte-identical, so a heatmap of it would be uniformly zero.
    const materialOnly: StructuredDiff = {
      version: "1.0",
      format: "gltf-scene",
      changes: [
        {
          path: "meshes/HoodMesh",
          label: "HoodMesh",
          kind: "modified",
          children: [
            {
              path: "meshes/HoodMesh/primitives/0/material",
              label: "material",
              kind: "modified",
              before: "Paint",
              after: "Chrome",
            },
          ],
        },
      ],
    };
    expect(geometryChanges(materialOnly)).toEqual([]);
    expect(createHeatmap({ head, base, geometry: geometryChanges(materialOnly) })).toBeNull();
  });

  it("is null when the changed mesh has no counterpart node in the previous version", async () => {
    const head = await side(hood(0.12, [{ name: "Hood", mesh: 0 }]));
    const base = await side(hood(0, [{ name: "Bonnet", mesh: 0 }]));
    expect(createHeatmap({ head, base, geometry: geometryChanges(geometryDiff()) })).toBeNull();
  });

  it("offers exactly the geometry it can pair up", async () => {
    const head = await side(hood(0.12));
    const base = await side(hood(0));
    const heatmap = createHeatmap({ head, base, geometry: geometryChanges(geometryDiff()) });
    expect(heatmap).not.toBeNull();
    expect(heatmap!.meshes).toBe(1);
    expect(heatmap!.on).toBe(false);
    expect(heatmap!.summary()).toBeNull();
  });
});

describe("measuring and painting", () => {
  it("measures the lift and reports it against the change's own path", async () => {
    const head = await side(hood(0.12));
    const base = await side(hood(0));
    const heatmap = createHeatmap({
      head,
      base,
      geometry: geometryChanges(geometryDiff()),
      yieldTo: immediately,
    })!;
    const summary = await heatmap.enable();
    expect(summary).not.toBeNull();
    expect(summary!.max).toBeCloseTo(0.12, 5);
    expect(summary!.min).toBeCloseTo(0.12, 5);
    expect(summary!.byPath.get("meshes/HoodMesh")).toBeCloseTo(0.12, 5);
    expect(heatmap.on).toBe(true);
  });

  it("does nothing at all until the first toggle", async () => {
    const head = await side(hood(0.12));
    const base = await side(hood(0));
    const mesh = meshesIn(head.gltf.scene)[0]!;
    const authored = materialOf(mesh);
    const heatmap = createHeatmap({
      head,
      base,
      geometry: geometryChanges(geometryDiff()),
      yieldTo: immediately,
    })!;
    expect(materialOf(mesh)).toBe(authored);
    expect(geometryOf(mesh).getAttribute("color")).toBeUndefined();
    expect(heatmap.summary()).toBeNull();
  });

  it("writes vertex colours on a cloned material, never the file's own", async () => {
    const head = await side(hood(0.12));
    const base = await side(hood(0));
    const mesh = meshesIn(head.gltf.scene)[0]!;
    const authored = materialOf(mesh);
    const authoredColor = (authored as unknown as { color: { getHex(): number } }).color.getHex();
    const heatmap = createHeatmap({
      head,
      base,
      geometry: geometryChanges(geometryDiff()),
      yieldTo: immediately,
    })!;
    await heatmap.enable();

    const painted = materialOf(mesh);
    expect(painted).not.toBe(authored);
    expect((painted as unknown as { vertexColors: boolean }).vertexColors).toBe(true);
    // Vertex colours multiply the material colour, so the clone has to be white
    // or the ramp would mean different things on differently painted panels.
    expect((painted as unknown as { color: { getHex(): number } }).color.getHex()).toBe(0xffffff);
    // The loader's instance is untouched — it is shared with every other
    // primitive that references the same glTF material.
    expect((authored as unknown as { color: { getHex(): number } }).color.getHex()).toBe(authoredColor);

    const colors = geometryOf(mesh).getAttribute("color") as BufferAttribute | undefined;
    expect(colors).toBeDefined();
    expect(colors!.count).toBe(geometryOf(mesh).getAttribute("position")!.count);
    expect(colors!.itemSize).toBe(3);
  });

  it("paints every node instancing the changed mesh, not just the first", async () => {
    const head = await side(
      hood(0.12, [
        { name: "WheelL", mesh: 0, translation: [-1, 0, 0] },
        { name: "WheelR", mesh: 0, translation: [1, 0, 0] },
      ]),
    );
    const base = await side(
      hood(0, [
        { name: "WheelL", mesh: 0, translation: [-1, 0, 0] },
        { name: "WheelR", mesh: 0, translation: [1, 0, 0] },
      ]),
    );
    const heatmap = createHeatmap({
      head,
      base,
      geometry: geometryChanges(geometryDiff()),
      yieldTo: immediately,
    })!;
    // One geometry, so one measurement — but two places on screen.
    expect(heatmap.meshes).toBe(1);
    await heatmap.enable();
    expect(heatmap.targets().length).toBe(2);
    for (const mesh of meshesIn(head.gltf.scene)) {
      expect((materialOf(mesh) as unknown as { vertexColors: boolean }).vertexColors).toBe(true);
    }
  });

  it("puts the overlay's paint back when switched off, keeping the measurement", async () => {
    const head = await side(hood(0.12));
    const base = await side(hood(0));
    const mesh = meshesIn(head.gltf.scene)[0]!;
    const before = materialOf(mesh);
    const heatmap = createHeatmap({
      head,
      base,
      geometry: geometryChanges(geometryDiff()),
      yieldTo: immediately,
    })!;
    await heatmap.enable();
    expect(materialOf(mesh)).not.toBe(before);

    heatmap.disable();
    expect(materialOf(mesh)).toBe(before);
    expect(heatmap.on).toBe(false);
    // The numbers survive: switching the colours off doesn't un-measure them,
    // and switching back on must not pay for the mesh a second time.
    expect(heatmap.summary()!.max).toBeCloseTo(0.12, 5);

    await heatmap.enable();
    expect(materialOf(mesh)).not.toBe(before);
    expect(heatmap.on).toBe(true);
  });

  it("still paints after an on-off-on before the first measurement landed", async () => {
    const head = await side(hood(0.12));
    const base = await side(hood(0));
    const heatmap = createHeatmap({
      head,
      base,
      geometry: geometryChanges(geometryDiff()),
      yieldTo: immediately,
    })!;
    // The reviewer changes their mind twice while the first run is in flight.
    // The cancelled run resolves to "no measurement"; joining it for the third
    // toggle would leave the model unpainted with the button reading pressed.
    const first = heatmap.enable();
    heatmap.disable();
    const third = heatmap.enable();
    expect(await first).toBeNull();
    expect((await third)?.max).toBeCloseTo(0.12, 5);
    expect(heatmap.on).toBe(true);
  });

  it("reads a value under a raycast hit, and nothing for geometry it didn't paint", async () => {
    const head = await side(hood(0.12));
    const base = await side(hood(0));
    const heatmap = createHeatmap({
      head,
      base,
      geometry: geometryChanges(geometryDiff()),
      yieldTo: immediately,
    })!;
    await heatmap.enable();
    const mesh = meshesIn(head.gltf.scene)[0]!;
    const reading = heatmap.readAt(mesh, { a: 0, b: 1, c: 2 });
    expect(reading).not.toBeNull();
    expect(reading!.label).toBe("HoodMesh");
    expect(reading!.value).toBeCloseTo(0.12, 5);
    // A hit with no face still answers with the mesh's own maximum rather than
    // blanking the readout.
    expect(heatmap.readAt(mesh, null)!.value).toBeCloseTo(0.12, 5);
    expect(heatmap.readAt(base.gltf.scene, { a: 0, b: 1, c: 2 })).toBeNull();
  });
});

describe("disposal", () => {
  it("frees the material clones and takes the colour attribute back off", async () => {
    const head = await side(hood(0.12));
    const base = await side(hood(0));
    const mesh = meshesIn(head.gltf.scene)[0]!;
    const authored = materialOf(mesh);
    const heatmap = createHeatmap({
      head,
      base,
      geometry: geometryChanges(geometryDiff()),
      yieldTo: immediately,
    })!;
    await heatmap.enable();
    const clone = materialOf(mesh);
    let disposed = 0;
    clone.addEventListener("dispose", () => disposed++);

    const report = heatmap.dispose();
    expect(report.materials).toBe(1);
    expect(report.attributes).toBe(1);
    expect(disposed).toBe(1);
    // The model is back on the materials the overlay put there, and the file's
    // geometry no longer carries an attribute a later material could read.
    expect(materialOf(mesh)).toBe(authored);
    expect(geometryOf(mesh).getAttribute("color")).toBeUndefined();
    expect(heatmap.on).toBe(false);
  });

  it("gives a file's own COLOR_0 back rather than deleting it", async () => {
    const head = await side(hood(0.12));
    const base = await side(hood(0));
    const mesh = meshesIn(head.gltf.scene)[0]!;
    const authoredColors = geometryOf(mesh).getAttribute("position")!;
    // Stand in for a file that ships vertex colours: the heatmap displaces the
    // attribute, and losing it would silently change how the model draws after
    // the view is closed.
    geometryOf(mesh).setAttribute("color", authoredColors);
    const heatmap = createHeatmap({
      head,
      base,
      geometry: geometryChanges(geometryDiff()),
      yieldTo: immediately,
    })!;
    await heatmap.enable();
    expect(geometryOf(mesh).getAttribute("color")).not.toBe(authoredColors);
    heatmap.dispose();
    expect(geometryOf(mesh).getAttribute("color")).toBe(authoredColors);
  });

  it("abandons a measurement still in flight instead of painting after teardown", async () => {
    const head = await side(hood(0.12));
    const base = await side(hood(0));
    const heatmap = createHeatmap({
      head,
      base,
      geometry: geometryChanges(geometryDiff()),
      yieldTo: immediately,
    })!;
    // enable() is asynchronous by construction, so the view can be torn down
    // between the request and the answer — a reviewer closing the diff while a
    // large mesh is being measured. The answer must be dropped, not painted onto
    // a model that no longer exists.
    const pending = heatmap.enable();
    heatmap.dispose();
    expect(await pending).toBeNull();
    expect(heatmap.on).toBe(false);
    const mesh = meshesIn(head.gltf.scene)[0]!;
    expect(geometryOf(mesh).getAttribute("color")).toBeUndefined();
  });
});
