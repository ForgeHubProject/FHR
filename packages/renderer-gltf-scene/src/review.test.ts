// Selection keys and the review path, against the diff shape the handler emits
// for the car fixture used for the manual pass: seven named parts, of which the
// head version deletes Mirror_L, moves Wheel_FL and recolours Paint.

import { describe, it, expect } from "vitest";
import type { StructuredDiff } from "@fhr/types";
import { entityPath, nodeNameOfPath, pathOfNodeName, segmentCount, unescapeSegment, escapeSegment } from "./change-path.js";
import { entityStops, headline, headlines } from "./review.js";

const carDiff: StructuredDiff = {
  version: "1.0",
  format: "gltf-scene",
  changes: [
    {
      path: "nodes",
      label: "nodes",
      kind: "modified",
      children: [
        {
          path: "nodes/Wheel_FL",
          label: "Wheel_FL",
          kind: "modified",
          children: [
            {
              path: "nodes/Wheel_FL/translation",
              label: "translation",
              kind: "modified",
              before: "[0.90 -1.40 0.35]",
              after: "[0.90 -1.40 0.40]",
            },
          ],
        },
        { path: "nodes/Mirror_L", label: "Mirror_L", kind: "removed" },
      ],
    },
    {
      path: "materials",
      label: "materials",
      kind: "modified",
      children: [
        {
          path: "materials/Paint",
          label: "Paint",
          kind: "modified",
          children: [
            {
              path: "materials/Paint/baseColorFactor",
              label: "baseColorFactor",
              kind: "modified",
              before: "[0.80 0.10 0.10 1.00]",
              after: "[0.10 0.20 0.70 1.00]",
            },
          ],
        },
      ],
    },
  ],
};

describe("change-path", () => {
  it("counts segments so a path's shape can be read", () => {
    expect(segmentCount("nodes")).toBe(1);
    expect(segmentCount("nodes/Wheel_FL")).toBe(2);
    expect(segmentCount("nodes/Wheel_FL/translation")).toBe(3);
  });

  it("finds the object a field change belongs to", () => {
    expect(entityPath("nodes/Wheel_FL/translation")).toBe("nodes/Wheel_FL");
    expect(entityPath("nodes/Wheel_FL")).toBe("nodes/Wheel_FL");
    expect(entityPath("nodes")).toBe("nodes");
    // Deeper paths (an animation channel) still resolve to their object.
    expect(entityPath("animations/Walk/channels/channel[0]/target")).toBe("animations/Walk");
  });

  it("reads a node name out of a path, whatever it is escaped as", () => {
    expect(nodeNameOfPath("nodes/Cube.001")).toBe("Cube.001");
    expect(nodeNameOfPath("nodes/Cube.001/translation")).toBe("Cube.001");
    expect(nodeNameOfPath("nodes/rig%2Fhand")).toBe("rig/hand");
    expect(nodeNameOfPath("nodes/50%25%2Fscale")).toBe("50%/scale");
    expect(nodeNameOfPath("nodes/node[2]")).toBe("node[2]");
  });

  it("has no node name for changes that aren't about nodes", () => {
    expect(nodeNameOfPath("materials/Paint")).toBeNull();
    expect(nodeNameOfPath("meshes/Tri/primitives")).toBeNull();
    expect(nodeNameOfPath("nodes")).toBeNull();
  });

  it("round-trips a name through a path", () => {
    for (const name of ["Cube.001", "rig/hand", "50%", "node[2]", "a%2Fb"]) {
      expect(nodeNameOfPath(pathOfNodeName(name)), name).toBe(name);
    }
  });

  it("escapes and unescapes only what the handler escapes", () => {
    expect(escapeSegment("rig/hand")).toBe("rig%2Fhand");
    expect(escapeSegment("50%")).toBe("50%25");
    expect(unescapeSegment("rig%2fhand")).toBe("rig/hand");
  });
});

