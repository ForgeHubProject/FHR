import { describe, it, expect } from "vitest";
import type { ChangeKind } from "@fhr/types";
import { buildSceneGraph, KIND_COLOR, NEUTRAL } from "./scene-graph.js";
import type { Entity } from "./gltf-parse.js";

function entity(name: string, position?: [number, number, number], parent: string | null = null): Entity {
  return {
    id: name,
    entityId: name,
    parentEntityId: parent,
    kind: "part",
    name,
    path: name,
    transform: position ? { position, rotationEulerDeg: [0, 0, 0], scale: [1, 1, 1] } : null,
  };
}

describe("buildSceneGraph", () => {
  it("colours nodes by their change kind, keyed by the diff's raw node name", () => {
    const changeMap = new Map<string, ChangeKind>([
      ["Cube", "modified"],
      ["Lamp", "added"],
    ]);
    const nodes = buildSceneGraph([entity("Cube", [5, 0, 0]), entity("Lamp"), entity("Floor")], changeMap);

    const byName = Object.fromEntries(nodes.map((n) => [n.name, n]));
    expect(byName["Cube"]!.kind).toBe("modified");
    expect(byName["Cube"]!.color).toBe(KIND_COLOR["modified"]);
    expect(byName["Lamp"]!.color).toBe(KIND_COLOR["added"]);
    // Not in the change map → unchanged/neutral.
    expect(byName["Floor"]!.kind).toBe("unchanged");
    expect(byName["Floor"]!.color).toBe(NEUTRAL);
  });

  it("still matches when the diff's label reached us mangled", () => {
    // The file has "Cube.001"; the change map arrived with it sanitized.
    const nodes = buildSceneGraph([entity("Cube.001")], new Map<string, ChangeKind>([["Cube001", "removed"]]));
    expect(nodes[0]!.kind).toBe("removed");
    expect(nodes[0]!.color).toBe(KIND_COLOR["removed"]);
  });

  it("does not confuse similarly named nodes", () => {
    const changeMap = new Map<string, ChangeKind>([["Cube.002", "added"]]);
    const nodes = buildSceneGraph([entity("Cube.001"), entity("Cube.002")], changeMap);
    expect(nodes[0]!.kind).toBe("unchanged");
    expect(nodes[1]!.kind).toBe("added");
  });

  it("carries the transform through, defaulting when absent", () => {
    const [withT, withoutT] = buildSceneGraph([entity("A", [1, 2, 3]), entity("B")], new Map());
    expect(withT!.position).toEqual([1, 2, 3]);
    expect(withT!.scale).toEqual([1, 1, 1]);
    expect(withoutT!.position).toEqual([0, 0, 0]); // no transform → origin
  });

  it("resolves a depth per node, so a flat list renders as a tree", () => {
    // parseGltf emits parents before children, which is what makes one forward
    // pass enough — the structure region indents off this.
    const nodes = buildSceneGraph(
      [
        entity("Car"),
        entity("Axle", undefined, "Car"),
        entity("Wheel_FL", undefined, "Axle"),
        entity("Roof", undefined, "Car"),
      ],
      new Map(),
    );
    expect(nodes.map((n) => [n.name, n.depth])).toEqual([
      ["Car", 0],
      ["Axle", 1],
      ["Wheel_FL", 2],
      ["Roof", 1],
    ]);
    expect(nodes.map((n) => n.id)).toEqual(["Car", "Axle", "Wheel_FL", "Roof"]);
  });

  it("treats an unresolvable parent as a root rather than as depth -1", () => {
    const [orphan] = buildSceneGraph([entity("Stray", undefined, "Missing")], new Map());
    expect(orphan!.depth).toBe(0);
  });
});
