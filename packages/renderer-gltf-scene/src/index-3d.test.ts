// The routing every selection from outside the 3D view goes through, headless.
//
// `mount3d` itself needs WebGL, so what is testable here is the part that has no
// pixels in it: which key each of the three surfaces is handed when a change-tree
// row, an `n`/`p` step or a host push arrives. That is the whole of the queue's
// position readout being right, and it is the one thing the reviewer's two
// on-screen lists can disagree about.

import { describe, it, expect } from "vitest";
import { routeSelection, type SelectionSurfaces } from "./index-3d.js";
import { selectionKeys } from "./selection-keys.js";
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
    selectChange(name: string | null): boolean {
      framed.push(name);
      return name === null || painted.includes(name);
    },
  };
  return { chrome, keys: selectionKeys(changes), handle, queued, highlighted, framed };
}

describe("routeSelection", () => {
  // A plain animated file: one node moved, one animation sampler re-baked. Both
  // are queue stops (every two-segment path is), and only the first has a node —
  // so the keys are built from the node change alone, exactly as the mount does
  // (index-3d.ts hands `selectionKeys` nodes, meshes and materials; animations
  // have nowhere in the scene to resolve to).
  const animated = (): Surfaces => surfaces([node("Base", "nodes/Base")], ["Base"]);

  it("gives the queue the path it was handed, and the tree the node name", () => {
    const s = animated();
    routeSelection(s, "nodes/Base");
    expect(s.queued).toEqual(["nodes/Base"]);
    expect(s.highlighted).toEqual(["Base"]);
    expect(s.framed).toEqual(["Base"]);
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
      ["Base"],
    );
    routeSelection(s, "meshes/Base");
    expect(s.queued).toEqual(["meshes/Base"]);
    // The scene and the tree still work in node names, and "Base" is the node.
    expect(s.highlighted).toEqual(["Base"]);
    expect(s.framed).toEqual(["Base"]);
  });

  it("routes a field row to the stop that owns it", () => {
    const s = animated();
    routeSelection(s, "nodes/Base/translation");
    expect(s.queued).toEqual(["nodes/Base"]);
    expect(s.highlighted).toEqual(["Base"]);
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
    const s = animated();
    // Painted: the scene framed it.
    expect(routeSelection(s, "nodes/Base")).toBe(true);
    // A node the diff names but the model doesn't carry.
    expect(routeSelection(s, "nodes/Ghost")).toBe(false);
    // No node at all: nothing to frame, but the queue still moved.
    expect(routeSelection(s, "animations/Spin")).toBe(false);
    expect(s.queued).toEqual(["nodes/Base", "nodes/Ghost", "animations/Spin"]);
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
