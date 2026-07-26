// The visual grammar, on real parsed models. Scene assembly and math need no
// WebGL, so everything here — tinting, cloned materials, ghosts, motion vectors,
// framing boxes, disposal — is exercised for real. What still needs a browser:
// that the pixels look right (opacity, depth sorting, shader compilation).

import { describe, it, expect } from "vitest";
import type { BufferGeometry, Material, Mesh, Object3D } from "three";
import { Color } from "three";
import type { NodeChange } from "./diff-map.js";
import { loadGltf } from "./gltf-load.js";
import { decodeGltf } from "./gltf-parse.js";
import { buildNameIndex } from "./node-index.js";
import { buildOverlay, type LoadedSide } from "./model-overlay.js";
import { meshesIn } from "./associations.js";
import { pathOfNodeName } from "./change-path.js";
import { changeAtObject } from "./pick.js";
import { KIND_COLOR, NEUTRAL } from "./palette.js";
import { buildGltf, toArrayBuffer, toGlb, type FixtureSpec } from "./glb-fixture.js";

async function side(spec: FixtureSpec): Promise<LoadedSide> {
  const bytes = toGlb(buildGltf(spec));
  const { gltf } = await loadGltf(toArrayBuffer(bytes));
  return { gltf, index: buildNameIndex(decodeGltf(bytes)) };
}

const change = (name: string, kind: NodeChange["kind"], fields: string[] = []): NodeChange => ({
  name,
  kind,
  fields,
  path: pathOfNodeName(name),
});

const materialOf = (object: Object3D): Material => (object as Mesh).material as Material;
const hexOf = (object: Object3D): number =>
  ((materialOf(object) as unknown as { color: Color }).color as Color).getHex();

/** Distance in RGB space, for "was this pulled towards that colour?" assertions. */
function distanceTo(object: Object3D, hex: number): number {
  const c = (materialOf(object) as unknown as { color: Color }).color;
  const target = new Color(hex);
  return Math.hypot(c.r - target.r, c.g - target.g, c.b - target.b);
}

const TWO_NODES: FixtureSpec = {
  nodes: [
    { name: "Hood", mesh: 0, translation: [0, 0, 0] },
    { name: "Mirror", mesh: 0, translation: [4, 0, 0] },
  ],
};

