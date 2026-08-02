// The visual grammar, on real parsed models. Scene assembly and math need no
// WebGL, so everything here — tinting, cloned materials, ghosts, motion vectors,
// framing boxes, disposal — is exercised for real. What still needs a browser:
// that the pixels look right (opacity, depth sorting, shader compilation).

import { describe, it, expect } from "vitest";
import type { BufferGeometry, Material, Mesh, Object3D } from "three";
import { Color, Vector3 } from "three";
import type { NodeChange } from "./diff-map.js";
import { loadGltf } from "./gltf-load.js";
import { decodeGltf, parseGltf } from "./gltf-parse.js";
import { buildNameIndex, resolveNodeIndex } from "./node-index.js";
import { buildSceneGraph } from "./scene-graph.js";
import { buildOverlay, type LoadedSide } from "./model-overlay.js";
import { meshesIn } from "./associations.js";
import { pathOfNodeName } from "./change-path.js";
import { changeAtObject } from "./pick.js";
import { KIND_COLOR, NEUTRAL } from "./palette.js";
import { buildGltf, toArrayBuffer, toGlb, type FixtureSpec } from "./glb-fixture.js";
import { selectionKeys } from "./selection-keys.js";

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

  // Regression: both poses of a moved node are the same shape in the same hue,
  // and opacity was the only thing telling them apart. The ghost is unlit while
  // the head copy is lit and tinted, so on a face turned from the light the
  // current position can read darker than the previous one and "faded means old"
  // inverts. Wireframe vs solid is categorical: it can't invert under lighting,
  // survives any camera angle, and carries no hue, so it also holds up for
  // colour-blind viewers.
  it("draws the old pose as a wireframe so it can't be mistaken for the new one", async () => {
    const head = await side({ nodes: [{ name: "Wheel", mesh: 0, translation: [6, 0, 0] }] });
    const base = await side({ nodes: [{ name: "Wheel", mesh: 0 }] });
    const overlay = buildOverlay({ head, base, changes: [change("Wheel", "modified", ["translation"])] });

    // The arrowhead is a Mesh in this group too, and it is deliberately solid.
    const ghosts = meshesIn(overlay.movedGroup!).filter((m) => m.name !== "fhr-motion-head");
    expect(ghosts.length).toBeGreaterThan(0);
    for (const ghost of ghosts) {
      expect((materialOf(ghost) as unknown as { wireframe?: boolean }).wireframe).toBe(true);
    }
    const arrow = meshesIn(overlay.movedGroup!).find((m) => m.name === "fhr-motion-head")!;
    expect((materialOf(arrow) as unknown as { wireframe?: boolean }).wireframe).toBeFalsy();
    // The current position stays solid — that contrast is the whole signal.
    for (const mesh of meshesIn(overlay.headGroup)) {
      expect((materialOf(mesh) as unknown as { wireframe?: boolean }).wireframe).toBeFalsy();
    }
  });

  it("puts an arrowhead on the destination end, so direction is stated not inferred", async () => {
    const head = await side({ nodes: [{ name: "Wheel", mesh: 0, translation: [6, 0, 0] }] });
    const base = await side({ nodes: [{ name: "Wheel", mesh: 0 }] });
    const overlay = buildOverlay({ head, base, changes: [change("Wheel", "modified", ["translation"])] });

    const arrow = overlay.movedGroup!.children.find((c) => c.name === "fhr-motion-head");
    expect(arrow).toBeDefined();

    // Read the endpoints off the line rather than restating them, so the test
    // can't drift from how the vector is actually anchored.
    const line = overlay.movedGroup!.children.find((c) => c.name === "fhr-motion")!;
    const p = (line as unknown as { geometry: { attributes: { position: { array: ArrayLike<number> } } } })
      .geometry.attributes.position.array;
    const from = new Vector3(p[0]!, p[1]!, p[2]!);
    const to = new Vector3(p[3]!, p[4]!, p[5]!);
    const direction = new Vector3().subVectors(to, from).normalize();
    const length = from.distanceTo(to) * 0.22;

    // The cone's origin is its centre, so it sits half a length back from `to` —
    // which puts the *tip* exactly on the destination.
    const tip = arrow!.position.clone().addScaledVector(direction, length / 2);
    expect(tip.distanceTo(to)).toBeLessThan(1e-6);
    // And it points down the travel direction, not back along it.
    const aim = new Vector3(0, 1, 0).applyQuaternion(arrow!.quaternion);
    expect(aim.dot(direction)).toBeCloseTo(1, 5);
  });

  it("scales the arrowhead with the move, not with the model", async () => {
    const far = buildOverlay({
      head: await side({ nodes: [{ name: "P", mesh: 0, translation: [100, 0, 0] }] }),
      base: await side({ nodes: [{ name: "P", mesh: 0 }] }),
      changes: [change("P", "modified", ["translation"])],
    });
    const near = buildOverlay({
      head: await side({ nodes: [{ name: "P", mesh: 0, translation: [2, 0, 0] }] }),
      base: await side({ nodes: [{ name: "P", mesh: 0 }] }),
      changes: [change("P", "modified", ["translation"])],
    });
    const height = (o: typeof far) => {
      const cone = o.movedGroup!.children.find((c) => c.name === "fhr-motion-head")!;
      return (cone as unknown as { geometry: { parameters: { height: number } } }).geometry.parameters.height;
    };
    // A 100-unit move gets a proportionally bigger head than a 2-unit one, so a
    // tiny nudge never gets a cone larger than the move it describes.
    expect(height(far)).toBeGreaterThan(height(near) * 10);
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
    const hidden = meshesIn(overlay.baseGhostGroup!).filter((m) => !m.visible);
    // Both, for different reasons: "Gone" is drawn as a removed ghost instead,
    // and "Keep" is identical to its head twin at the same world position.
    expect(hidden).toHaveLength(2);
    expect(meshesIn(overlay.baseGhostGroup!).filter((m) => m.visible)).toHaveLength(0);
  });

  // Regression: the ghost used to include every unchanged node, so unchanged
  // geometry existed twice at the same coordinates — once opaque in the head
  // model, once translucent here. Transparency draws after the opaque pass with
  // depthWrite off and an equal-depth test that passes, so the two coincident
  // surfaces blended wherever float depth tied, and faces visibly winked in and
  // out as the camera orbited.
  it("never ghosts a node the diff doesn't mention", async () => {
    const scene: FixtureSpec = {
      nodes: [
        { name: "Quiet", mesh: 0 },
        { name: "Loud", mesh: 0, translation: [3, 0, 0] },
      ],
    };
    const head = await side(scene);
    const base = await side(scene);
    const overlay = buildOverlay({ head, base, changes: [change("Loud", "modified", ["mesh"])] });

    const named = (visible: boolean) =>
      meshesIn(overlay.baseGhostGroup!)
        .filter((m) => m.visible === visible)
        .length;
    // "Loud" is in the diff but has no transform change, so it has no grammar of
    // its own and legitimately belongs to the ghost. "Quiet" must not be there.
    expect(named(true)).toBe(1);
    expect(named(false)).toBe(1);
  });

  it("ghosts nothing at all when no change resolves to a base node", async () => {
    const scene = { nodes: [{ name: "Solo", mesh: 0 }] };
    const overlay = buildOverlay({ head: await side(scene), base: await side(scene), changes: [] });
    expect(meshesIn(overlay.baseGhostGroup!).filter((m) => m.visible)).toHaveLength(0);
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

  it("keeps a box per change path, so a change list can fly to one (#45)", async () => {
    const head = await side(TWO_NODES);
    const overlay = buildOverlay({
      head,
      changes: [change("Hood", "modified", ["mesh"]), change("Mirror", "added")],
    });
    expect([...overlay.boxByChangePath.keys()].sort()).toEqual(["nodes/Hood", "nodes/Mirror"]);
    expect(overlay.boxByChangePath.get("nodes/Mirror")!.min.x).toBeCloseTo(4);
    expect(overlay.objectsByChangePath.get("nodes/Hood")).toHaveLength(1);
  });

  it("leaves changeBox empty when nothing was painted", async () => {
    const overlay = buildOverlay({ head: await side(TWO_NODES), changes: [] });
    expect(overlay.changeBox.isEmpty()).toBe(true);
  });

  it("frames a node the diff never mentioned, for the structure tree", async () => {
    // The whole reason the tree is a separate region: a reviewer can reach an
    // *unchanged* part for context, and framing one needs a box that no painting
    // pass produced.
    const overlay = buildOverlay({
      head: await side(TWO_NODES),
      changes: [change("Hood", "modified", ["mesh"])],
    });
    expect(overlay.boxByChangePath.has("nodes/Mirror")).toBe(false);
    expect(overlay.boxOfNode("Mirror")!.min.x).toBeCloseTo(4);
    expect(overlay.boxOfNode("Hood")!.min.x).toBeCloseTo(0);
  });

  it("returns null for a name this file's scene graph doesn't have", async () => {
    const overlay = buildOverlay({ head: await side(TWO_NODES), changes: [] });
    expect(overlay.boxOfNode("NotHere")).toBeNull();
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
    const objects = overlay.objectsByChangePath.get("nodes/Hood")!;
    expect(objects.length).toBeGreaterThan(0);
    for (const object of objects) expect(overlay.changePathByObject.get(object)).toBe("nodes/Hood");
  });

  it("resolves a click on a mesh inside a painted node", async () => {
    const head = await side(TWO_NODES);
    const overlay = buildOverlay({ head, changes: [change("Hood", "modified", ["mesh"])] });
    const hood = meshesIn(head.gltf.scene).find((m) => m.name === "Hood")!;
    expect(changeAtObject(hood, { changePathByObject: overlay.changePathByObject })).toBe("nodes/Hood");
  });

  it("resolves a click on the ghost of a removed part, which no association covers", async () => {
    const head = await side({ nodes: [{ name: "Hood", mesh: 0 }] });
    const base = await side(TWO_NODES);
    const overlay = buildOverlay({ head, base, changes: [change("Mirror", "removed")] });
    const ghost = overlay.removedGroup!.children[0]!;
    // The clone is in no glTF association — the loader never made it.
    expect(overlay.nodeIndexOfObject(ghost)).toBeNull();
    expect(changeAtObject(meshesIn(ghost)[0] ?? ghost, { changePathByObject: overlay.changePathByObject })).toBe(
      "nodes/Mirror",
    );
  });

  it("resolves nothing for a click on unchanged geometry", async () => {
    const head = await side(TWO_NODES);
    const overlay = buildOverlay({ head, changes: [change("Hood", "modified", ["mesh"])] });
    const mirror = meshesIn(head.gltf.scene).find((m) => m.name === "Mirror")!;
    expect(changeAtObject(mirror, { changePathByObject: overlay.changePathByObject })).toBeNull();
  });

  it("offers the loader's node association as the fallback path", async () => {
    const head = await side(TWO_NODES);
    const overlay = buildOverlay({ head, changes: [change("Mirror", "modified", ["mesh"])] });
    const mirror = meshesIn(head.gltf.scene).find((m) => m.name === "Mirror")!;
    const index = overlay.nodeIndexOfObject(mirror);
    expect(index).not.toBeNull();
    expect(overlay.changePathByNodeIndex.get(index!)).toBe("nodes/Mirror");
    // The same answer, reached without the painted map.
    expect(
      changeAtObject(mirror, {
        changePathByObject: new Map(),
        nodeIndexOf: overlay.nodeIndexOfObject,
        changePathByNodeIndex: overlay.changePathByNodeIndex,
      }),
    ).toBe("nodes/Mirror");
  });
});

