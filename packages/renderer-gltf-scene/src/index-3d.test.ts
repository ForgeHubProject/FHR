// The routing every selection from outside the 3D view goes through, headless.
//
// `mount3d` itself needs WebGL, so what is testable here is the part that has no
// pixels in it: which key each of the three surfaces is handed when a change-tree
// row, an `n`/`p` step or a host push arrives. That is the whole of the queue's
// position readout being right, and it is the one thing the reviewer's two
// on-screen lists can disagree about.

import { describe, it, expect } from "vitest";
import { indirectPaint, routeSelection, structureRows, type SelectionSurfaces } from "./index-3d.js";
import { selectionKeys } from "./selection-keys.js";
import { buildNameIndex } from "./node-index.js";
import type { GltfDocument } from "./gltf-parse.js";
import type { ChangeKind, DiffChange, MountProps } from "@fhr/types";
import type { EntityChange, NodeChange } from "./diff-map.js";
import type { Chrome } from "./chrome.js";
import type { SceneHandle } from "./scene-3d.js";

const node = (name: string, path: string): NodeChange => ({ name, path, kind: "modified", fields: [] });
const entity = (name: string, path: string): EntityChange => ({ ...node(name, path), primitives: [] });

type Surfaces = SelectionSurfaces & {
  /** What the change queue was told to select, in order. */
  queued: (string | null)[];
  /** What the structure tree was told to highlight, in order. */
  highlighted: (string | null)[];
  /** What the WebGL scene was asked to frame, in order. */
  framed: (string | null)[];
};

/** `painted` is the change *paths* the scene can show — its own key since #47. */
function surfaces(changes: (NodeChange | EntityChange)[], painted: string[]): Surfaces {
  const queued: (string | null)[] = [];
  const highlighted: (string | null)[] = [];
  const framed: (string | null)[] = [];
  const chrome = {
    selectChange: (path: string | null) => queued.push(path),
    highlightNode: (name: string | null) => highlighted.push(name),
  } as unknown as Chrome;
  const handle: SceneHandle = {
    dispose(): void {},
    selectChange(path: string | null): boolean {
      framed.push(path);
      return path === null || painted.includes(path);
    },
  };
  // The mount's `rowOf`, in the only part of it these cases exercise: a node
  // change's row is the node itself. A mesh or material change's row is
  // whichever nodes draw its geometry, which needs the file — so it is null
  // here, and the describe below supplies a real one.
  const rowOf = (path: string): string | null =>
    changes.find((c) => c.path === path && path.startsWith("nodes/"))?.name ?? null;
  return { chrome, keys: selectionKeys(changes), handle, rowOf, queued, highlighted, framed };
}

