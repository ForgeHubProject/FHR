import { describe, it, expect } from "vitest";
import type { ChangeKind } from "@fhr/types";
import { buildSceneGraph, KIND_COLOR, NEUTRAL } from "./scene-graph.js";
import type { Entity } from "./gltf-parse.js";

function entity(name: string, position?: [number, number, number]): Entity {
  return {
    id: name,
    entityId: name,
    parentEntityId: null,
    kind: "part",
    name,
    path: name,
    transform: position ? { position, rotationEulerDeg: [0, 0, 0], scale: [1, 1, 1] } : null,
  };
}

describe("buildSceneGraph", () => {
  it("colours nodes by their change kind, keyed by slugified name", () => {
    const changeMap = new Map<string, ChangeKind>([
      ["cube", "modified"],
      ["lamp", "added"],
    ]);
    const nodes = buildSceneGraph([entity("Cube", [5, 0, 0]), entity("Lamp"), entity("Floor")], changeMap);

    const byName = Object.fromEntries(nodes.map((n) => [n.name, n]));
    expect(byName["Cube"].kind).toBe("modified");
    expect(byName["Cube"].color).toBe(KIND_COLOR["modified"]);
    expect(byName["Lamp"].color).toBe(KIND_COLOR["added"]);
    // Not in the change map → unchanged/neutral.
    expect(byName["Floor"].kind).toBe("unchanged");
    expect(byName["Floor"].color).toBe(NEUTRAL);
  });

  it("carries the transform through, defaulting when absent", () => {
    const [withT, withoutT] = buildSceneGraph([entity("A", [1, 2, 3]), entity("B")], new Map());
    expect(withT.position).toEqual([1, 2, 3]);
    expect(withT.scale).toEqual([1, 1, 1]);
    expect(withoutT.position).toEqual([0, 0, 0]); // no transform → origin
  });
});
