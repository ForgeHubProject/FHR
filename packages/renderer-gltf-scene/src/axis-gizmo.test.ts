// The orientation gizmo, headless.
//
// The projection is the part that can be wrong in a way nobody notices: an axis
// cross that is subtly mirrored or has y the wrong way up still looks like an
// axis cross, and it will confidently tell a reviewer the model is facing the
// opposite way. So each case below fixes a camera somewhere with a known answer
// and checks the tip lands where a person looking at the screen would point.
//
// The DOM half is the same deal as the callout's: structure and writes, never
// pixels.

import { describe, it, expect } from "vitest";
import { Euler, PerspectiveCamera, Quaternion } from "three";
import { createFakeDocument, asElement, type FakeElement } from "./fake-dom.js";
import { axisTips, createAxisGizmo, GIZMO_RADIUS, type AxisName, type AxisTip } from "./axis-gizmo.js";

/** The camera's world orientation, from where it is and what it looks at. */
const lookingFrom = (x: number, y: number, z: number): Quaternion => {
  const camera = new PerspectiveCamera(50, 1.5, 0.1, 5000);
  camera.position.set(x, y, z);
  camera.lookAt(0, 0, 0);
  camera.updateMatrixWorld();
  return camera.quaternion.clone();
};

const tip = (tips: AxisTip[], axis: AxisName): AxisTip => tips.find((t) => t.axis === axis)!;

describe("axisTips", () => {
  it("puts X right, Y up and Z at the viewer for the default camera", () => {
    // Down the −Z axis at the origin: the arrangement every scene opens near,
    // and the one a reader checks the widget against first.
    const tips = axisTips(lookingFrom(0, 0, 10));
    expect(tip(tips, "x").x).toBeCloseTo(GIZMO_RADIUS);
    expect(tip(tips, "x").y).toBeCloseTo(0);
    // CSS y grows downwards, so "up" is negative here — flipping this is the
    // mirror bug that still looks like a perfectly good axis cross.
    expect(tip(tips, "y").y).toBeCloseTo(-GIZMO_RADIUS);
    expect(tip(tips, "z").toward).toBeCloseTo(1);
  });

  it("turns X towards the viewer when the camera swings round to +X", () => {
    const tips = axisTips(lookingFrom(10, 0, 0));
    expect(tip(tips, "x").toward).toBeCloseTo(1);
    // Seen from +X, world +Z runs to screen *left* — getting this backwards is
    // the mirrored gizmo that still looks perfectly plausible.
    expect(tip(tips, "z").x).toBeCloseTo(-GIZMO_RADIUS);
    expect(tip(tips, "y").y).toBeCloseTo(-GIZMO_RADIUS);
  });

  it("fades the axes pointing away from the viewer", () => {
    // From behind the model, +Z now points into the screen.
    expect(tip(axisTips(lookingFrom(0, 0, -10)), "z").toward).toBeLessThan(0);
    expect(tip(axisTips(lookingFrom(0, 0, -10)), "x").toward).toBeCloseTo(0);
  });

  it("keeps every tip inside the widget, whatever the camera is doing", () => {
    const orientation = new Quaternion().setFromEuler(new Euler(0.7, -1.2, 0.3));
    for (const t of axisTips(orientation)) {
      expect(Math.hypot(t.x, t.y)).toBeLessThanOrEqual(GIZMO_RADIUS + 1e-6);
      expect(Math.abs(t.toward)).toBeLessThanOrEqual(1 + 1e-6);
    }
  });

  it("is about rotation alone — dollying in does not change it", () => {
    // An orientation gizmo that grew as the camera closed in would be reporting
    // distance, which the reviewer can already see.
    const near = axisTips(lookingFrom(3, 3, 3));
    const far = axisTips(lookingFrom(300, 300, 300));
    for (const axis of ["x", "y", "z"] as const) {
      expect(tip(near, axis).x).toBeCloseTo(tip(far, axis).x);
      expect(tip(near, axis).y).toBeCloseTo(tip(far, axis).y);
    }
  });
});

describe("createAxisGizmo", () => {
  const setup = () => {
    const doc = createFakeDocument();
    const container = doc.createElement("div");
    const gizmo = createAxisGizmo(asElement(container), "light");
    return { container, gizmo };
  };

  const lineFor = (container: FakeElement, axis: string): FakeElement =>
    container.byAttr("data-axis", axis)[0]!;
  const tipFor = (container: FakeElement, axis: string): FakeElement =>
    container.byAttr("data-axis-tip", axis)[0]!;

  it("labels the axes rather than relying on their colours", () => {
    // Red and green side by side is the pair palette.ts exists to keep out of
    // this renderer; the letters are what makes this one legible anyway.
    const { container } = setup();
    expect(["x", "y", "z"].map((a) => tipFor(container, a).textContent)).toEqual(["X", "Y", "Z"]);
  });

  it("takes no pointer events — it is a readout, not a control", () => {
    const { container } = setup();
    expect(container.byAttr("data-gizmo", "1")[0]!.style.cssText).toContain("pointer-events:none");
  });

  it("points the axes at the camera's orientation", () => {
    const { container, gizmo } = setup();
    gizmo.update(lookingFrom(0, 0, 10));
    // X runs to screen right: no rotation, full length.
    expect(lineFor(container, "x").style.transform).toContain("rotate(0deg)");
    expect(tipFor(container, "x").style.transform).toBe(`translate(${GIZMO_RADIUS}px, 0px)`);
    // Z points at the viewer, so it collapses to a dot in the middle.
    expect(tipFor(container, "z").style.transform).toBe("translate(0px, 0px)");
    expect(lineFor(container, "z").style.transform).toContain("scaleX(0)");
  });

  it("dims an axis once it points away from the viewer", () => {
    const { container, gizmo } = setup();
    gizmo.update(lookingFrom(0, 0, 10));
    expect(tipFor(container, "z").style.opacity).toBe("1");
    gizmo.update(lookingFrom(0, 0, -10));
    expect(tipFor(container, "z").style.opacity).not.toBe("1");
  });

  it("writes nothing on a frame the camera didn't turn", () => {
    // It runs once per frame for the life of the view, and a reviewer reads for
    // far longer than they orbit.
    const { container, gizmo } = setup();
    const still = lookingFrom(2, 3, 4);
    gizmo.update(still);
    const line = lineFor(container, "x");
    line.style.transform = "sentinel";
    gizmo.update(still);
    expect(line.style.transform).toBe("sentinel");
    gizmo.update(lookingFrom(4, 3, 2));
    expect(line.style.transform).not.toBe("sentinel");
  });

  it("takes itself out of the viewport when disposed", () => {
    const { container, gizmo } = setup();
    gizmo.dispose();
    expect(container.byAttr("data-gizmo", "1")).toEqual([]);
  });
});
