import type { StructuredDiff, DiffChange, ChangeKind } from "@fhr/types";

function slugify(name: string): string {
  return name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "") || "node";
}

/**
 * A node-level change path: exactly one segment below the "nodes" collection.
 * Handler paths are "/"-separated and fully qualified, so a property change
 * reads "nodes/Cube/translation" — matching on the prefix alone would colour the
 * scene by property names too.
 */
const NODE_PATH = /^nodes\/[^/]+$/;

/** Reverse the handler's segment escaping ("%" → %25, "/" → %2F); "/" first. */
function unescapeSegment(segment: string): string {
  return segment.replace(/%2F/gi, "/").replace(/%25/g, "%");
}

/**
 * Map a gltf-scene StructuredDiff to a change kind per scene node, keyed by the
 * node's slugified name (matching the parser's entityId). A display heuristic:
 * node-level changes are those whose path is a direct child of the "nodes"
 * collection (e.g. "nodes/Cube"); their field children
 * ("nodes/Cube/translation", …) are ignored for colouring. Nested-node name
 * collisions remain a known limitation — issue #42 replaces name keying with
 * stable identity.
 */
export function diffChangeTypes(diff: StructuredDiff | undefined): Map<string, ChangeKind> {
  const acc = new Map<string, ChangeKind>();
  if (!diff) return acc;
  const walk = (changes: DiffChange[]): void => {
    for (const c of changes) {
      if (NODE_PATH.test(c.path)) {
        // Label is the raw display name; the path is the escaped machine key.
        const name = c.label ?? unescapeSegment(c.path.slice("nodes/".length));
        acc.set(slugify(name), c.kind);
      }
      if (c.children?.length) walk(c.children);
    }
  };
  walk(diff.changes ?? []);
  return acc;
}
