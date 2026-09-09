// Test-only DOM stand-in. Not imported by any bundle entry.
//
// The DOM this renderer's non-3D code touches is deliberately tiny
// (createElement, style.cssText, textContent, appendChild/append, remove,
// addEventListener), so a few dozen lines of plain objects cover it. That keeps
// the test suite dependency-free — no jsdom in the lockfile for a handful of
// element assertions — and keeps these tests honest about what they prove: DOM
// *structure and text*, never layout or rendering.

export type FakeElement = {
  tagName: string;
  className: string;
  style: Record<string, string> & { cssText: string };
  textContent: string;
  childNodes: FakeElement[];
  parentNode: FakeElement | null;
  attributes: Record<string, string>;
  listeners: Record<string, ((ev: unknown) => void)[]>;
  appendChild(child: FakeElement): FakeElement;
  append(...children: (FakeElement | string)[]): void;
  replaceChildren(...children: FakeElement[]): void;
  remove(): void;
  removeAttribute(name: string): void;
  setAttribute(name: string, value: string): void;
  addEventListener(type: string, fn: (ev: unknown) => void): void;
  removeEventListener(type: string, fn: (ev: unknown) => void): void;
  getAttribute(name: string): string | null;
  focus(): void;
  /** Test helper: times focus() was called. */
  focused: number;
  /** Test helper: fire a listener registered on this element. */
  fire(type: string, ev?: unknown): void;
  /** Test helper: this element's text plus all descendants', joined. */
  allText(): string;
  /** Test helper: every descendant (self excluded), depth first. */
  descendants(): FakeElement[];
  /** Test helper: descendants whose className contains `token`. */
  byClass(token: string): FakeElement[];
  /** Test helper: descendants carrying an attribute with this value. */
  byAttr(name: string, value: string): FakeElement[];
  ownerDocument: FakeDocument;
  clientWidth: number;
  clientHeight: number;
};

export type FakeDocument = {
  createElement(tagName: string): FakeElement;
};

/** A document whose elements record what was done to them. */
export function createFakeDocument(): FakeDocument {
  const doc: FakeDocument = {
    createElement(tagName: string): FakeElement {
      const el: FakeElement = {
        tagName: tagName.toUpperCase(),
        className: "",
        style: { cssText: "" },
        focused: 0,
        textContent: "",
        childNodes: [],
        parentNode: null,
        attributes: {},
        listeners: {},
        clientWidth: 640,
        clientHeight: 420,
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
        replaceChildren(...children: FakeElement[]): void {
          for (const c of el.childNodes) c.parentNode = null;
          el.childNodes = [];
          for (const c of children) el.appendChild(c);
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
        focus(): void {
          el.focused += 1;
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
        byAttr(name: string, value: string): FakeElement[] {
          return el.descendants().filter((c) => c.attributes[name] === value);
        },
      };
      return el;
    },
  };
  return doc;
}

/** The fake document, typed as a DOM Document for functions that expect one. */
export function asDocument(doc: FakeDocument): Document {
  return doc as unknown as Document;
}

/** A fake element, typed as an HTMLElement for functions that expect one. */
export function asElement(el: FakeElement): HTMLElement {
  return el as unknown as HTMLElement;
}
