// What the 3D scene decides, minus the pixels.
//
// `mountModelScene` needs WebGL, so nothing here mounts one. What is testable —
// and what the reviewer actually reads — is the caption the viewport puts over a
// node when the structure tree frames one: the one place this view can state
// something the picture beside it contradicts.

import { describe, it, expect } from "vitest";
import { framingForNode, NOT_IN_CHANGE_LIST } from "./scene-3d.js";

const facts = (options: {
  changes?: string[];
  reaches?: Record<string, string>;
  sceneRootName?: string | null;
}) => ({
  isChangeName: (name: string): boolean => (options.changes ?? []).includes(name),
  changeOfNode: (name: string): string | null => options.reaches?.[name] ?? null,
  sceneRootName: options.sceneRootName ?? null,
});

describe("framingForNode — what clicking a structure-tree row means", () => {
  it("treats a node the diff names as the queue does", () => {
    expect(framingForNode("Mirror_L", facts({ changes: ["Mirror_L"] }))).toEqual({
      via: "change",
      change: "Mirror_L",
    });
  });

  // The regression: `boxByChangeName` is keyed on change names, and #51's mesh
  // and material changes are named for the mesh or the material. Asking that map
  // "is this node in the change list" answered no for geometry the overlay had
  // just painted orange, so the callout captioned it "not in the change list" —
  // over a part that is in the change queue and highlighted on screen.
  it("names the change that reaches a node through its mesh or material", () => {
    const reached = facts({ changes: ["BodyMesh"], reaches: { Body: "BodyMesh" } });
    expect(framingForNode("Body", reached)).toEqual({ via: "entity", change: "BodyMesh" });
  });

  it("never calls a painted node unlisted", () => {
    const reached = facts({ changes: ["WheelMesh"], reaches: { Wheel_FL: "WheelMesh", Wheel_FR: "WheelMesh" } });
    for (const wheel of ["Wheel_FL", "Wheel_FR"]) {
      expect(framingForNode(wheel, reached).via).not.toBe("none");
    }
  });

  it("still says so for a node nothing reaches", () => {
    expect(framingForNode("Chassis", facts({ changes: ["BodyMesh"], reaches: { Body: "BodyMesh" } }))).toEqual({
      via: "none",
    });
    expect(NOT_IN_CHANGE_LIST).toBe("not in the change list");
  });

  it("keeps the root row meaning the whole model", () => {
    // The tree's first and most prominent row is the glTF scene, not a node. It
    // must not borrow the unchanged headline: "not in the change list" over a
    // model with changes reads as a claim about the file.
    expect(framingForNode("Car", facts({ sceneRootName: "Car" }))).toEqual({ via: "scene" });
  });

  it("lets a change on the root row win over the scene reading", () => {
    expect(framingForNode("Car", facts({ changes: ["Car"], sceneRootName: "Car" }))).toEqual({
      via: "change",
      change: "Car",
    });
  });
});