describe("tinting the head model", () => {
  it("paints added and modified nodes in their palette colours", async () => {
    const head = await side(TWO_NODES);
    const overlay = buildOverlay({
      head,
      changes: [change("Hood", "modified", ["mesh"]), change("Mirror", "added")],
    });
    const byName = new Map(meshesIn(head.gltf.scene).map((m) => [m.name, m]));
    expect(distanceTo(byName.get("Hood")!, KIND_COLOR["modified"]!)).toBeLessThan(
      distanceTo(byName.get("Hood")!, KIND_COLOR["added"]!),
    );
    expect(distanceTo(byName.get("Mirror")!, KIND_COLOR["added"]!)).toBeLessThan(
      distanceTo(byName.get("Mirror")!, KIND_COLOR["modified"]!),
    );
    expect(overlay.stats.tinted).toBe(2);
  });

  it("clones materials instead of mutating the loader's shared instance", async () => {
    const head = await side(TWO_NODES);
    const meshes = meshesIn(head.gltf.scene);
    const sharedBefore = materialOf(meshes[0]!);
    expect(materialOf(meshes[1]!)).toBe(sharedBefore); // one material, two meshes
    const originalHex = (sharedBefore as unknown as { color: Color }).color.getHex();

    buildOverlay({ head, changes: [change("Hood", "modified", ["mesh"])] });

    // The neighbour is no longer painted with the tinted material …
    expect(materialOf(meshes[0]!)).not.toBe(materialOf(meshes[1]!));
    // … and the original instance was left exactly as the loader made it.
    expect((sharedBefore as unknown as { color: Color }).color.getHex()).toBe(originalHex);
  });

  it("reuses one tint clone for one shared material, not one per mesh", async () => {
    const head = await side({
      nodes: [
        { name: "A", mesh: 0 },
        { name: "B", mesh: 0 },
        { name: "C", mesh: 0 },
      ],
    });
    buildOverlay({
      head,
      changes: [change("A", "added"), change("B", "added"), change("C", "added")],
    });
    const materials = new Set(meshesIn(head.gltf.scene).map(materialOf));
    expect(materials.size).toBe(1);
  });

  it("quiets unchanged geometry towards neutral, opaquely", async () => {
    const head = await side(TWO_NODES);
    const overlay = buildOverlay({ head, changes: [change("Hood", "modified", ["mesh"])] });
    const mirror = meshesIn(head.gltf.scene).find((m) => m.name === "Mirror")!;
    expect(overlay.stats.desaturated).toBe(1);
    expect(distanceTo(mirror, NEUTRAL)).toBeLessThan(0.35);
    // Transparency is reserved for the base layers — two translucent layers over
    // each other is a depth-sorting soup.
    expect(materialOf(mirror).transparent).toBe(false);
  });

  it("leaves the model as authored when there is no diff at all", async () => {
    const head = await side(TWO_NODES);
    const authored = new Map(meshesIn(head.gltf.scene).map((m) => [m.name, hexOf(m)]));
    const overlay = buildOverlay({ head, changes: [] });
    for (const mesh of meshesIn(head.gltf.scene)) {
      expect(hexOf(mesh)).toBe(authored.get(mesh.name));
    }
    expect(overlay.stats).toMatchObject({ tinted: 0, desaturated: 0 });
  });

  it("does not grey the whole model when no change could be located", async () => {
    const head = await side(TWO_NODES);
    const overlay = buildOverlay({ head, changes: [change("Spoiler", "added")] });
    expect(overlay.stats.unmatched).toBe(1);
    expect(overlay.stats.desaturated).toBe(0);
    expect(overlay.notes.join(" ")).toContain("isn't in either file's scene graph");
  });

  // Regression: a removed node lives only in the previous version. With no base
  // loaded it was counted as "in neither file's scene graph", which blames a
  // change list that is in fact correct and points a reviewer at the wrong repo.
  it("blames the missing base, not the change list, for an undrawable removal", async () => {
    const head = await side(TWO_NODES);
    const overlay = buildOverlay({ head, base: null, changes: [change("Mirror_L", "removed", ["translation"])] });

    expect(overlay.stats.needsBase).toBe(1);
    expect(overlay.stats.unmatched).toBe(0);

    const notes = overlay.notes.join(" ");
    expect(notes).toContain("previous version, which isn't loaded");
    expect(notes).toContain("change list is correct");
    expect(notes).not.toContain("isn't in either file's scene graph");
  });

  // A name in neither file is still a real mismatch even with no base loaded —
  // the new counter must not swallow it.
  it("still reports a genuinely unknown name when no base is loaded", async () => {
    const head = await side(TWO_NODES);
    const overlay = buildOverlay({ head, base: null, changes: [change("Spoiler", "modified", ["translation"])] });
    expect(overlay.stats.unmatched).toBe(1);
    expect(overlay.stats.needsBase).toBe(0);
  });

  // With the base present the removal is drawn, so neither counter fires.
  it("draws the removal and counts nothing once the base is loaded", async () => {
    const head = await side(TWO_NODES);
    const base = await side(TWO_NODES);
    const removedName = meshesIn(base.gltf.scene)[0]!.name;
    const overlay = buildOverlay({ head, base, changes: [change(removedName, "removed", ["translation"])] });
    expect(overlay.stats.needsBase).toBe(0);
    expect(overlay.stats.removedGhosts).toBeGreaterThan(0);
  });

  it("paints every primitive of a multi-primitive node", async () => {
    const head = await side({ nodes: [{ name: "Shell", mesh: 0 }], primitives: 3 });
    const overlay = buildOverlay({ head, changes: [change("Shell", "modified", ["mesh"])] });
    expect(overlay.stats.tinted).toBe(3);
  });

  it("matches a change whose label reached us mangled", async () => {
    const head = await side({ nodes: [{ name: "Cube.001", mesh: 0 }] });
    const overlay = buildOverlay({ head, changes: [change("Cube001", "modified", ["mesh"])] });
    expect(overlay.stats.tinted).toBe(1);
    expect(overlay.stats.unmatched).toBe(0);
  });

  it("warns when a changed name is ambiguous in the file", async () => {
    const head = await side({
      nodes: [
        { name: "Cube", mesh: 0 },
        { name: "Cube", mesh: 0 },
      ],
    });
    const overlay = buildOverlay({ head, changes: [change("Cube", "modified", ["mesh"])] });
    expect(overlay.notes.join(" ")).toContain("2 nodes in this file are called \"Cube\"");
    expect(overlay.stats.tinted).toBe(1); // the first, as the handler meant
  });
});

