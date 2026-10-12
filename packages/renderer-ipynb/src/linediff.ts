// A line diff for one cell's text: what a reader of a notebook diff wants to
// see inside each changed cell, GitHub style.

export type DiffLine =
  | { kind: "same"; oldNo: number; newNo: number; text: string }
  | { kind: "del"; oldNo: number; text: string }
  | { kind: "add"; newNo: number; text: string }
  | { kind: "fold"; count: number };

/** Lines of a cell's text; a trailing newline does not make an empty last line. */
export function splitLines(text: string): string[] {
  if (text === "") return [];
  const lines = text.split("\n");
  if (lines[lines.length - 1] === "") lines.pop();
  return lines;
}

/** Past this many line pairs the table is skipped: every old line removed, every new line added. */
const MAX_CELLS = 4_000_000;

/**
 * The line diff of `before` → `after`: a longest common subsequence of lines,
 * with removals listed before additions within each changed run.
 */
export function lineDiff(before: string, after: string): DiffLine[] {
  const a = splitLines(before);
  const b = splitLines(after);
  const out: DiffLine[] = [];
  const n = a.length;
  const m = b.length;
  if (n * m > MAX_CELLS) {
    a.forEach((text, i) => out.push({ kind: "del", oldNo: i + 1, text }));
    b.forEach((text, j) => out.push({ kind: "add", newNo: j + 1, text }));
    return out;
  }
  // lcs[i][j] = LCS length of a[i:] and b[j:], one flat row-major array.
  const w = m + 1;
  const lcs = new Uint32Array((n + 1) * w);
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      lcs[i * w + j] = a[i] === b[j] ? lcs[(i + 1) * w + j + 1]! + 1 : Math.max(lcs[(i + 1) * w + j]!, lcs[i * w + j + 1]!);
    }
  }
  let i = 0;
  let j = 0;
  let dels: DiffLine[] = [];
  let adds: DiffLine[] = [];
  const flush = () => {
    out.push(...dels, ...adds);
    dels = [];
    adds = [];
  };
  while (i < n || j < m) {
    if (i < n && j < m && a[i] === b[j]) {
      flush();
      out.push({ kind: "same", oldNo: i + 1, newNo: j + 1, text: a[i]! });
      i++;
      j++;
    } else if (j >= m || (i < n && lcs[(i + 1) * w + j]! >= lcs[i * w + j + 1]!)) {
      dels.push({ kind: "del", oldNo: i + 1, text: a[i]! });
      i++;
    } else {
      adds.push({ kind: "add", newNo: j + 1, text: b[j]! });
      j++;
    }
  }
  flush();
  return out;
}

/**
 * Fold runs of unchanged lines longer than `2 * context + 1`, keeping
 * `context` lines on each side of every change (none at the ends of the cell
 * beyond that), so a one-line edit in a long cell reads as a short hunk.
 */
export function foldUnchanged(lines: DiffLine[], context = 3): DiffLine[] {
  const out: DiffLine[] = [];
  let k = 0;
  while (k < lines.length) {
    if (lines[k]!.kind !== "same") {
      out.push(lines[k]!);
      k++;
      continue;
    }
    let end = k;
    while (end < lines.length && lines[end]!.kind === "same") end++;
    const run = lines.slice(k, end);
    const keepHead = k === 0 ? 0 : context;
    const keepTail = end === lines.length ? 0 : context;
    if (run.length > keepHead + keepTail + 1) {
      out.push(...run.slice(0, keepHead));
      out.push({ kind: "fold", count: run.length - keepHead - keepTail });
      out.push(...run.slice(run.length - keepTail));
    } else {
      out.push(...run);
    }
    k = end;
  }
  return out;
}
