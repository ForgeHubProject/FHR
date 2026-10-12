import { describe, it, expect } from "vitest";
import type { MountProps, StructuredDiff } from "@fhr/types";
import bundle, { renderNotebookDiff } from "./index.js";
import { foldUnchanged, lineDiff, splitLines } from "./linediff.js";

describe("ipynb renderer bundle", () => {
  it("declares the ipynb handler and its extension via the mount() contract", () => {
    expect(bundle.handlerId).toBe("ipynb");
    expect(bundle.extensions).toContain(".ipynb");
    expect(bundle.fhrVersion).toBe(1);
    expect(typeof bundle.mount).toBe("function");
  });

  it("draws diffs only, so a host keeps its own viewer for a single file", () => {
    expect(bundle.modes).toEqual(["diff"]);
  });
});

describe("lineDiff", () => {
  const show = (before: string, after: string) =>
    lineDiff(before, after).map((l) => (l.kind === "fold" ? `…${l.count}` : `${{ same: " ", add: "+", del: "-" }[l.kind]}${l.text}`));

  it("keeps common lines and marks the edit", () => {
    expect(show("x = 1\nprint(x)\n", "x = 2\nprint(x)\n")).toEqual(["-x = 1", "+x = 2", " print(x)"]);
  });

  it("lists removals before additions in a changed run, with both line numbers", () => {
    const d = lineDiff("a\nb\nc", "a\nB\nC\nc");
    expect(d).toEqual([
      { kind: "same", oldNo: 1, newNo: 1, text: "a" },
      { kind: "del", oldNo: 2, text: "b" },
      { kind: "add", newNo: 2, text: "B" },
      { kind: "add", newNo: 3, text: "C" },
      { kind: "same", oldNo: 3, newNo: 4, text: "c" },
    ]);
  });

  it("reads an added or removed cell as all additions or all removals", () => {
    expect(show("", "import os\nos.getcwd()")).toEqual(["+import os", "+os.getcwd()"]);
    expect(show("gone()", "")).toEqual(["-gone()"]);
  });

  it("does not treat a trailing newline as an extra line", () => {
    expect(splitLines("a\nb\n")).toEqual(["a", "b"]);
    expect(splitLines("")).toEqual([]);
  });

  it("folds long unchanged runs, keeping three lines of context around a change", () => {
    const before = Array.from({ length: 20 }, (_, i) => `line ${i + 1}`).join("\n");
    const after = before.replace("line 10", "line ten");
    const folded = foldUnchanged(lineDiff(before, after)).map((l) => (l.kind === "fold" ? `…${l.count}` : l.kind === "same" ? l.text : `${l.kind}:${l.text}`));
    expect(folded).toEqual(["…6", "line 7", "line 8", "line 9", "del:line 10", "add:line ten", "line 11", "line 12", "line 13", "…7"]);
  });
});

// ── rendering, against just enough DOM ─────────────────────────────────────────
type El = {
  tag: string; className: string; textContent: string; attrs: Record<string, string>; children: El[];
  listeners: Record<string, (() => void)[]>;
  setAttribute(k: string, v: string): void; appendChild(c: El): El; append(...c: El[]): void;
  addEventListener(t: string, f: () => void): void; removeEventListener(t: string, f: () => void): void;
};
function makeDoc() {
  const doc = {
    createElement(tag: string): El {
      const el: El = {
        tag, className: "", textContent: "", attrs: {}, children: [], listeners: {},
        setAttribute(k, v) { el.attrs[k] = v; },
        appendChild(c) { el.children.push(c); return c; },
        append(...c) { el.children.push(...c); },
        addEventListener(t, f) { (el.listeners[t] ??= []).push(f); },
        removeEventListener(t, f) { el.listeners[t] = (el.listeners[t] ?? []).filter((g) => g !== f); },
      };
      return el;
    },
  };
  return Object.assign(doc.createElement("div"), { ownerDocument: doc });
}
const all = (e: El): El[] => [e, ...e.children.flatMap(all)];
const byClass = (e: El, cls: string) => all(e).filter((x) => x.className.split(" ").includes(cls));
const text = (e: El): string => e.textContent + e.children.map(text).join("");