// #51: a mesh is drawn once per node instancing it, and a material reaches
// geometry only through the primitives referencing it. Before this, both classes
// of change were detected, listed, formatted and headlined — and highlighted
// nowhere, which made a real diff look like an unchanged model.
describe("meshes and materials, painted through the geometry that carries them", () => {
  const entity = (name: string, kind: NodeChange["kind"] = "modified", primitives: number[] = []) => ({
    name,
    kind,
    fields: [],
    path: `meshes/${name}`,
    primitives,
  });

  it("paints every node instancing a changed mesh, not just the first", async () => {
    const head = await side({
      nodes: [
        { name: "Wheel_FL", mesh: 0, translation: [0, 0, 0] },
        { name: "Wheel_FR", mesh: 0, translation: [3, 0, 0] },
        { name: "Wheel_RL", mesh: 0, translation: [0, 0, 3] },
      ],
      meshName: "WheelMesh",
    });
    const overlay = buildOverlay({ head, changes: [], meshes: [entity("WheelMesh")] });

    expect(overlay.stats.tinted).toBe(3);
    expect(overlay.objectsByChangePath.get("meshes/WheelMesh")).toHaveLength(3);
    // The framing box spans all three, so the camera doesn't fly to one wheel.
    const box = overlay.boxByChangePath.get("meshes/WheelMesh")!;
    expect(box.max.x - box.min.x).toBeGreaterThan(3);
  });

  it("paints only the primitives using a changed material", async () => {
    const head = await side({
      nodes: [{ name: "Part", mesh: 0 }],
      primitives: 3,
      materialNames: ["Body", "Trim"],
      primitiveMaterials: [0, 1, 0],
    });
    const overlay = buildOverlay({
      head,
      changes: [],
      materials: [{ ...entity("Trim"), path: "materials/Trim" }],
    });
    // Trim is on one primitive of three: the trim lights up, not the whole part.
    expect(overlay.stats.tinted).toBe(1);
    expect(overlay.objectsByChangePath.get("materials/Trim")).toHaveLength(1);
  });

  it("narrows a mesh change to the primitive ordinal it names", async () => {
    const head = await side({ nodes: [{ name: "Part", mesh: 0 }], primitives: 3 });
    const overlay = buildOverlay({ head, changes: [], meshes: [entity("Tri", "modified", [1])] });
    expect(overlay.stats.tinted).toBe(1);
  });

  it("quiets the rest of the model once a mesh change paints", async () => {
    const head = await side({
      nodes: [
        { name: "Body", mesh: 0 },
        { name: "Trim", mesh: 0 },
      ],
    });
    // Regression: with nothing paintable the quieting pass is skipped and the
    // model renders in authored colours, so a real diff looked untouched.
    const overlay = buildOverlay({ head, changes: [], meshes: [entity("Tri")] });
    expect(overlay.stats.tinted).toBeGreaterThan(0);
    expect(overlay.stats.desaturated).toBe(0); // both nodes instance the changed mesh
  });

  it("counts and explains a change with nowhere to go, instead of staying silent", async () => {
    const head = await side({ nodes: [{ name: "Part", mesh: 0 }] });
    const overlay = buildOverlay({
      head,
      changes: [],
      materials: [{ ...entity("Ghostly"), path: "materials/Ghostly" }],
      unpaintable: [{ ...entity("Spin"), path: "animations/Spin" }],
    });
    expect(overlay.stats.unpaintable).toBe(2); // unreferenced material + animation
    expect(overlay.stats.tinted).toBe(0);
    expect(overlay.notes.join(" ")).toContain("no place on the model");
  });

  it("does not throw on a mesh nothing instances", async () => {
    const head = await side({ nodes: [{ name: "Part", mesh: 0 }] });
    expect(() => buildOverlay({ head, changes: [], meshes: [entity("Absent")] })).not.toThrow();
  });

  it("resolves a renamed node against each file's own name (#47)", async () => {
    // The head file knows it as Fender and the base file as Cube.003. Looking the
    // base up under the head's name is how a renamed-and-moved node silently loses
    // its ghost and its motion vector: nothing in the previous version answers to
    // the new name.
    const head = await side({ nodes: [{ name: "Fender", mesh: 0, translation: [6, 0, 0] }] });
    const base = await side({ nodes: [{ name: "Cube.003", mesh: 0, translation: [0, 0, 0] }] });
    const overlay = buildOverlay({
      head,
      base,
      changes: [{ ...change("Fender", "renamed", ["translation"]), oldName: "Cube.003" }],
    });

    expect(overlay.stats.unmatched).toBe(0);
    expect(overlay.stats.moveGhosts).toBe(1);
    expect(overlay.stats.motionVectors).toBe(1);
    // Tinted at the new name, in renamed's own colour — not ghosted as a removal.
    expect(overlay.stats.removedGhosts).toBe(0);
    const painted = meshesIn(head.gltf.scene)[0]!;
    expect(distanceTo(painted, KIND_COLOR["renamed"]!)).toBeLessThan(
      distanceTo(painted, KIND_COLOR["removed"]!),
    );
  });

  it("says so when a rename's previous name matched several base nodes (#47)", async () => {
    // `before` is the bare old name, which is all a rename can carry — an array
    // index would name whatever sits at that number now. When the previous version
    // spelled two nodes the same way, the ghost comes from the first of them, and
    // that guess has to be visible rather than a confident picture of a move.
    const head = await side({
      nodes: [
        { name: "Wheel", mesh: 0, translation: [1, 0, 0] },
        { name: "Tire", mesh: 0, translation: [5, 0, 0] },
      ],
    });
    const base = await side({
      nodes: [
        { name: "Wheel", mesh: 0, translation: [1, 0, 0] },
        { name: "Wheel", mesh: 0, translation: [2, 0, 0] },
      ],
    });
    const overlay = buildOverlay({
      head,
      base,
      changes: [{ ...change("Tire", "renamed", ["translation"]), oldName: "Wheel" }],
    });

    expect(overlay.stats.moveGhosts).toBe(1);
    expect(overlay.stats.motionVectors).toBe(1);
    expect(overlay.notes.join(" ")).toContain('2 nodes in the previous version are called "Wheel"');
  });

  it("ghosts a deletion whose name a rename took over (#47)", async () => {
    // The base file has A and B; the head file has one node, called B, that the
    // authored id says used to be A. So "B" names two different objects across
    // the pair: one renamed into it and one deleted out of it. The handler keeps
    // the two changes on separate paths; the ghost has to come from the deleted
    // node, and the node that merely inherited the name must not get an arrow
    // drawn to it from where the dead one used to stand.
    const head = await side({ nodes: [{ name: "B", mesh: 0, translation: [1, 0, 0] }] });
    const base = await side({
      nodes: [
        { name: "A", mesh: 0, translation: [1, 0, 0] },
        { name: "B", mesh: 0, translation: [8, 0, 0] },
      ],
    });
    const overlay = buildOverlay({
      head,
      base,
      changes: [
        { name: "B", kind: "renamed", fields: [], path: "nodes/B", oldName: "A" },
        { name: "B", kind: "removed", fields: ["translation", "mesh"], path: "nodes/B#1" },
      ],
    });

    expect(overlay.stats.removedGhosts).toBe(1);
    expect(overlay.stats.moveGhosts).toBe(0);
    expect(overlay.stats.motionVectors).toBe(0);
    expect(overlay.stats.unmatched).toBe(0);
    // Ghosted from the node that was actually deleted — base "B", out at x=8.
    const ghost = overlay.removedGroup!.children[0]!;
    expect(ghost.matrix.elements[12]).toBeCloseTo(8);
  });

  it("paints a rename with no transform change like a modification: tint, no ghost", async () => {
    const head = await side({ nodes: [{ name: "Fender", mesh: 0 }] });
    const base = await side({ nodes: [{ name: "Cube.003", mesh: 0 }] });
    const overlay = buildOverlay({
      head,
      base,
      changes: [{ ...change("Fender", "renamed"), oldName: "Cube.003" }],
    });
    expect(overlay.stats.tinted).toBe(1);
    expect(overlay.stats.moveGhosts).toBe(0);
    expect(overlay.stats.removedGhosts).toBe(0);
    expect(overlay.objectsByChangePath.has("nodes/Fender")).toBe(true);
  });

  it("merges a node's own change with one on the mesh it instances", async () => {
    const head = await side({ nodes: [{ name: "Body", mesh: 0, translation: [2, 0, 0] }] });
    const base = await side({ nodes: [{ name: "Body", mesh: 0 }] });
    const overlay = buildOverlay({
      head,
      base,
      changes: [change("Body", "modified", ["translation"])],
      meshes: [entity("Tri")],
    });
    // Both are reported: the node moved and its geometry changed.
    expect(overlay.stats.moveGhosts).toBe(1);
    expect(overlay.objectsByChangePath.has("nodes/Body")).toBe(true);
    expect(overlay.objectsByChangePath.has("meshes/Tri")).toBe(true);
    expect(overlay.stats.unpaintable).toBe(0);
  });
});