describe("removed geometry, drawn from the base file", () => {
  it("ghosts a removed node at its base transform, in the removed colour", async () => {
    const head = await side({ nodes: [{ name: "Hood", mesh: 0 }] });
    const base = await side({
      nodes: [
        { name: "Hood", mesh: 0 },
        { name: "Mirror", mesh: 0, translation: [4, 1, 0] },
      ],
    });
    const overlay = buildOverlay({ head, base, changes: [change("Mirror", "removed")] });

    expect(overlay.stats.removedGhosts).toBe(1);
    const ghost = overlay.removedGroup!.children[0]!;
    const material = materialOf(meshesIn(ghost)[0] ?? ghost);
    expect((material as unknown as { color: Color }).color.getHex()).toBe(KIND_COLOR["removed"]);
    expect(material.transparent).toBe(true);
    expect(material.depthWrite).toBe(false);
    // Positioned where it was in the previous version.
    expect(ghost.matrix.elements[12]).toBeCloseTo(4);
    expect(ghost.matrix.elements[13]).toBeCloseTo(1);
  });

  it("shares one material across all removed ghosts", async () => {
    const head = await side({ nodes: [{ name: "Keep", mesh: 0 }] });
    const base = await side({
      nodes: [
        { name: "Keep", mesh: 0 },
        { name: "GoneA", mesh: 0 },
        { name: "GoneB", mesh: 0 },
      ],
    });
    const overlay = buildOverlay({
      head,
      base,
      changes: [change("GoneA", "removed"), change("GoneB", "removed")],
    });
    const materials = new Set(overlay.removedGroup!.children.map((c) => materialOf(meshesIn(c)[0] ?? c)));
    expect(materials.size).toBe(1);
  });

  it("draws nothing for a removed node when the base model isn't available", async () => {
    const head = await side({ nodes: [{ name: "Hood", mesh: 0 }] });
    const overlay = buildOverlay({ head, base: null, changes: [change("Mirror", "removed")] });
    expect(overlay.removedGroup).toBeNull();
    // The reviewer is told — and told the truth. This used to count as
    // `unmatched`, i.e. "in neither file's scene graph", which blames a change
    // list that named a node the previous version really does contain.
    expect(overlay.stats.needsBase).toBe(1);
    expect(overlay.stats.unmatched).toBe(0);
    expect(overlay.notes).not.toHaveLength(0);
  });
});