const diff: StructuredDiff = {
  version: "1.0",
  format: "ipynb",
  changes: [
    { path: "metadata/kernel", kind: "modified", label: "kernel", before: "Python 3", after: "Python 3.12" },
    { path: "cells/setup", kind: "added", label: "code cell 1", children: [{ path: "source", kind: "added", label: "source", after: "import pandas as pd" }] },
    {
      path: "cells/widths", kind: "modified", label: "code cell 3",
      children: [
        { path: "source", kind: "modified", label: "source", before: "w = [1, 2]\nprint(sum(w))", after: "w = [1, 2, 3]\nprint(sum(w))" },
        { path: "outputs", kind: "modified", label: "outputs", before: "3\n", after: "6\n" },
      ],
    },
    { path: "cells/plot", kind: "modified", label: "code cell 4", children: [{ path: "position", kind: "modified", label: "position", before: 2, after: 4 }] },
    { path: "cells/3", kind: "removed", label: "markdown cell 3", children: [{ path: "source", kind: "removed", label: "source", before: "Old notes" }] },
  ],
};

function mount(props: Partial<MountProps> = {}) {
  const root = makeDoc();
  const cleanup = renderNotebookDiff(root as unknown as HTMLElement, { mode: "diff", diff, ...props });
  return { root, cleanup };
}

describe("notebook diff rendering", () => {
  it("summarises the cells by kind", () => {
    const { root } = mount();
    expect(text(byClass(root, "fhr-nb__summary")[0]!)).toBe("4 cells: 1 added · 1 removed · 1 changed · 1 moved");
  });

  it("draws one card per change, in the handler's order, with the right badge", () => {
    const { root } = mount();
    const cards = byClass(root, "fhr-nb__cell");
    expect(cards.map((c) => text(byClass(c, "fhr-nb__badge")[0]!))).toEqual(["changed", "added", "modified", "moved", "removed"]);
    expect(text(cards[0]!)).toContain("Python 3 → Python 3.12");
    expect(text(cards[3]!)).toContain("moved from cell 2 to 4");
    expect(text(cards[3]!)).toContain("Content unchanged.");
  });

  it("line-diffs a changed cell's source and its outputs", () => {
    const { root } = mount();
    const card = byClass(root, "fhr-nb__cell")[2]!;
    const lines = byClass(card, "fhr-nb__line").map((tr) => tr.className.split("--")[1] + ":" + text(tr.children[3]!));
    expect(lines).toEqual(["del:w = [1, 2]", "add:w = [1, 2, 3]", "same:print(sum(w))", "del:3", "add:6"]);
    expect(byClass(card, "fhr-nb__part").map(text)).toEqual(["Outputs"]);
  });

  it("draws an added cell as additions and a removed one as removals", () => {
    const { root } = mount();
    const [, added, , , removed] = byClass(root, "fhr-nb__cell");
    expect(byClass(added!, "fhr-nb__line--add").length).toBe(1);
    expect(byClass(removed!, "fhr-nb__line--del").length).toBe(1);
  });

  it("reports a cell selection to the host and follows the host's selection", () => {
    const events: unknown[] = [];
    const { root, cleanup } = mount({ onEvent: (e) => events.push(e), selectedChangePath: "cells/widths" });
    const card = byClass(root, "fhr-nb__cell")[2]!;
    expect(card.attrs["aria-selected"]).toBe("true");
    byClass(card, "fhr-nb__head")[0]!.listeners["click"]![0]!();
    expect(events).toEqual([{ type: "select", changePath: "cells/widths" }]);
    cleanup();
    expect(byClass(card, "fhr-nb__head")[0]!.listeners["click"]).toEqual([]);
  });

  it("says so when there is nothing to draw", () => {
    expect(text(mount({ diff: { version: "1.0", format: "ipynb", changes: [] } }).root)).toContain("No changes.");
  });

  it("follows the host theme", () => {
    expect(byClass(mount({ theme: "dark" }).root, "fhr-nb")[0]!.attrs["data-theme"]).toBe("dark");
  });
});
