// The heatmap on real parsed models. GLTFLoader parsing, geometry pairing,
// material swapping and disposal all work without WebGL, so all of it runs here;
// what still needs a browser is only whether the shaded pixels look right.

import { describe, it, expect } from "vitest";
import type { BufferAttribute, BufferGeometry, Material, Mesh, Object3D } from "three";
import type { StructuredDiff } from "@fhr/types";
import { createHeatmap, heatmapOffered, type Heatmap } from "./heatmap.js";
import { geometryChanges } from "./diff-map.js";
import { loadGltf } from "./gltf-load.js";
import { decodeGltf } from "./gltf-parse.js";
import { buildNameIndex } from "./node-index.js";
import { meshesIn } from "./associations.js";
import { formatDeviation, CHUNK_VERTICES } from "./deviation.js";
import { rampLinear } from "./ramp.js";
import { createHeatmapLegend } from "./legend.js";
import { asElement, createFakeDocument, type FakeElement } from "./fake-dom.js";
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
function hood(lift: number, nodes?: FixtureSpec["nodes"], w = 4): FixtureSpec {
  const { positions, indices } = plane(w, lift);
  return {
    nodes: nodes ?? [{ name: "Hood", mesh: 0 }],
    meshName: "HoodMesh",
    positions,
    indices,
  };
}

/**
 * A hood whose surface is tilted from z = `low` at one edge to z = `high` at the
 * other, so the measurement has a genuine range rather than one value repeated —
 * the case where the ramp's low end and the legend's low label can disagree.
 */