describe("moves: ghost at the old pose plus a motion vector", () => {
  it("ghosts the old pose and draws the vector between old and new", async () => {
    const head = await side({ nodes: [{ name: "Wheel", mesh: 0, translation: [6, 0, 0] }] });
    const base = await side({ nodes: [{ name: "Wheel", mesh: 0, translation: [0, 0, 0] }] });
    const overlay = buildOverlay({
      head,
      base,
      changes: [change("Wheel", "modified", ["translation"])],
    });

    expect(overlay.stats.moveGhosts).toBe(1);
    expect(overlay.stats.motionVectors).toBe(1);
    const line = overlay.movedGroup!.children.find((c) => c.name === "fhr-motion")!;
    const positions = (line as unknown as { geometry: { attributes: { position: { array: ArrayLike<number> } } } })
      .geometry.attributes.position.array;
    expect(positions[0]).toBeCloseTo(0.5); // base triangle's centre
    expect(positions[3]).toBeCloseTo(6.5); // head triangle's centre
  });

  it("skips the vector when the reported change didn't move anything", async () => {
    // A real case: (0,0,0,-1) and (0,0,0,1) are the *same* rotation, but compare
    // unequal component-wise, so a transform change can be reported for a node
    // that sits in exactly the same place. Ghost it, but draw no arrow to
    // nowhere.
    const head = await side({ nodes: [{ name: "Hinge", mesh: 0, rotation: [0, 0, 0, -1] }] });
    const base = await side({ nodes: [{ name: "Hinge", mesh: 0, rotation: [0, 0, 0, 1] }] });
    const overlay = buildOverlay({ head, base, changes: [change("Hinge", "modified", ["rotation"])] });
    expect(overlay.stats.moveGhosts).toBe(1);
    expect(overlay.stats.motionVectors).toBe(0);
  });

  it("anchors the vector on the geometry, so it connects the two visible bodies", async () => {
    // The arrow runs between bounding-box centres rather than node origins: on a
    // node whose geometry is offset from its origin, an origin-to-origin arrow
    // would start in empty space away from the ghost it is meant to label.
    const head = await side({ nodes: [{ name: "Panel", mesh: 0, translation: [10, 0, 0] }] });
    const base = await side({ nodes: [{ name: "Panel", mesh: 0, translation: [0, 0, 0] }] });
    const overlay = buildOverlay({ head, base, changes: [change("Panel", "modified", ["translation"])] });
    const line = overlay.movedGroup!.children.find((c) => c.name === "fhr-motion")!;
    const positions = (line as unknown as { geometry: { attributes: { position: { array: ArrayLike<number> } } } })
      .geometry.attributes.position.array;
    // The fixture triangle spans x∈[0,1], so its centre sits at +0.5, not at 0.
    expect(positions[0]).toBeCloseTo(0.5);
    expect(positions[3]).toBeCloseTo(10.5);
  });

  it("does not ghost a node whose change isn't a transform", async () => {
    const head = await side({ nodes: [{ name: "Door", mesh: 0 }] });
    const base = await side({ nodes: [{ name: "Door", mesh: 0 }] });
    const overlay = buildOverlay({ head, base, changes: [change("Door", "modified", ["mesh"])] });
    expect(overlay.stats.moveGhosts).toBe(0);
    expect(overlay.movedGroup).toBeNull();
    expect(overlay.stats.tinted).toBe(1);
  });
});

