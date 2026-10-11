// Test-only DOM stand-in. Not exported from the package entry.
//
// The DOM renderDiffTree touches is deliberately small (createElement, class and
// style writes, textContent, append/appendChild/remove, get/setAttribute,
// add/removeEventListener, focus, scrollIntoView), so a hundred lines of plain
// objects cover it. That keeps the suite dependency-free — no jsdom in the
// lockfile for a handful of element assertions — and keeps these tests honest
// about what they prove: DOM structure, text and attributes, never layout.
//
// A near-twin of this file lives in renderer-gltf-scene (added with the real
// renderer in FHR #44); the two are deliberately independent, because a package's
// test double is not a contract other packages should import.

export type FakeElement = {
  tagName: string;
  className: string;
  style: Record<string, string> & { cssText: string };
  textContent: string;
  childNodes: FakeElement[];
  parentNode: FakeElement | null;
  attributes: Record<string, string>;
  listeners: Record<string, ((ev: unknown) => void)[]>;
  /** Test helper: times focus()/scrollIntoView() were called. */
  focused: number;
  scrolled: number;
  appendChild(child: FakeElement): FakeElement;
  append(...children: (FakeElement | string)[]): void;
  remove(): void;
  removeAttribute(name: string): void;
  setAttribute(name: string, value: string): void;
  getAttribute(name: string): string | null;
  addEventListener(type: string, fn: (ev: unknown) => void): void;
  removeEventListener(type: string, fn: (ev: unknown) => void): void;
  focus(): void;
  scrollIntoView(): void;
  /** Test helper: fire the listeners registered on this element. */
  fire(type: string, ev?: unknown): void;
  /** Test helper: this element's text plus all descendants', joined. */
  allText(): string;
  /** Test helper: every descendant (self excluded), depth first. */
  descendants(): FakeElement[];
  /** Test helper: descendants whose className contains `token`. */
  byClass(token: string): FakeElement[];
  ownerDocument: FakeDocument;
};

export type FakeDocument = { createElement(tagName: string): FakeElement };

/** A document whose elements record what was done to them. */
export function createFakeDocument(): FakeDocument {
  const doc: FakeDocument = {
    createElement(tagName: string): FakeElement {
      const el: FakeElement = {
        tagName: tagName.toUpperCase(),
        className: "",
        style: { cssText: "" },
        textContent: "",
        childNodes: [],
        parentNode: null,
        attributes: {},
        listeners: {},
        focused: 0,
        scrolled: 0,
        ownerDocument: doc,
        appendChild(child: FakeElement): FakeElement {
          child.parentNode = el;
          el.childNodes.push(child);
          return child;
        },
        append(...children: (FakeElement | string)[]): void {
          for (const c of children) {
            if (typeof c === "string") {
              const text = doc.createElement("#text");
              text.textContent = c;
              el.appendChild(text);
            } else {
              el.appendChild(c);
            }
          }
        },
        remove(): void {
          const parent = el.parentNode;
          if (!parent) return;
          parent.childNodes = parent.childNodes.filter((c) => c !== el);
          el.parentNode = null;
        },
        setAttribute(name: string, value: string): void {
          el.attributes[name] = value;
        },
        getAttribute(name: string): string | null {
          return el.attributes[name] ?? null;
        },
        removeAttribute(name: string): void {
          delete el.attributes[name];
        },
        addEventListener(type: string, fn: (ev: unknown) => void): void {
          (el.listeners[type] ??= []).push(fn);
        },
        removeEventListener(type: string, fn: (ev: unknown) => void): void {
          el.listeners[type] = (el.listeners[type] ?? []).filter((f) => f !== fn);
        },
        focus(): void {
          el.focused += 1;
        },
        scrollIntoView(): void {
          el.scrolled += 1;
        },
        fire(type: string, ev: unknown = {}): void {
          for (const fn of [...(el.listeners[type] ?? [])]) fn(ev);
        },
        allText(): string {
          return [el.textContent, ...el.childNodes.map((c) => c.allText())].join(" ").trim();
        },
        descendants(): FakeElement[] {
          return el.childNodes.flatMap((c) => [c, ...c.descendants()]);
        },
        byClass(token: string): FakeElement[] {
          return el.descendants().filter((c) => c.className.split(/\s+/).includes(token));
        },
      };
      return el;
    },
  };
  return doc;
}

/** A container element, typed as an HTMLElement for functions that expect one. */
export function asElement(el: FakeElement): HTMLElement {
  return el as unknown as HTMLElement;
}

/** A keyboard-event stand-in that records preventDefault(). */
export function fakeKey(
  key: string,
  extra: Partial<{ target: unknown; ctrlKey: boolean; metaKey: boolean; altKey: boolean }> = {},
): { key: string; prevented: number; preventDefault(): void } & Record<string, unknown> {
  return {
    key,
    prevented: 0,
    preventDefault(): void {
      (this as { prevented: number }).prevented += 1;
    },
    ...extra,
  };
}
