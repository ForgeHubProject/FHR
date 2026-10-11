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
      rows.push(rowOf(c, depth, children.length > 0));
      if (children.length > 0) walk(children, depth + 1);
    }
  };
  // A nil Go slice marshals to JSON null, so changes may be null over the wire.
  walk(diff.changes ?? [], 0);
  return rows;
}

export type DiffSummary = {
  added: number;
  removed: number;
  modified: number;
  /** Total change nodes, including nested children. */
  total: number;
  /**
   * Counts keyed by kind *as it appeared on the wire*, so a kind this SDK build
   * has never heard of (a handler that starts emitting "renamed" or "moved")
   * still gets counted and shown instead of vanishing from the summary bar.
   */
  byKind: Record<string, number>;
  /** Kinds present, known ones first in review order, then the rest sorted. */
  kinds: string[];
};

/** The kinds this SDK knows, in the order a summary bar should read. */
const KNOWN_KINDS: readonly string[] = ["added", "removed", "modified"];

export type KindCounts = {
  /** Count per kind as it appeared on the wire. */
  byKind: Record<string, number>;
  /** Kinds present: the known ones in review order, then the rest sorted. */
  kinds: string[];
};

/**
 * Tally any list of changes by kind. Kinds this SDK has never heard of are
 * counted too and sorted after the known ones, so a handler that starts emitting
 * "renamed" or "moved" shows up in a summary bar instead of vanishing from it.
 */
export function countKinds(changes: readonly { kind: string }[]): KindCounts {
  const byKind: Record<string, number> = {};
  for (const c of changes) {
    const kind = String(c.kind);
    byKind[kind] = (byKind[kind] ?? 0) + 1;
  }
  const extra = Object.keys(byKind)
    .filter((k) => !KNOWN_KINDS.includes(k))
    .sort();
  return { byKind, kinds: [...KNOWN_KINDS.filter((k) => byKind[k]), ...extra] };
}

/** Count changes by kind across the whole tree (children included). */
export function diffSummary(diff: StructuredDiff): DiffSummary {
  const all: DiffChange[] = [];
  const walk = (changes: DiffChange[]) => {
    for (const c of changes) {
      all.push(c);
      if (c.children?.length) walk(c.children);
    }
  };
  walk(diff.changes ?? []);
  const { byKind, kinds } = countKinds(all);
  return {
    added: byKind["added"] ?? 0,
    removed: byKind["removed"] ?? 0,
    modified: byKind["modified"] ?? 0,
    total: all.length,
    byKind,
    kinds,
  };
}

/**
 * One stop on the next/previous review path: a change plus the value rows that
 * belong to it.
 */
export type ReviewStop = {
  row: DiffRow;
  /** This stop's own descendant rows — the "before → after" lines under it. */
  details: DiffRow[];
};

/**
 * The changes `n`/`p` steps through: the *shallowest rows that carry values*.
 *
 * A structured diff nests collection wrappers above the things a reviewer thinks
 * of as changes ("nodes" → "Wheel_FL" → "translation"), and stepping through
 * every row would mean stepping through both the wrapper and each field of the
 * object it wraps. So a row is a stop when it has no children of its own, or
 * when at least one of its children is a leaf *carrying a value* — and nothing
 * below a stop is a stop. The "carrying a value" part is what separates an
 * object from a collection: a collection's leaf children are whole entities
 * (a node removed outright has no before/after of its own), while an object's
 * leaf children are its changed fields, which always have one.
 *
 * Renderers that know their own path scheme can pass an exact list instead;
 * the glTF renderer does.
 */
export function reviewStops(diff: StructuredDiff | undefined): ReviewStop[] {
  const stops: ReviewStop[] = [];
  if (!diff) return stops;

  const descend = (change: DiffChange, depth: number): DiffRow[] => {
    const rows: DiffRow[] = [];
    const walk = (c: DiffChange, d: number): void => {
      const children = c.children ?? [];
      rows.push(rowOf(c, d, children.length > 0));
      for (const child of children) walk(child, d + 1);
    };
    for (const child of change.children ?? []) walk(child, depth + 1);
    return rows;
  };

  const walk = (changes: DiffChange[], depth: number): void => {
    for (const change of changes) {
      const children = change.children ?? [];
      const isStop = children.length === 0 || children.some(isValueLeaf);
      if (isStop) {
        stops.push({ row: rowOf(change, depth, children.length > 0), details: descend(change, depth) });
        continue;
      }
      walk(children, depth + 1);
    }
  };

  walk(diff.changes ?? [], 0);
  return stops;
}

/** A childless change that carries a value: a changed *field*, not an entity. */
function isValueLeaf(c: DiffChange): boolean {
  return (c.children ?? []).length === 0 && (c.before !== undefined || c.after !== undefined);
}

function rowOf(c: DiffChange, depth: number, hasChildren: boolean): DiffRow {
  return {
    path: c.path,
    kind: c.kind,
    label: c.label ?? c.path,
    depth,
    before: c.before,
    after: c.after,
    hasChildren,
  };
}

/**
 * Index of the next (or previous) stop, wrapping at both ends. `current` may be
 * -1 for "nothing selected yet", which steps to the first (or last) stop.
 */
export function stepIndex(count: number, current: number, delta: number): number {
  if (count <= 0) return -1;
  if (current < 0) return delta >= 0 ? 0 : count - 1;
  return (((current + delta) % count) + count) % count;
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