// A name is not a change's identity (#47), and the overlay is where that stops
// being an abstract claim: two changes about two different objects can carry the
// same name, and every map the viewport reads has to keep them apart.
//
// The pair below is the one the handler emits for the stamped-file case the whole
// issue exists for — base [B, A(fhr_uid=u1)], head [B(fhr_uid=u1)] — where "B" is
// at once a node that was deleted and the name an unrelated node was renamed into.
// The removal is emitted FIRST here, which is the order that made keying by name
// lose the rename; the reverse order lost the deletion. Both are pinned.
describe("two changes, one name (#47)", () => {
  const COLLIDED_HEAD: FixtureSpec = { nodes: [{ name: "B", mesh: 0, translation: [1, 0, 0] }] };
  const COLLIDED_BASE: FixtureSpec = {
    nodes: [
      { name: "A", mesh: 0, translation: [1, 0, 0] },
      { name: "B", mesh: 0, translation: [8, 0, 0] },
    ],
  };
  const REMOVED_FIRST: NodeChange[] = [
    { name: "B", kind: "removed", fields: ["translation", "mesh"], path: "nodes/B#1" },
    { name: "B", kind: "renamed", fields: [], path: "nodes/B", oldName: "A" },
  ];
  const RENAME_FIRST: NodeChange[] = [REMOVED_FIRST[1]!, REMOVED_FIRST[0]!];

  /**
   * The #45 round trip the host drives, in the three lookups scene-3d.ts makes:
   * a row selected by path → the objects, the box and the callout it selects.
   */
  const select = (overlay: ReturnType<typeof buildOverlay>, changes: NodeChange[], path: string) => {
    const resolved = selectionKeys(changes).changePathOf(path);
    if (resolved === null) return null;
    const headlines: Record<string, string> = {
      "nodes/B#1": "removed",
      "nodes/B": "renamed A → B",
    };
    return {
      objects: overlay.objectsByChangePath.get(resolved) ?? [],
      box: overlay.boxByChangePath.get(resolved) ?? null,
      label: overlay.labelByChangePath.get(resolved) ?? resolved,
      headline: headlines[resolved] ?? "changed",
    };
  };

  for (const [order, changes] of [
    ["removal first", REMOVED_FIRST],
    ["rename first", RENAME_FIRST],
  ] as const) {
    it(`selects the surviving node for the rename's row, ${order}`, async () => {
      const overlay = buildOverlay({
        head: await side(COLLIDED_HEAD),
        base: await side(COLLIDED_BASE),
        changes,
      });
      const hit = select(overlay, changes, "nodes/B")!;
      // The head node that inherited the name, at x=1 — NOT the ghost of the
      // deleted node out at x=8, which is what a name-keyed lookup returned.
      expect(hit.objects).toHaveLength(1);
      expect(overlay.removedGroup!.children).not.toContain(hit.objects[0]);
      expect(hit.box!.min.x).toBeCloseTo(1); // the fixture triangle spans x=0..1
      expect(hit.label).toBe("B");
      expect(hit.headline).toBe("renamed A → B");
    });

    it(`selects the deleted node's ghost for the removal's row, ${order}`, async () => {
      const overlay = buildOverlay({
        head: await side(COLLIDED_HEAD),
        base: await side(COLLIDED_BASE),
        changes,
      });
      const hit = select(overlay, changes, "nodes/B#1")!;
      expect(hit.objects).toEqual([overlay.removedGroup!.children[0]]);
      expect(hit.box!.min.x).toBeCloseTo(8);
      // The label is the name; the "#1" belongs to the key, not to the reviewer.
      expect(hit.label).toBe("B");
      expect(hit.headline).toBe("removed");
    });

    it(`gives the removal no claim on the head node it doesn't own, ${order}`, async () => {
      const head = await side(COLLIDED_HEAD);
      const overlay = buildOverlay({ head, base: await side(COLLIDED_BASE), changes });
      // The pick fallback: clicking the surviving node reports the rename, and the
      // node is tinted as renamed rather than in the deletion's colour.
      const mesh = meshesIn(head.gltf.scene)[0]!;
      const index = overlay.nodeIndexOfObject(mesh);
      expect(overlay.changePathByNodeIndex.get(index!)).toBe("nodes/B");
      expect(overlay.stats.tinted).toBe(1);
      expect(distanceTo(mesh, KIND_COLOR["renamed"]!)).toBeLessThan(
        distanceTo(mesh, KIND_COLOR["removed"]!),
      );
    });
  }

  // Same collision one level down: a mesh (or material) deleted while another is
  // renamed into the name it vacated. The removed one is not in the head file, so
  // resolving its name there could only ever find the survivor — and painting it
  // put the removal's colour on living geometry, on top of the rename's own paint,
  // with nothing reported as unpaintable.
  it("does not paint surviving geometry for a removed mesh whose name a rename took", async () => {
    const head = await side({ nodes: [{ name: "Part", mesh: 0 }], meshName: "Body" });
    const meshes = [
      { name: "Body", kind: "removed" as const, fields: [], path: "meshes/Body#1", primitives: [] },
      { name: "Body", kind: "renamed" as const, fields: [], path: "meshes/Body", primitives: [] },
    ];
    const overlay = buildOverlay({ head, changes: [], meshes });

    expect(overlay.stats.tinted).toBe(1); // once, by the rename
    expect(overlay.objectsByChangePath.has("meshes/Body")).toBe(true);
    expect(overlay.objectsByChangePath.has("meshes/Body#1")).toBe(false);
    // Counted, so the banner says the deleted mesh isn't on this model.
    expect(overlay.stats.unpaintable).toBe(1);
    const mesh = meshesIn(head.gltf.scene)[0]!;
    expect(distanceTo(mesh, KIND_COLOR["renamed"]!)).toBeLessThan(
      distanceTo(mesh, KIND_COLOR["removed"]!),
    );
  });

  it("does not paint surviving geometry for a removed material whose name a rename took", async () => {
    const head = await side({ nodes: [{ name: "Part", mesh: 0 }], materialNames: ["Body"] });
    const materials = [
      { name: "Body", kind: "removed" as const, fields: [], path: "materials/Body#1", primitives: [] },
      { name: "Body", kind: "renamed" as const, fields: [], path: "materials/Body", primitives: [] },
    ];
    const overlay = buildOverlay({ head, changes: [], materials });

    expect(overlay.stats.tinted).toBe(1);
    expect(overlay.objectsByChangePath.has("materials/Body")).toBe(true);
    expect(overlay.objectsByChangePath.has("materials/Body#1")).toBe(false);
    expect(overlay.stats.unpaintable).toBe(1);
    const mesh = meshesIn(head.gltf.scene)[0]!;
    expect(distanceTo(mesh, KIND_COLOR["renamed"]!)).toBeLessThan(
      distanceTo(mesh, KIND_COLOR["removed"]!),
    );
  });
});

