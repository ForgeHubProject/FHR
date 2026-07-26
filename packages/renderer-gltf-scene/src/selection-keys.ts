// Path ⇄ node-name translation for the selection round trip (#45).
//
// The two halves of the linkage speak different keys, on purpose:
//
//   the lite bundle and the host   fully-qualified change paths ("nodes/Wheel_FL")
//                                  — what a diff row and a host's selection state
//                                    are keyed on
//   the 3D scene                   glTF node names ("Wheel_FL") — what the
//                                  overlay's maps are keyed on
//
// `nodeChanges` is the one place that has seen the pairing the handler wrote, so
// the translation is built from it and neither half re-derives an escaping rule.
// Pure: no three.js, no DOM.

import type { NodeChange } from "./diff-map.js";
import { entityPath, nodeNameOfPath, pathOfNodeName } from "./change-path.js";

export type SelectionKeys = {
  /** The node name a selection path refers to, or null if this diff has none. */
  nameOf(path: string): string | null;
  /** The selection path for a node name (the handler's own escaped path). */
  pathOf(name: string): string;
  /** Re-key the lite bundle's path→headline map by node name for the callout. */
  headlinesByName(byPath: Record<string, string> | undefined): Record<string, string>;
};

export function emptyKeys(): SelectionKeys {
  return {
    nameOf: () => null,
    pathOf: (name) => pathOfNodeName(name),
    headlinesByName: () => ({}),
  };
}

export function selectionKeys(changes: NodeChange[]): SelectionKeys {
  const nameByPath = new Map<string, string>();
  const pathByName = new Map<string, string>();
  for (const change of changes) {
    nameByPath.set(change.path, change.name);
    if (!pathByName.has(change.name)) pathByName.set(change.name, change.path);
  }
  return {
    nameOf(path: string): string | null {
      // A field row ("nodes/Cube/translation") selects its object; a path this
      // diff never mentioned resolves through the scheme, so a host that keys on
      // node paths still lands somewhere sensible.
      return nameByPath.get(path) ?? nameByPath.get(entityPath(path)) ?? nodeNameOfPath(path);
    },
    pathOf(name: string): string {
      return pathByName.get(name) ?? pathOfNodeName(name);
    },
    headlinesByName(byPath: Record<string, string> | undefined): Record<string, string> {
      const out: Record<string, string> = {};
      if (!byPath) return out;
      for (const [path, line] of Object.entries(byPath)) {
        const name = nameByPath.get(path);
        if (name !== undefined) out[name] = line;
      }
      return out;
    },
  };
}
