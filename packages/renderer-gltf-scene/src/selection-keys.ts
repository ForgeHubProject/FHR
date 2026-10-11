// Selection-path resolution for the round trip in #45.
//
// Both halves speak one key now — the handler's fully-qualified change path. The
// lite bundle and the host key a diff row and the host's selection state on it,
// and the 3D overlay keys its boxes, its painted objects and its pick map on it
// too (model-overlay.ts). That is not cosmetic. The translation this module used
// to do — path → glTF node *name*, and the callout's headline map re-keyed to
// match — is exactly what #47 broke: a name stopped identifying a change the
// moment one revision could delete "Wheel" while renaming something else *to*
// "Wheel". Two changes, two objects, one name, and whichever was written second
// won: one row became unreachable and both were labelled with the other's news.
//
// What is left here is the one thing paths still need reconciling on: a selection
// may address a *field* of a change ("nodes/Cube/translation") while the scene
// only ever paints whole objects, so a field path resolves to its object. A path
// this diff never mentioned resolves to nothing, which is the honest answer —
// there is no such change to select.
//
// Pure: no three.js, no DOM.

import { entityPath } from "./change-path.js";

export type SelectionKeys = {
  /** The change path a selection path selects, or null when this diff has none. */
  changePathOf(path: string): string | null;
};

export function emptyKeys(): SelectionKeys {
  return { changePathOf: () => null };
}

/** Resolution over the changes the overlay was built from (nodes, meshes, materials). */
export function selectionKeys(changes: readonly { path: string }[]): SelectionKeys {
  const known = new Set<string>();
  for (const change of changes) known.add(change.path);
  return {
    changePathOf(path: string): string | null {
      if (known.has(path)) return path;
      const entity = entityPath(path);
      return entity !== path && known.has(entity) ? entity : null;
    },
  };
}