describe("taking the paint off — what a side-by-side pane draws", () => {
  /**
   * The paint is a mutation of the head model's own materials, so a pane that
   * hides every diff *group* is still looking at it. These pin the way back.
   */
  it("restores the file's own materials on every mesh it painted", async () => {
    const head = await side(TWO_NODES);
    const authored = new Map(meshesIn(head.gltf.scene).map((m) => [m.name, materialOf(m)]));
    const overlay = buildOverlay({ head, changes: [change("Hood", "modified", ["mesh"])] });
    // Both halves of the paint are in play: one tinted mesh, one desaturated.
    expect(overlay.stats).toMatchObject({ tinted: 1, desaturated: 1 });
    for (const mesh of meshesIn(head.gltf.scene)) {
      expect(materialOf(mesh)).not.toBe(authored.get(mesh.name));
    }

    overlay.setPaint(false);
    for (const mesh of meshesIn(head.gltf.scene)) {
      expect(materialOf(mesh)).toBe(authored.get(mesh.name));
    }

    overlay.setPaint(true);
    for (const mesh of meshesIn(head.gltf.scene)) {
      expect(materialOf(mesh)).not.toBe(authored.get(mesh.name));
    }
  });

  it("shows the current version's own colours, not the diff's", async () => {
    // The reviewer's complaint the mode exists to answer: with the paint on, the
    // changed part is the palette's orange and everything else is grey, so the
    // two panes are not comparable — neither colour is in either file.
    const head = await side(TWO_NODES);
    const authored = new Map(meshesIn(head.gltf.scene).map((m) => [m.name, hexOf(m)]));
    const overlay = buildOverlay({ head, changes: [change("Hood", "modified", ["mesh"])] });
    const byName = (): Map<string, number> =>
      new Map(meshesIn(head.gltf.scene).map((m) => [m.name, hexOf(m)]));

    expect(byName().get("Hood")).not.toBe(authored.get("Hood")); // tinted
    expect(byName().get("Mirror")).not.toBe(authored.get("Mirror")); // desaturated

    overlay.setPaint(false);
    expect(byName().get("Hood")).toBe(authored.get("Hood"));
    expect(byName().get("Mirror")).toBe(authored.get("Mirror"));
  });

  it("keeps the file's material as the original when one mesh is painted twice", async () => {
    // A node change and a material change can both land on one primitive. The
    // second swap displaces *our* tint, which must not be mistaken for the file's.
    const head = await side({ nodes: [{ name: "Hood", mesh: 0 }], materialNames: ["Paint"] });
    const authored = materialOf(meshesIn(head.gltf.scene)[0]!);
    const overlay = buildOverlay({
      head,
      changes: [change("Hood", "modified", ["mesh"])],
      materials: [{ name: "Paint", kind: "modified", fields: [], path: "materials/Paint", primitives: [] }],
    });
    expect(materialOf(meshesIn(head.gltf.scene)[0]!)).not.toBe(authored);
    overlay.setPaint(false);
    expect(materialOf(meshesIn(head.gltf.scene)[0]!)).toBe(authored);
  });

  it("frees the same materials whichever way the paint was left", async () => {
    // Disposal walks what is *attached*; with the paint off the tint clones hang
    // off nothing, so leaving the model unpainted must not leak them.
    const asPainted = buildOverlay({
      head: await side(TWO_NODES),
      changes: [change("Hood", "modified", ["mesh"])],
    }).dispose();

    const stripped = buildOverlay({
      head: await side(TWO_NODES),
      changes: [change("Hood", "modified", ["mesh"])],
    });
    stripped.setPaint(false);
    expect(stripped.dispose().materials).toBe(asPainted.materials);
    expect(asPainted.materials).toBeGreaterThan(0);
  });
});

