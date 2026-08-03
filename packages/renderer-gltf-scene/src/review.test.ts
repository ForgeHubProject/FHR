// Selection keys and the review path, against the diff shape the handler emits
// for the car fixture used for the manual pass: seven named parts, of which the
// head version deletes Mirror_L, moves Wheel_FL and recolours Paint.

import { describe, it, expect } from "vitest";
import type { StructuredDiff } from "@fhr/types";
import { entityPath, nodeNameOfPath, pathOfNodeName, segmentCount, unescapeSegment, escapeSegment } from "./change-path.js";
import { entityStops, formatGltfChange, headline, headlines } from "./review.js";

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

  it("reads a rename as old → new, leaving the evidence to the panel", () => {
    const renameDiff: StructuredDiff = {
      version: "1.0",
      format: "gltf-scene",
      changes: [
        {
          path: "nodes",
          label: "nodes",
          kind: "modified",
          children: [
            {
              path: "nodes/Fender",
              label: "Fender",
              kind: "renamed",
              before: "Cube.003",
              after: "Fender (matched by content, ~91% similar)",
              children: [
                {
                  path: "nodes/Fender/translation",
                  label: "translation",
                  kind: "modified",
                  before: "[0.00 0.00 0.00]",
                  after: "[0.00 0.00 0.05]",
                },
              ],
            },
          ],
        },
      ],
    };
    const stops = entityStops(renameDiff);
    expect(stops).toHaveLength(1);
    // The callout carries one line: which object this was. The similarity figure
    // and the move stay in the panel below it.
    expect(headline(stops[0]!)).toBe("renamed Cube.003 → Fender");
  });

  it("reads a reparent off the parent detail row, never the evidence-bearing after (#42)", () => {
    const reparentDiff: StructuredDiff = {
      version: "1.0",
      format: "gltf-scene",
      changes: [
        {
          path: "nodes",
          label: "nodes",
          kind: "modified",
          children: [
            {
              path: "nodes/Mirror_L",
              label: "Mirror_L",
              kind: "reparented",
              before: "Body",
              // The node-level after carries the pairing evidence; parsing the
              // parent's name back out of it would be the string-scraping this
              // renderer refuses everywhere else.
              after: "Door_L (matched by structure)",
              children: [
                {
                  path: "nodes/Mirror_L/parent",
                  label: "parent",
                  kind: "modified",
                  before: "Body",
                  after: "Door_L",
                },
              ],
            },
          ],
        },
      ],
    };
    const stops = entityStops(reparentDiff);
    expect(stops).toHaveLength(1);
    expect(headline(stops[0]!)).toBe("reparented under Door_L");
  });

  it("still says reparented when the parent detail row is missing", () => {
    const bare: StructuredDiff = {
      version: "1.0",
      format: "gltf-scene",
      changes: [{ path: "nodes/Mirror_L", label: "Mirror_L", kind: "reparented", before: "Body", after: "Door_L" }],
    };
    const stops = entityStops(bare);
    expect(headline(stops[0]!)).toBe("reparented");
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

  // That map is keyed on the path, which is what makes it safe for a name to
  // appear twice: since #47 a node can be renamed *into* a name the previous
  // version's deleted node had. Both callouts have to survive — one path, one
  // change, per the handler's own guarantee.
  it("keeps both callouts when a rename takes over a deleted node's name", () => {
    const diff: StructuredDiff = {
      version: "1.0",
      format: "gltf-scene",
      changes: [
        {
          path: "nodes",
          label: "nodes",
          kind: "modified",
          children: [
            { path: "nodes/B", label: "B", kind: "renamed", before: "A", after: "B (matched by fhr_uid)" },
            {
              path: "nodes/B#1",
              label: "B",
              kind: "removed",
              before: "node",
              children: [{ path: "nodes/B#1/mesh", label: "mesh", kind: "removed", before: "BodyMesh" }],
            },
          ],
        },
      ],
    };
    expect(headlines(entityStops(diff))).toEqual({
      "nodes/B": "renamed A → B",
      "nodes/B#1": "removed",
    });
  });
});

