import type { StructuredDiff, DiffChange, ChangeKind } from "@fhr/types";

function slugify(name: string): string {
  return name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "") || "node";
}

/**
 * Map a gltf-scene StructuredDiff to a change kind per scene node, keyed by the
 * node's slugified name (matching the parser's entityId). A display heuristic:
 * node-level changes are those whose path sits under the "nodes" collection
 * (e.g. "nodes.Cube"); their field children ("translation", …) are ignored for
 * colouring. Nested-node name collisions are a known limitation to refine.
 */
export function diffChangeTypes(diff: StructuredDiff | undefined): Map<string, ChangeKind> {
  const acc = new Map<string, ChangeKind>();
  if (!diff) return acc;
  const walk = (changes: DiffChange[]): void => {
    for (const c of changes) {
      if (c.path.startsWith("nodes.")) {
        const name = c.label ?? c.path.slice(c.path.lastIndexOf(".") + 1);
        acc.set(slugify(name), c.kind);
      }
      if (c.children?.length) walk(c.children);
    }
  };
  walk(diff.changes);
  return acc;
}
