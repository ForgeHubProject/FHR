// The single viewport callout: the projection maths (real three.js camera, no
// WebGL), the placement rules, and the DOM it writes (fake document — structure
// and style text only, never layout).

import { describe, it, expect } from "vitest";
import { PerspectiveCamera, Vector3 } from "three";
import { calloutPlacement, createCallout, projectToScreen } from "./callout.js";
import { createFakeDocument, asElement, type FakeElement } from "./fake-dom.js";

const view = { width: 400, height: 200 };

function camera(): PerspectiveCamera {
  const c = new PerspectiveCamera(50, view.width / view.height, 0.1, 100);
  c.position.set(0, 0, 5);
  c.lookAt(0, 0, 0);
  c.updateMatrixWorld(true);
  return c;
}

describe("projectToScreen", () => {
  it("puts a point on the view axis at the centre of the viewport", () => {
    const p = projectToScreen(new Vector3(0, 0, 0), camera(), view);
    expect(p.x).toBeCloseTo(view.width / 2, 5);
    expect(p.y).toBeCloseTo(view.height / 2, 5);
    expect(p.visible).toBe(true);
  });

  it("puts a point above the axis higher up the screen", () => {
    const p = projectToScreen(new Vector3(0, 1, 0), camera(), view);
    expect(p.y).toBeLessThan(view.height / 2);
  });

  it("reports a point behind the camera as not visible", () => {
    const p = projectToScreen(new Vector3(0, 0, 20), camera(), view);
    expect(p.visible).toBe(false);
  });
});

describe("calloutPlacement", () => {
  it("anchors on the projected point", () => {
    const placement = calloutPlacement({ x: 100, y: 80, visible: true }, view);
    expect(placement).toMatchObject({ left: 100, top: 80, flip: false, onScreen: true });
  });

  it("flips the label inwards when there is no room on the right", () => {
    expect(calloutPlacement({ x: 380, y: 80, visible: true }, view).flip).toBe(true);
  });

  // A callout pinned to the edge for a change that is off screen is a lie about
  // where the change is.
  it("is off screen for a point outside the viewport or behind the camera", () => {
    expect(calloutPlacement({ x: -50, y: 80, visible: true }, view).onScreen).toBe(false);
    expect(calloutPlacement({ x: 100, y: 400, visible: true }, view).onScreen).toBe(false);
    expect(calloutPlacement({ x: 100, y: 80, visible: false }, view).onScreen).toBe(false);
  });
});

describe("createCallout", () => {
  function mount() {
    const doc = createFakeDocument();
    const container = doc.createElement("div");
    const callout = createCallout(asElement(container));
    return { container, callout, root: container.childNodes[0]! as FakeElement };
  }

  it("starts hidden — nothing is selected yet", () => {
    const { callout, root } = mount();
    expect(callout.visible).toBe(false);
    expect(root.style["display"]).toBe("none");
  });

  it("shows the change and its headline number", () => {
    const { callout, root } = mount();
    callout.show("Wheel_FL", "moved 50 mm");
    expect(callout.visible).toBe(true);
    expect(root.style["display"]).toBe("block");
    expect(root.allText()).toContain("Wheel_FL");
    expect(root.allText()).toContain("moved 50 mm");
  });

  it("has a dot, a leader line and a label — one callout, not a legend", () => {
    const { root } = mount();
    expect(root.childNodes.length).toBe(3);
    expect(root.childNodes[1]!.style.cssText).toContain("transform-origin:0 50%");
  });

  it("moves to the projected point", () => {
    const { callout, root } = mount();
    callout.show("Wheel_FL", "moved 50 mm");
    callout.place({ x: 120, y: 60, visible: true }, view);
    expect(root.style["transform"]).toBe("translate(120px, 60px)");
  });

  it("hides itself rather than pointing at the wrong place", () => {
    const { callout, root } = mount();
    callout.show("Wheel_FL", "moved 50 mm");
    callout.place({ x: 120, y: 60, visible: false }, view);
    expect(root.style["display"]).toBe("none");
  });

  it("stays put when nothing is selected", () => {
    const { callout, root } = mount();
    callout.place({ x: 120, y: 60, visible: true }, view);
    expect(root.style["display"]).toBe("none");
  });

  it("flips the label near the right edge", () => {
    const { callout, root } = mount();
    callout.show("Wheel_FL", "moved 50 mm");
    callout.place({ x: 395, y: 60, visible: true }, view);
    const label = root.childNodes[2]!;
    expect(label.style["transform"]).toBe("translate(-100%, -50%)");
  });

  it("takes itself out of the DOM on dispose", () => {
    const { container, callout } = mount();
    callout.dispose();
    expect(container.childNodes).toEqual([]);
  });
});