// ── the geometry-detection seam (#50) ───────────────────────────────────────────
// Verbatim `gltf-scene` output for a sculpted car body plus a material
// reassignment, pinned as a fixture: this shape is what a mesh-level change looks
// like once the handler reports geometry, and it is the diff that exposed the
// asymmetric-pair formatting bug (see renderer-sdk/src/format.test.ts).
const SCULPT_DIFF: StructuredDiff = {
  "version": "1.0",
  "format": "gltf-scene",
  "changes": [
    {
      "path": "meshes",
      "kind": "modified",
      "label": "meshes",
      "children": [
        {
          "path": "meshes/BodyMesh",
          "kind": "modified",
          "label": "BodyMesh",
          "children": [
            {
              "path": "meshes/BodyMesh/primitives/0",
              "kind": "modified",
              "label": "primitive[0]",
              "children": [
                {
                  "path": "meshes/BodyMesh/primitives/0/geometry",
                  "kind": "modified",
                  "label": "geometry",
                  "children": [
                    {
                      "path": "meshes/BodyMesh/primitives/0/geometry/POSITION",
                      "kind": "modified",
                      "label": "POSITION",
                      "before": "count=24 type=VEC3 component=FLOAT hash=9138e59d77d851a5",
                      "after": "count=24 type=VEC3 component=FLOAT hash=f9cd47e93c1ec065"
                    }
                  ]
                },
                {
                  "path": "meshes/BodyMesh/primitives/0/bounds",
                  "kind": "modified",
                  "label": "bounds",
                  "before": "[4.00 1.80 1.00]",
                  "after": "[4.00 1.80 1.12] (+0.12 Z)"
                },
                {
                  "path": "meshes/BodyMesh/primitives/0/centroid",
                  "kind": "modified",
                  "label": "centroid",
                  "before": "[0.00 0.00 0.00]",
                  "after": "[0.00 0.00 0.06] (moved 0.060)"
                }
              ]
            }
          ]
        },
        {
          "path": "meshes/WheelMesh",
          "kind": "modified",
          "label": "WheelMesh",
          "children": [
            {
              "path": "meshes/WheelMesh/primitives/0",
              "kind": "modified",
              "label": "primitive[0]",
              "children": [
                {
                  "path": "meshes/WheelMesh/primitives/0/material",
                  "kind": "modified",
                  "label": "material",
                  "before": "Rubber",
                  "after": "Glass"
                }
              ]
            }
          ]
        }
      ]
    }
  ]
} as StructuredDiff;

describe("mesh-level changes (the #50 shape)", () => {
  const stops = entityStops(SCULPT_DIFF);
  const stopFor = (path: string) => stops.find((s) => s.row.path === path)!;

  it("stops on each changed mesh, not on the primitives below it", () => {
    expect(stops.map((s) => s.row.path)).toEqual(["meshes/BodyMesh", "meshes/WheelMesh"]);
  });

  it("gathers the whole subtree as details, headers included", () => {
    expect(stopFor("meshes/BodyMesh").details.map((d) => d.label)).toEqual([
      "primitive[0]",
      "geometry",
      "POSITION",
      "bounds",
      "centroid",
    ]);
  });

  // Five detail rows, of which three carry a value and one is the edit: a
  // reviewer calls that one geometry change, so the headline reports the
  // magnitude rather than counting rows.
  it("headlines a sculpt with the size it gained, not a row count", () => {
    expect(headline(stopFor("meshes/BodyMesh"))).toBe("grew 120 mm");
  });

  it("headlines a material reassignment as the field that changed", () => {
    expect(headline(stopFor("meshes/WheelMesh"))).toBe("material changed");
  });

  it("says a shrink shrank", () => {
    const shrunk = entityStops({
      version: "1.0",
      format: "gltf-scene",
      changes: [
        {
          path: "meshes/Hood",
          label: "Hood",
          kind: "modified",
          children: [
            { path: "meshes/Hood/bounds", label: "bounds", kind: "modified", before: "[4.00 1.80 1.12]", after: "[4.00 1.80 1.00]" },
          ],
        },
      ],
    });
    expect(headline(shrunk[0]!)).toBe("shrank 120 mm");
  });

  it("falls back to naming the edit when only vertex data changed", () => {
    const vertexOnly = entityStops({
      version: "1.0",
      format: "gltf-scene",
      changes: [
        {
          path: "meshes/Hood",
          label: "Hood",
          kind: "modified",
          children: [
            {
              path: "meshes/Hood/primitives/0",
              label: "primitive[0]",
              kind: "modified",
              children: [
                { path: "meshes/Hood/primitives/0/geometry/POSITION", label: "POSITION", kind: "modified", before: "hash=a", after: "hash=b" },
              ],
            },
          ],
        },
      ],
    });
    expect(headline(vertexOnly[0]!)).toBe("geometry edited");
  });

  it("formats both halves of an annotated metric in one notation", () => {
    const bounds = stopFor("meshes/BodyMesh").details.find((d) => d.label === "bounds")!;
    const f = formatGltfChange(bounds);
    expect(f.before).toBe("(4, 1.8, 1)");
    expect(f.after).toBe("(4, 1.8, 1.12) (+0.12 Z)");
    expect(f.deltaCell).toBe("Δ(0, 0, 0.12) = 120 mm");
  });
});