function tiltedHood(low: number, high: number): FixtureSpec {
  const w = 4;
  const { positions, indices } = plane(w, 0);
  for (let v = 0; v * 3 < positions.length; v++) {
    positions[v * 3 + 2] = low + (high - low) * positions[v * 3]!;
  }
  return { nodes: [{ name: "Hood", mesh: 0 }], meshName: "HoodMesh", positions, indices };
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
const colorOf = (object: Object3D): BufferAttribute | undefined =>
  geometryOf(object).getAttribute("color") as BufferAttribute | undefined;

/** Where on the ramp a painted colour sits — the lookup a reviewer does by eye. */
function rampPosition(r: number, g: number, b: number): number {
  let best = 0;
  let bestError = Infinity;
  for (let i = 0; i <= 2000; i++) {
    const t = i / 2000;
    const c = rampLinear(t);
    const error = (c.r - r) ** 2 + (c.g - g) ** 2 + (c.b - b) ** 2;
    if (error < bestError) {
      bestError = error;
      best = t;
    }
  }
  return best;
}

/** "12.0 mm" → 0.012: the legend's label read back as the number it states. */
function labelled(text: string): number {
  const [value, unit] = text.split(" ");
  return Number(value) * (unit === "mm" ? 0.001 : 1);
}

function legendFor(summary: { min: number; max: number }): { low: number; high: number } {
  const doc = createFakeDocument();
  const host = doc.createElement("div") as FakeElement;
  const legend = createHeatmapLegend(asElement(host), "light");
  legend.setRange(summary.min, summary.max);
  return {
    low: labelled(host.byAttr("data-legend-min", "1")[0]!.textContent),
    high: labelled(host.byAttr("data-legend-max", "1")[0]!.textContent),
  };
}

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

describe("the scale a colour is read against", () => {
  it("paints a colour the legend's own labels decode back to the measured value", async () => {
    // A surface tilted from 100 mm to 200 mm: nothing sits at zero, which is
    // exactly when the ramp's normalisation and the legend's labels can quietly
    // disagree. Whatever they do, the round trip a reviewer makes — see a
    // colour, find it on the ramp, convert with the printed ends — has to give
    // back the number that was measured.
    const head = await side(tiltedHood(0.1, 0.2));
    const base = await side(hood(0));
    const heatmap = createHeatmap({
      head,
      base,
      geometry: geometryChanges(geometryDiff()),
      yieldTo: immediately,
    })!;
    const summary = (await heatmap.enable())!;
    expect(summary.min).toBeCloseTo(0.1, 5);
    expect(summary.max).toBeCloseTo(0.2, 5);

    const { low, high } = legendFor(summary);
    const mesh = meshesIn(head.gltf.scene)[0]!;
    const colors = colorOf(mesh)!;
    const positions = geometryOf(mesh).getAttribute("position")!;
    for (const v of [0, 2, positions.count - 1]) {
      const t = rampPosition(colors.getX(v), colors.getY(v), colors.getZ(v));
      // The vertex sits at z above a base plane at zero, so its deviation IS
      // its z — the one number both halves of the picture have to agree on.
      expect(low + t * (high - low)).toBeCloseTo(positions.getZ(v), 3);
    }
  });

  it("reports the scene's millimetres, not the mesh's own units", async () => {
    // A mesh authored in millimetres hung under a node scaled 0.001 — the
    // ordinary CAD and Blender export. Its local coordinates lift by 0.12, and
    // reading those raw would put "120 mm" in a legend, a hover readout and a
    // queue row for an edit that is twelve hundredths of a millimetre.
    const authoredInMm = (lift: number): FixtureSpec =>
      hood(lift, [{ name: "Hood", mesh: 0, scale: [0.001, 0.001, 0.001] }]);
    const head = await side(authoredInMm(0.12));
    const base = await side(authoredInMm(0));
    const heatmap = createHeatmap({
      head,
      base,
      geometry: geometryChanges(geometryDiff()),
      yieldTo: immediately,
    })!;
    const summary = (await heatmap.enable())!;
    expect(summary.max).toBeCloseTo(0.00012, 9);
    expect(summary.byPath.get("meshes/HoodMesh")).toBeCloseTo(0.00012, 9);
    expect(formatDeviation(summary.max)).toBe("0.12 mm");
    expect(summary.mixedScale).toBe(false);
    // The hover readout is the same number by the same route.
    expect(formatDeviation(heatmap.readAt(meshesIn(head.gltf.scene)[0]!, null)!.value)).toBe("0.12 mm");
  });

  it("measures shared geometry at its largest instance and admits the ambiguity", async () => {
    // One mesh, two sizes, one colour attribute between them: there is no
    // reading that is right for both copies. The largest wins so the headline
    // "max deviation" is at least an upper bound, and the summary says so.
    const nodes: FixtureSpec["nodes"] = [
      { name: "Small", mesh: 0, scale: [1, 1, 1] },
      { name: "Big", mesh: 0, scale: [2, 2, 2] },
    ];
    const head = await side(hood(0.12, nodes));
    const base = await side(hood(0, nodes));
    const heatmap = createHeatmap({
      head,
      base,
      geometry: geometryChanges(geometryDiff()),
      yieldTo: immediately,
    })!;
    const summary = (await heatmap.enable())!;
    expect(summary.max).toBeCloseTo(0.24, 5);
    expect(summary.mixedScale).toBe(true);
  });

  it("does not call two instances at two orientations a scale conflict", async () => {
    // Distances survive rotation, so four wheels facing four ways measure
    // identically — flagging them would put a caveat on every wheeled model.
    const nodes: FixtureSpec["nodes"] = [
      { name: "WheelL", mesh: 0, translation: [-1, 0, 0] },
      { name: "WheelR", mesh: 0, translation: [1, 0, 0], rotation: [0, 0, Math.SQRT1_2, Math.SQRT1_2] },
    ];
    const head = await side(hood(0.12, nodes));
    const base = await side(hood(0, nodes));
    const summary = (await createHeatmap({
      head,
      base,
      geometry: geometryChanges(geometryDiff()),
      yieldTo: immediately,
    })!.enable())!;
    expect(summary.max).toBeCloseTo(0.12, 5);
    expect(summary.mixedScale).toBe(false);
  });
});

describe("switching off", () => {
  it("takes the ramp off the file's geometry, not only the materials", async () => {
    const head = await side(hood(0.12));
    const base = await side(hood(0));
    const mesh = meshesIn(head.gltf.scene)[0]!;
    // Stand in for a file that ships COLOR_0. GLTFLoader turns `vertexColors`
    // on for any primitive carrying it, so a ramp left on the geometry keeps
    // drawing through the authored material — in structural mode, in
    // side-by-side, in overlay without the heatmap — until the view is torn
    // down. And scene-3d calls disable() on every mode change, not just the
    // toggle.
    const authoredColors = geometryOf(mesh).getAttribute("position")! as BufferAttribute;
    geometryOf(mesh).setAttribute("color", authoredColors);
    const heatmap = createHeatmap({
      head,
      base,
      geometry: geometryChanges(geometryDiff()),
      yieldTo: immediately,
    })!;
    await heatmap.enable();
    expect(colorOf(mesh)).not.toBe(authoredColors);

    heatmap.disable();
    expect(colorOf(mesh)).toBe(authoredColors);
    await heatmap.enable();
    expect(colorOf(mesh)).not.toBe(authoredColors);
  });

  it("leaves a geometry that had no colours without one", async () => {
    const head = await side(hood(0.12));
    const base = await side(hood(0));
    const mesh = meshesIn(head.gltf.scene)[0]!;
    const heatmap = createHeatmap({
      head,
      base,
      geometry: geometryChanges(geometryDiff()),
      yieldTo: immediately,
    })!;
    await heatmap.enable();
    expect(colorOf(mesh)).toBeDefined();
    heatmap.disable();
    expect(colorOf(mesh)).toBeUndefined();
  });

  it("comes back on with the ramp it already built rather than recolouring", async () => {
    // The promise the toggle makes: off and on again is a pointer swap per
    // painted mesh, not a second pass over every vertex.
    const head = await side(hood(0.12));
    const base = await side(hood(0));
    const mesh = meshesIn(head.gltf.scene)[0]!;
    const heatmap = createHeatmap({
      head,
      base,
      geometry: geometryChanges(geometryDiff()),
      yieldTo: immediately,
    })!;
    await heatmap.enable();
    const ramp = colorOf(mesh);
    heatmap.disable();
    await heatmap.enable();
    expect(colorOf(mesh)).toBe(ramp);
  });
});

describe("staying interactive", () => {
  it("yields during the colour pass, not just during the measurement", async () => {
    // `rampLinear` is three Math.pow calls a vertex — ~54 ms for 100k of them,
    // landing right after the reviewer has already waited for the measurement.
    // Proved by cancelling from inside a yield that can only be the paint's:
    // the summary is null until the measurement has landed, so a heatmap that
    // painted in one blocking pass would finish before this ever fired.
    const head = await side(hood(0.12, undefined, 64)); // 65² = 4225 vertices
    const base = await side(hood(0, undefined, 64));
    expect(4225).toBeGreaterThan(CHUNK_VERTICES);
    let heatmap: Heatmap | null = null;
    const cancelOncePainting = async (): Promise<void> => {
      if (heatmap !== null && heatmap.summary() !== null) heatmap.disable();
    };
    heatmap = createHeatmap({
      head,
      base,
      geometry: geometryChanges(geometryDiff()),
      yieldTo: cancelOncePainting,
    })!;
    expect(await heatmap.enable()).toBeNull();
    expect(heatmap.on).toBe(false);
    // The measurement survives the cancelled paint — it is a fact about the two
    // files — but nothing was left half-coloured on the model.
    expect(heatmap.summary()!.max).toBeCloseTo(0.12, 5);
    expect(colorOf(meshesIn(head.gltf.scene)[0]!)).toBeUndefined();
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
