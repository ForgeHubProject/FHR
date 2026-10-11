import { describe, it, expect } from "vitest";
import { PerspectiveCamera, Vector3 } from "three";
import { applyCameraState, cameraState, withPaneAspect } from "./camera-sync.js";
import { splitPanes } from "./split.js";

const orbit = (): { target: Vector3; update(): void } => ({ target: new Vector3(), update(): void {} });

describe("cameraState / applyCameraState", () => {
  it("copies the orbit state exactly — never an approximation", () => {
    // The mode's whole claim is that a difference you see is a difference in the
    // model. A rig that is *nearly* in step turns parallax into evidence.
    const a = new PerspectiveCamera(50, 1.5, 0.1, 100);
    const ca = orbit();
    a.position.set(1.2345678, -9.87654321, 0.000001);
    ca.target.set(-0.5, 2.25, 7.125);

    const b = new PerspectiveCamera(50, 0.4, 0.1, 100);
    const cb = orbit();
    applyCameraState(b, cb, cameraState(a, ca));

    expect(cameraState(b, cb)).toEqual(cameraState(a, ca));
    // Aspect is a property of the pane, not of the shared state.
    expect(b.aspect).toBe(0.4);
  });
});

describe("withPaneAspect", () => {
  it("gives each pane its own projection and leaves the rig's alone", () => {
    const camera = new PerspectiveCamera(50, 1.6, 0.1, 100);
    camera.position.set(3, 4, 5);
    const controls = orbit();
    controls.target.set(1, 1, 1);
    const before = cameraState(camera, controls);

    const seen: number[] = [];
    for (const pane of splitPanes("columns", { width: 800, height: 400 })) {
      withPaneAspect(camera, pane.aspect, () => seen.push(camera.aspect));
    }

    expect(seen).toEqual([399 / 400, 399 / 400]);
    // OrbitControls pans against camera.aspect: a rig left holding a pane's
    // aspect would pan at the wrong rate for the rest of the session.
    expect(camera.aspect).toBe(1.6);
    expect(cameraState(camera, controls)).toEqual(before);
  });

  it("restores the aspect even when the draw throws", () => {
    const camera = new PerspectiveCamera(50, 2, 0.1, 100);
    expect(() =>
      withPaneAspect(camera, 0.5, () => {
        throw new Error("context lost mid-pass");
      }),
    ).toThrow("context lost mid-pass");
    expect(camera.aspect).toBe(2);
  });

  it("ignores a degenerate aspect rather than dividing by zero", () => {
    const camera = new PerspectiveCamera(50, 2, 0.1, 100);
    let inside = 0;
    withPaneAspect(camera, 0, () => {
      inside = camera.aspect;
    });
    expect(inside).toBe(2);
  });
});