describe("routeSelection", () => {
  // A plain animated file: one node moved, one animation sampler re-baked. Both
  // are queue stops (every two-segment path is), and only the first has a node —
  // so the keys are built from the node change alone, exactly as the mount does
  // (index-3d.ts hands `selectionKeys` nodes, meshes and materials; animations
  // have nowhere in the scene to resolve to).
  const animated = (): Surfaces => surfaces([node("Base", "nodes/Base")], ["nodes/Base"]);

  it("gives the queue and the scene the path, and the tree the node name", () => {
    const s = animated();
    routeSelection(s, "nodes/Base");
    expect(s.queued).toEqual(["nodes/Base"]);
    expect(s.highlighted).toEqual(["Base"]);
    expect(s.framed).toEqual(["nodes/Base"]);
  });

  it("keeps the queue on a change with no node behind it", () => {
    // The regression this pins: routing the path through the node name and back
    // returned null for an animation, which cleared the row highlight, emptied
    // the panel and reset the position readout to the size of the job — while
    // the change tree beside it still highlighted the right row.
    const s = animated();
    routeSelection(s, "animations/Spin");
    expect(s.queued).toEqual(["animations/Spin"]);
    // Nothing in the scene to frame or highlight, and the handle says so.
    expect(s.highlighted).toEqual([null]);
    expect(s.framed).toEqual([]);
  });

  it("keeps the queue on the row it was given when a mesh and a node share a name", () => {
    // Routine exporter output: the mesh is named after the node instancing it.
    // Name → path is first-wins, so the round trip walked the queue backwards
    // onto "nodes/Base" and reported a lower position number.
    const s = surfaces(
      [node("Base", "nodes/Base"), entity("Base", "meshes/Base")],
      ["meshes/Base"],
    );
    routeSelection(s, "meshes/Base");
    expect(s.queued).toEqual(["meshes/Base"]);
    // The scene is keyed on the path too, so it lands on the mesh change and not
    // on the node that happens to share its name. (Which tree row lights up is
    // the mount's `rowOf`; see the describe below.)
    expect(s.framed).toEqual(["meshes/Base"]);
  });

  // The #47 case, end to end through the router: one diff, one name, two
  // changes. Each has to reach its own object, in either emission order.
  it("tells a deletion from a rename into the name it vacated", () => {
    const s = surfaces(
      [node("Wheel", "nodes/Wheel#1"), node("Wheel", "nodes/Wheel")],
      ["nodes/Wheel#1", "nodes/Wheel"],
    );
    routeSelection(s, "nodes/Wheel#1");
    routeSelection(s, "nodes/Wheel");
    expect(s.queued).toEqual(["nodes/Wheel#1", "nodes/Wheel"]);
    expect(s.framed).toEqual(["nodes/Wheel#1", "nodes/Wheel"]);
  });

  it("routes a field row to the stop that owns it", () => {
    const s = animated();
    routeSelection(s, "nodes/Base/translation");
    expect(s.queued).toEqual(["nodes/Base"]);
    expect(s.highlighted).toEqual(["Base"]);
    expect(s.framed).toEqual(["nodes/Base"]);
    routeSelection(s, "animations/Spin/channels/0/output");
    expect(s.queued).toEqual(["nodes/Base", "animations/Spin"]);
  });

  it("clears every surface for a null selection", () => {
    const s = animated();
    expect(routeSelection(s, null)).toBe(true);
    expect(s.queued).toEqual([null]);
    expect(s.highlighted).toEqual([null]);
    expect(s.framed).toEqual([null]);
  });

  it("reports whether the scene could show it, without touching the chrome's answer", () => {
    // "Ghost" is in the diff but not in this model — a node the previous version
    // had. The scene cannot frame it; the queue still has a row for it.
    const s = surfaces(
      [node("Base", "nodes/Base"), node("Ghost", "nodes/Ghost")],
      ["nodes/Base"],
    );
    expect(routeSelection(s, "nodes/Base")).toBe(true);
    expect(routeSelection(s, "nodes/Ghost")).toBe(false);
    // No change at that path at all: nothing to frame, but the queue still moved.
    expect(routeSelection(s, "animations/Spin")).toBe(false);
    expect(s.queued).toEqual(["nodes/Base", "nodes/Ghost", "animations/Spin"]);
    expect(s.framed).toEqual(["nodes/Base", "nodes/Ghost"]);
  });

  it("moves the queue and the tree even before the scene exists", () => {
    // The fallback ladder mounts a chrome-less view, and the model handle is null
    // until the mount finishes; a selection arriving in between must not throw.
    const s = animated();
    expect(routeSelection({ ...s, handle: null }, "nodes/Base")).toBe(false);
    expect(s.queued).toEqual(["nodes/Base"]);
    expect(routeSelection({ ...s, chrome: null, handle: null }, "nodes/Base")).toBe(false);
  });
});

// #56 + #51: a mesh or material change is about geometry a node merely carries,
// and the structure tree's rows are nodes. The mount resolves one to the other;
// without it every step through a worklist of such changes handed the tree a key
// it has no row for, and the tree cleared instead of following.
describe("routeSelection — the tree row for a change named after geometry", () => {
  const rows = (s: Surfaces, rowOf: (path: string) => string | null): SelectionSurfaces => ({
    ...s,
    rowOf,
  });

  it("highlights the node a mesh change paints, not the mesh's own name", () => {
    const s = surfaces([entity("BodyMesh", "meshes/BodyMesh")], ["meshes/BodyMesh"]);
    routeSelection(rows(s, (path) => (path === "meshes/BodyMesh" ? "Body" : null)), "meshes/BodyMesh");
    expect(s.queued).toEqual(["meshes/BodyMesh"]);
    expect(s.highlighted).toEqual(["Body"]);
    // The scene is keyed on the path, same as the queue — only the tree is
    // re-keyed, because only the tree works in node names.
    expect(s.framed).toEqual(["meshes/BodyMesh"]);
  });

  it("gives a node change the node's own row", () => {
    const s = surfaces([node("Base", "nodes/Base")], ["nodes/Base"]);
    routeSelection(rows(s, (path) => (path === "nodes/Base" ? "Base" : null)), "nodes/Base");
    expect(s.highlighted).toEqual(["Base"]);
  });

  // An unreferenced mesh, an animation: real changes with nothing in a tree of
  // nodes. Highlighting the change's own key there would clear the tree anyway,
  // so the router says null rather than inventing a row name.
  it("clears the tree for a change no row carries", () => {
    const s = surfaces([entity("Spoiler", "meshes/Spoiler")], ["meshes/Spoiler"]);
    routeSelection(rows(s, () => null), "meshes/Spoiler");
    expect(s.queued).toEqual(["meshes/Spoiler"]);
    expect(s.highlighted).toEqual([null]);
  });

  it("still clears the tree for a null selection", () => {
    const s = surfaces([entity("BodyMesh", "meshes/BodyMesh")], ["meshes/BodyMesh"]);
    routeSelection(rows(s, () => "Body"), null);
    expect(s.highlighted).toEqual([null]);
  });
});