describe("ghost base overlay", () => {
  it("draws the whole previous version through one shared translucent material", async () => {
    const head = await side(TWO_NODES);
    const base = await side(TWO_NODES);
    const overlay = buildOverlay({ head, base, changes: [change("Hood", "modified", ["mesh"])] });

    const ghostMeshes = meshesIn(overlay.baseGhostGroup!);
    expect(ghostMeshes.length).toBeGreaterThan(0);
    const materials = new Set(ghostMeshes.map(materialOf));
    expect(materials.size).toBe(1);
    const material = [...materials][0]!;
    expect(material.transparent).toBe(true);
    expect(material.opacity).toBeCloseTo(0.22);
    expect(material.depthWrite).toBe(false);
    for (const mesh of ghostMeshes) expect(mesh.renderOrder).toBe(-1);
  });

  it("keeps a hidden solid copy of the base for the A/B blink", async () => {
    const head = await side(TWO_NODES);
    const base = await side(TWO_NODES);
    const overlay = buildOverlay({ head, base, changes: [] });
    expect(overlay.baseSolidGroup!.visible).toBe(false);
    expect(overlay.headGroup.visible).toBe(true);
    // The solid copy keeps the base's own materials, so the blink is a pure
    // visibility swap with no material or upload work on the blink frame.
    expect(materialOf(meshesIn(overlay.baseSolidGroup!)[0]!)).not.toBe(
      materialOf(meshesIn(overlay.baseGhostGroup!)[0]!),
    );
  });

  it("hides, in the plain ghost, the nodes drawn under their own grammar", async () => {
    const head = await side({ nodes: [{ name: "Keep", mesh: 0 }] });
    const base = await side({
      nodes: [
        { name: "Keep", mesh: 0 },
        { name: "Gone", mesh: 0, translation: [3, 0, 0] },
      ],
    });
    const overlay = buildOverlay({ head, base, changes: [change("Gone", "removed")] });
    const visible = meshesIn(overlay.baseGhostGroup!).filter((m) => m.visible);
    const hidden = meshesIn(overlay.baseGhostGroup!).filter((m) => !m.visible);
    expect(visible).toHaveLength(1); // "Keep"
    expect(hidden).toHaveLength(1); // "Gone" — drawn as a removed ghost instead
  });

  it("has no base layers at all when the base wasn't loaded", async () => {
    const overlay = buildOverlay({ head: await side(TWO_NODES), changes: [] });
    expect(overlay.baseGhostGroup).toBeNull();
    expect(overlay.baseSolidGroup).toBeNull();
  });
});

describe("framing boxes", () => {
  it("unions the changes, not the model, into changeBox", async () => {
    const head = await side({
      nodes: [
        { name: "Near", mesh: 0, translation: [0, 0, 0] },
        { name: "Far", mesh: 0, translation: [100, 0, 0] },
      ],
    });
    const overlay = buildOverlay({ head, changes: [change("Far", "modified", ["mesh"])] });
    expect(overlay.changeBox.min.x).toBeCloseTo(100);
    expect(overlay.sceneBox.min.x).toBeCloseTo(0); // the whole model is wider
  });

  it("includes removed geometry (which only exists in the base) in changeBox", async () => {
    const head = await side({ nodes: [{ name: "Keep", mesh: 0 }] });
    const base = await side({
      nodes: [
        { name: "Keep", mesh: 0 },
        { name: "Gone", mesh: 0, translation: [50, 0, 0] },
      ],
    });
    const overlay = buildOverlay({ head, base, changes: [change("Gone", "removed")] });
    expect(overlay.changeBox.max.x).toBeGreaterThan(50);
  });

  it("keeps a box per change name, so a change list can fly to one (#45)", async () => {
    const head = await side(TWO_NODES);
    const overlay = buildOverlay({
      head,
      changes: [change("Hood", "modified", ["mesh"]), change("Mirror", "added")],
    });
    expect([...overlay.boxByChangeName.keys()].sort()).toEqual(["Hood", "Mirror"]);
    expect(overlay.boxByChangeName.get("Mirror")!.min.x).toBeCloseTo(4);
    expect(overlay.objectsByChangeName.get("Hood")).toHaveLength(1);
  });

  it("leaves changeBox empty when nothing was painted", async () => {
    const overlay = buildOverlay({ head: await side(TWO_NODES), changes: [] });
    expect(overlay.changeBox.isEmpty()).toBe(true);
  });
});