describe("the structure tree's root row", () => {
  const CAR: FixtureSpec = { ...TWO_NODES, sceneName: "Car" };

  async function loaded(spec: FixtureSpec): Promise<{ head: LoadedSide; doc: ReturnType<typeof decodeGltf> }> {
    const bytes = toGlb(buildGltf(spec));
    const { gltf } = await loadGltf(toArrayBuffer(bytes));
    const doc = decodeGltf(bytes);
    return { head: { gltf, index: buildNameIndex(doc) }, doc };
  }

  it("frames the whole current version, though it is a scene and not a node", async () => {
    const { head, doc } = await loaded(CAR);
    const overlay = buildOverlay({ head, changes: [change("Hood", "modified", ["mesh"])] });

    // The rows the tree renders, exactly as index-3d builds them.
    const rows = buildSceneGraph(parseGltf(doc), new Map());
    expect(rows.map((r) => r.name)).toEqual(["Car", "Hood", "Mirror"]);
    expect(rows[0]!.depth).toBe(0);
    expect(overlay.sceneRootName).toBe("Car");
    // It is in no node index — which is what used to make the row inert.
    expect(resolveNodeIndex(head.index, "Car").index).toBeNull();

    // Every row the tree offers now resolves to a box the camera can fly to …
    const boxes = rows.map((r) => overlay.boxOfNode(r.name));
    expect(boxes.every((b) => b !== null)).toBe(true);
    // … and the root's is the whole model: it encloses every other row's.
    for (const part of boxes.slice(1)) expect(boxes[0]!.containsBox(part!)).toBe(true);
  });

  it("answers for no scene name when the tree has no synthetic root", async () => {
    // One root node: parseGltf hangs the tree off it directly, so "Car" names a
    // row that does not exist and must not silently frame the whole model.
    const { head, doc } = await loaded({
      nodes: [{ name: "Rig", children: [1] }, { name: "Hood", mesh: 0 }],
      sceneName: "Car",
    });
    const overlay = buildOverlay({ head, changes: [] });
    expect(parseGltf(doc).some((e) => e.name === "Car")).toBe(false);
    expect(overlay.sceneRootName).toBeNull();
    expect(overlay.boxOfNode("Car")).toBeNull();
  });

  it("lets a real node win the name when a node and the scene share one", async () => {
    const { head } = await loaded({
      nodes: [
        { name: "Car", mesh: 0, translation: [0, 0, 0] },
        { name: "Mirror", mesh: 0, translation: [4, 0, 0] },
      ],
      sceneName: "Car",
    });
    const overlay = buildOverlay({ head, changes: [] });
    const node = overlay.boxOfNode("Car")!;
    expect(node.max.x).toBeLessThan(4); // the node alone, not the pair
  });
});