// The regression #56 made harmful by promoting the tree to a permanent region:
// `diffChangeTypes` is the node-level changes alone, so a diff that reaches the
// model through a mesh or a material annotated nothing. Every row of a
// regenerated-topology diff — the case presentation.ts is built around — read
// "unchanged" beside a viewport painting that geometry orange.
describe("structureRows — every row the viewport paints admits it", () => {
  const car: GltfDocument = {
    asset: { version: "2.0" },
    scene: 0,
    scenes: [{ name: "Car", nodes: [0, 1, 2] }],
    nodes: [
      { name: "Body", mesh: 0 },
      { name: "Wheel_FL", mesh: 1 },
      { name: "Wheel_FR", mesh: 1 },
    ],
    meshes: [
      { name: "BodyMesh", primitives: [{ material: 0 }] },
      { name: "WheelMesh", primitives: [{ material: 1 }] },
    ],
    materials: [{ name: "Paint" }, { name: "Rubber" }],
  };

  const collection = (collectionName: string, entries: [string, ChangeKind][]): DiffChange => ({
    path: collectionName,
    label: collectionName,
    kind: "modified",
    children: entries.map(([name, kind]) => ({
      path: `${collectionName}/${name}`,
      label: name,
      kind,
      children: [],
    })),
  });

  const rowsFor = (changes: DiffChange[]): Map<string, string> => {
    const props: MountProps = { mode: "diff", diff: { version: "1.0", format: "gltf-scene", changes } };
    const index = buildNameIndex(car);
    const rows = structureRows(car, props, indirectPaint(index, props));
    return new Map(rows.map((row) => [row.name, row.kind]));
  };

  it("marks every node instancing a changed mesh", () => {
    const kinds = rowsFor([collection("meshes", [["WheelMesh", "modified"]])]);
    expect(kinds.get("Wheel_FL")).toBe("modified");
    expect(kinds.get("Wheel_FR")).toBe("modified");
    // Nothing else moves: the tree is the whole model, not the change list.
    expect(kinds.get("Body")).toBe("unchanged");
  });

  it("marks the nodes a changed material actually reaches", () => {
    const kinds = rowsFor([collection("materials", [["Paint", "modified"]])]);
    expect(kinds.get("Body")).toBe("modified");
    expect(kinds.get("Wheel_FL")).toBe("unchanged");
  });

  it("keeps a node's own change when a mesh change reaches it too", () => {
    // The node change is the more specific fact, and the one the queue is keyed
    // on — a row that reported the mesh's kind instead would disagree with it.
    const kinds = rowsFor([
      collection("nodes", [["Body", "added"]]),
      collection("meshes", [["BodyMesh", "modified"]]),
    ]);
    expect(kinds.get("Body")).toBe("added");
  });

  it("leaves an untouched file untouched", () => {
    const kinds = rowsFor([]);
    expect([...kinds.values()].every((kind) => kind === "unchanged")).toBe(true);
  });

  it("degrades to no rows rather than failing the mount", () => {
    const props: MountProps = { mode: "diff" };
    const broken = { asset: { version: "2.0" } } as GltfDocument;
    expect(structureRows(broken, props, indirectPaint(buildNameIndex(broken), props))).toEqual([]);
  });
});