describe("overlay disposal", () => {
  it("frees geometry and materials from both models and the ghosts", async () => {
    const head = await side(TWO_NODES);
    const base = await side(TWO_NODES);
    const overlay = buildOverlay({
      head,
      base,
      changes: [change("Hood", "modified", ["translation"]), change("Mirror", "removed")],
    });

    // Geometry is shared between the base model, its ghost clone and the removed
    // ghost, so count unique geometries: each must be disposed exactly once.
    const geometries = new Set<BufferGeometry>();
    overlay.root.traverse((object) => {
      const mesh = object as Mesh;
      if (mesh.geometry) geometries.add(mesh.geometry);
    });
    const disposed = new Set<BufferGeometry>();
    for (const geometry of geometries) {
      geometry.addEventListener("dispose", () => disposed.add(geometry));
    }

    const report = overlay.dispose();
    expect(geometries.size).toBeGreaterThan(0);
    expect(report.geometries).toBe(geometries.size);
    expect(disposed.size).toBe(geometries.size);
    // Tint clones AND the originals they displaced are both accounted for.
    expect(report.materials).toBeGreaterThanOrEqual(4);
    expect(overlay.root.parent).toBeNull();
  });
});

// The maps the raycast reads (#45). Picking is what makes the 3D view an input
// and not just a picture, and the only mapping that survives cloning is the one
// the overlay builds while it paints.
describe("the maps a click resolves through", () => {
  it("maps every painted head object to its change", async () => {
    const head = await side(TWO_NODES);
    const overlay = buildOverlay({ head, changes: [change("Hood", "modified", ["mesh"])] });
    const objects = overlay.objectsByChangeName.get("Hood")!;
    expect(objects.length).toBeGreaterThan(0);
    for (const object of objects) expect(overlay.changeNameByObject.get(object)).toBe("Hood");
  });

  it("resolves a click on a mesh inside a painted node", async () => {
    const head = await side(TWO_NODES);
    const overlay = buildOverlay({ head, changes: [change("Hood", "modified", ["mesh"])] });
    const hood = meshesIn(head.gltf.scene).find((m) => m.name === "Hood")!;
    expect(changeAtObject(hood, { changeNameByObject: overlay.changeNameByObject })).toBe("Hood");
  });

  it("resolves a click on the ghost of a removed part, which no association covers", async () => {
    const head = await side({ nodes: [{ name: "Hood", mesh: 0 }] });
    const base = await side(TWO_NODES);
    const overlay = buildOverlay({ head, base, changes: [change("Mirror", "removed")] });
    const ghost = overlay.removedGroup!.children[0]!;
    // The clone is in no glTF association — the loader never made it.
    expect(overlay.nodeIndexOfObject(ghost)).toBeNull();
    expect(changeAtObject(meshesIn(ghost)[0] ?? ghost, { changeNameByObject: overlay.changeNameByObject })).toBe(
      "Mirror",
    );
  });

  it("resolves nothing for a click on unchanged geometry", async () => {
    const head = await side(TWO_NODES);
    const overlay = buildOverlay({ head, changes: [change("Hood", "modified", ["mesh"])] });
    const mirror = meshesIn(head.gltf.scene).find((m) => m.name === "Mirror")!;
    expect(changeAtObject(mirror, { changeNameByObject: overlay.changeNameByObject })).toBeNull();
  });

  it("offers the loader's node association as the fallback path", async () => {
    const head = await side(TWO_NODES);
    const overlay = buildOverlay({ head, changes: [change("Mirror", "modified", ["mesh"])] });
    const mirror = meshesIn(head.gltf.scene).find((m) => m.name === "Mirror")!;
    const index = overlay.nodeIndexOfObject(mirror);
    expect(index).not.toBeNull();
    expect(overlay.changeNameByNodeIndex.get(index!)).toBe("Mirror");
    // The same answer, reached without the painted map.
    expect(
      changeAtObject(mirror, {
        changeNameByObject: new Map(),
        nodeIndexOf: overlay.nodeIndexOfObject,
        changeNameByNodeIndex: overlay.changeNameByNodeIndex,
      }),
    ).toBe("Mirror");
  });
});