describe("entityStops", () => {
  it("stops on the changed objects, in diff order", () => {
    expect(entityStops(carDiff).map((s) => s.row.path)).toEqual([
      "nodes/Wheel_FL",
      "nodes/Mirror_L",
      "materials/Paint",
    ]);
  });

  it("keeps a removed node with no fields of its own as a stop", () => {
    const stop = entityStops(carDiff).find((s) => s.row.path === "nodes/Mirror_L");
    expect(stop?.row.kind).toBe("removed");
    expect(stop?.details).toEqual([]);
  });

  it("gives each stop its own field rows as details", () => {
    const stops = entityStops(carDiff);
    expect(stops[0]?.details.map((d) => d.path)).toEqual(["nodes/Wheel_FL/translation"]);
    expect(stops[2]?.details.map((d) => d.label)).toEqual(["baseColorFactor"]);
  });

  it("has nothing to review without a diff", () => {
    expect(entityStops(undefined)).toEqual([]);
  });

  it("falls back to the SDK heuristic for a diff with no object-shaped paths", () => {
    // Nothing here is one or two segments deep, so the path-shape rule finds no
    // stop at all and the general heuristic takes over rather than leaving the
    // review path empty.
    const odd: StructuredDiff = {
      version: "1.0",
      format: "gltf-scene",
      changes: [
        {
          path: "root",
          kind: "modified",
          label: "root",
          children: [{ path: "root/deep/leaf", kind: "modified", label: "leaf", before: "1", after: "2" }],
        },
      ],
    };
    expect(entityStops(odd).map((s) => s.row.path)).toEqual(["root"]);
  });

  it("keeps a top-level change with no children reachable", () => {
    const flat: StructuredDiff = {
      version: "1.0",
      format: "gltf-scene",
      changes: [{ path: "file", kind: "modified", label: "file", before: "a", after: "b" }],
    };
    expect(entityStops(flat).map((s) => s.row.path)).toEqual(["file"]);
  });
});

describe("headline (the one viewport callout's text)", () => {
  const of = (path: string): string => headline(entityStops(carDiff).find((s) => s.row.path === path)!);

  it("names the movement and its magnitude", () => {
    expect(of("nodes/Wheel_FL")).toBe("moved 50 mm");
  });

  it("says removed for a deletion, without inventing a number", () => {
    expect(of("nodes/Mirror_L")).toBe("removed");
  });

  it("says recoloured for a colour change rather than reading out floats", () => {
    expect(of("materials/Paint")).toBe("recoloured");
  });

  it("prefers the transform when several things changed at once", () => {
    const stops = entityStops({
      version: "1.0",
      format: "gltf-scene",
      changes: [
        {
          path: "nodes/Hood",
          label: "Hood",
          kind: "modified",
          children: [
            { path: "nodes/Hood/mesh", label: "mesh", kind: "modified", before: "mesh[3]", after: "mesh[5]" },
            { path: "nodes/Hood/rotation", label: "rotation", kind: "modified", before: "(0.00° 0.00° 0.00°)", after: "(0.00° 12.00° 0.00°)" },
          ],
        },
      ],
    });
    expect(headline(stops[0]!)).toBe("rotated 12°");
  });

  it("does not headline pure index churn as if it were the change", () => {
    const stops = entityStops({
      version: "1.0",
      format: "gltf-scene",
      changes: [
        {
          path: "nodes/Hood",
          label: "Hood",
          kind: "modified",
          children: [
            { path: "nodes/Hood/mesh", label: "mesh", kind: "modified", before: "mesh[3]", after: "mesh[5]" },
          ],
        },
      ],
    });
    expect(headline(stops[0]!)).toBe("changed");
  });

  it("counts when several unmeasurable things changed", () => {
    const stops = entityStops({
      version: "1.0",
      format: "gltf-scene",
      changes: [
        {
          path: "materials/Trim",
          label: "Trim",
          kind: "modified",
          children: [
            { path: "materials/Trim/alphaMode", label: "alphaMode", kind: "modified", before: "OPAQUE", after: "BLEND" },
            { path: "materials/Trim/doubleSided", label: "doubleSided", kind: "modified", before: "false", after: "true" },
          ],
        },
      ],
    });
    expect(headline(stops[0]!)).toBe("2 changes");
  });

  it("builds a path → headline map for the viewport", () => {
    expect(headlines(entityStops(carDiff))).toEqual({
      "nodes/Wheel_FL": "moved 50 mm",
      "nodes/Mirror_L": "removed",
      "materials/Paint": "recoloured",
    });
  });
});
