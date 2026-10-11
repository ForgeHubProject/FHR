import { describe, it, expect } from "vitest";
import type { ReviewStop } from "@fhr/renderer-sdk";
import { buildQueue, queuePosition, type QueueEntry } from "./queue.js";
import { formatGltfChange, headline } from "./review.js";

const stop = (path: string, label: string, kind: "added" | "removed" | "modified", details: unknown[] = []): ReviewStop =>
  ({
    row: { path, label, kind, depth: 1, hasChildren: details.length > 0 },
    details,
  }) as unknown as ReviewStop;

describe("buildQueue", () => {
  it("formats a change's fields once, on the side that owns the rules", () => {
    const entries = buildQueue(
      [
        stop("nodes/Wheel_FL", "Wheel_FL", "modified", [
          {
            path: "nodes/Wheel_FL/translation",
            label: "translation",
            kind: "modified",
            depth: 2,
            before: "[0.90 -1.40 0.35]",
            after: "[0.90 -1.40 0.40]",
          },
          // A header row above other rows carries no value of its own, and a
          // panel line reading "→" would be furniture.
          { path: "nodes/Wheel_FL/geometry", label: "geometry", kind: "modified", depth: 2 },
        ]),
      ],
      formatGltfChange,
      headline,
    );

    expect(entries).toHaveLength(1);
    const entry = entries[0]!;
    expect(entry.path).toBe("nodes/Wheel_FL");
    expect(entry.label).toBe("Wheel_FL");
    expect(entry.kind).toBe("modified");
    expect(entry.headline).toBe("moved 50 mm");
    expect(entry.details.map((d) => d.label)).toEqual(["translation"]);
    // Formatted by the SDK's rules, not re-derived: tuple values arrive as
    // vectors, with the delta the reviewer judges the change by beside them.
    expect(entry.details[0]!.before).toBe("(0.9, -1.4, 0.35)");
    expect(entry.details[0]!.after).toBe("(0.9, -1.4, 0.4)");
    expect(entry.details[0]!.delta).toContain("50 mm");
  });

  it("carries a removal with nothing under it", () => {
    const [entry] = buildQueue([stop("nodes/Mirror_L", "Mirror_L", "removed")], formatGltfChange, headline);
    expect(entry!.headline).toBe("removed");
    expect(entry!.details).toEqual([]);
  });
});

describe("queuePosition", () => {
  const entries: QueueEntry[] = ["a", "b", "c"].map((path) => ({
    path,
    label: path,
    kind: "modified",
    headline: "changed",
    details: [],
  }));

  it("reports the size of the job until the reviewer is standing somewhere", () => {
    // "0 / 3" would claim a position in the worklist that nobody has taken yet.
    expect(queuePosition(entries, null)).toEqual({ index: -1, total: 3, label: "3 changes" });
  });

  it("reports where the reviewer is", () => {
    expect(queuePosition(entries, "b")).toEqual({ index: 1, total: 3, label: "2 / 3" });
  });

  it("treats a selection that isn't in the queue as no position", () => {
    // A field row, or a host key from a diff this queue was not built from.
    expect(queuePosition(entries, "nodes/Wheel_FL/translation").index).toBe(-1);
  });

  it("counts one change in the singular", () => {
    expect(queuePosition([entries[0]!], null).label).toBe("1 change");
  });

  it("handles an empty queue", () => {
    expect(queuePosition([], null).label).toBe("0 changes");
  });
});
