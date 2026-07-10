import type { StructuredDiff, DiffChange, ChangeKind } from "@fhr/types";

/** A single change flattened out of the (possibly nested) StructuredDiff tree. */
export type DiffRow = {
  path: string;
  kind: ChangeKind;
  label: string;
  /** Nesting depth; 0 for top-level changes. */
  depth: number;
  before?: unknown;
  after?: unknown;
  hasChildren: boolean;
};

/** Depth-first flatten of a StructuredDiff into display rows (parents precede children). */
export function flattenDiff(diff: StructuredDiff): DiffRow[] {
  const rows: DiffRow[] = [];
  const walk = (changes: DiffChange[], depth: number) => {
    for (const c of changes) {
      const children = c.children ?? [];
      rows.push({
        path: c.path,
        kind: c.kind,
        label: c.label ?? c.path,
        depth,
        before: c.before,
        after: c.after,
        hasChildren: children.length > 0,
      });
      if (children.length > 0) walk(children, depth + 1);
    }
  };
  walk(diff.changes, 0);
  return rows;
}

export type DiffSummary = {
  added: number;
  removed: number;
  modified: number;
  /** Total change nodes, including nested children. */
  total: number;
};

/** Count changes by kind across the whole tree (children included). */
export function diffSummary(diff: StructuredDiff): DiffSummary {
  const s: DiffSummary = { added: 0, removed: 0, modified: 0, total: 0 };
  const walk = (changes: DiffChange[]) => {
    for (const c of changes) {
      s[c.kind] += 1;
      s.total += 1;
      if (c.children?.length) walk(c.children);
    }
  };
  walk(diff.changes);
  return s;
}

/**
 * Compact one-line rendering of a diff value for display. Numbers are trimmed
 * to 3 decimals to suppress float noise (e.g. quaternion→euler drift); arrays
 * and objects are rendered shallowly.
 */
export function formatValue(v: unknown): string {
  if (v === undefined) return "—";
  if (v === null) return "null";
  if (typeof v === "number") {
    if (Number.isInteger(v)) return String(v);
    return String(Math.round(v * 1000) / 1000);
  }
  if (typeof v === "string") return v;
  if (typeof v === "boolean") return v ? "true" : "false";
  if (Array.isArray(v)) return "[" + v.map(formatValue).join(", ") + "]";
  if (typeof v === "object") {
    try {
      return JSON.stringify(v);
    } catch {
      return String(v);
    }
  }
  return String(v);
}
